"""Text embeddings (ADR-020).

`local` runs BAAI/bge-small-en-v1.5 (384-d) through fastembed/ONNX inside this
service: no per-call cost, and no profile or job text leaves the host.
`hash` is a deterministic feature-hashing embedder for tests and offline
development. It reports its own model id, so vectors from the two can never be
mixed silently, and production rejects it.
"""

from __future__ import annotations

import hashlib
import itertools
import logging
import math
import re
import threading
from typing import Protocol

from app.core.errors import ServiceError

log = logging.getLogger("careerpilot.ai.embeddings")

DIM = 384
LOCAL_MODEL = "BAAI/bge-small-en-v1.5"
# The reported model id includes the input recipe (what text is embedded), so
# changing the recipe re-embeds everything instead of mixing incomparable vectors.
RECIPE = "r2"
HASH_MODEL = "hash-384-v1"
MAX_CHARS = 2000  # the model reads at most 512 tokens; longer text is wasted work


class EmbeddingsUnavailable(ServiceError):
    def __init__(self, message: str = "Embeddings are not configured.") -> None:
        super().__init__("EMBEDDINGS_UNAVAILABLE", message, 503)


class Embedder(Protocol):
    model: str
    dim: int

    def embed(self, texts: list[str]) -> list[list[float]]: ...


class LocalEmbedder:
    model = f"{LOCAL_MODEL}@{RECIPE}"
    dim = DIM

    def __init__(self, cache_dir: str | None, offline: bool, threads: int | None = None) -> None:
        self._cache_dir = cache_dir
        self._offline = offline
        self._threads = threads
        self._lock = threading.Lock()
        self._impl = None

    def _load(self):  # type: ignore[no-untyped-def]
        with self._lock:
            if self._impl is None:
                try:
                    import onnxruntime
                    from fastembed import TextEmbedding
                except ImportError as e:
                    raise EmbeddingsUnavailable("fastembed is not installed.") from e
                # No third-party telemetry from the inference runtime.
                if hasattr(onnxruntime, "disable_telemetry_events"):
                    onnxruntime.disable_telemetry_events()
                try:
                    self._impl = TextEmbedding(
                        model_name=LOCAL_MODEL,
                        cache_dir=self._cache_dir,
                        threads=self._threads,
                        local_files_only=self._offline,
                    )
                except Exception as e:
                    log.exception("embedding model failed to load")
                    raise EmbeddingsUnavailable("The embedding model could not be loaded.") from e
                log.info("embedding model loaded", extra={"fields": {"model": LOCAL_MODEL}})
            return self._impl

    def warm(self) -> None:
        self._load()

    def embed(self, texts: list[str]) -> list[list[float]]:
        impl = self._load()
        clipped = [t[:MAX_CHARS] for t in texts]
        return [[float(x) for x in vec] for vec in impl.embed(clipped, batch_size=16)]


_TOKEN = re.compile(r"[a-z0-9+#.]+")


class HashEmbedder:
    """Signed feature hashing of unigrams and bigrams, L2-normalised. Similar
    texts get similar vectors, which is enough to exercise the pipeline in
    tests. It is not a semantic model."""

    model = HASH_MODEL
    dim = DIM

    def embed(self, texts: list[str]) -> list[list[float]]:
        return [self._one(t[:MAX_CHARS]) for t in texts]

    def _one(self, text: str) -> list[float]:
        vec = [0.0] * DIM
        tokens = _TOKEN.findall(text.lower())
        grams = tokens + [a + " " + b for a, b in itertools.pairwise(tokens)]
        for g in grams:
            h = int.from_bytes(hashlib.blake2b(g.encode(), digest_size=8).digest(), "big")
            vec[h % DIM] += 1.0 if (h >> 63) & 1 else -1.0
        norm = math.sqrt(sum(x * x for x in vec))
        return [x / norm for x in vec] if norm else vec
