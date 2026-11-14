"""Text extraction from uploaded PDF and DOCX files, with resource limits.

Uploaded documents are untrusted: limits guard against decompression bombs
and pathological files. Scanned (image-only) PDFs yield no text and are
reported as unreadable rather than guessed at.
"""

from __future__ import annotations

import io
import re
import zipfile

from pypdf import PdfReader
from pypdf.errors import PdfReadError

from app.core.errors import DocumentTooLong, UnreadableDocument

PDF = "application/pdf"
DOCX = "application/vnd.openxmlformats-officedocument.wordprocessingml.document"

MAX_PAGES = 30
MAX_CHARS = 60_000
MIN_CHARS = 80
MAX_DOCX_UNCOMPRESSED = 40 * 1024 * 1024
MAX_DOCX_ENTRIES = 2_000


def extract_text(data: bytes, mime_type: str) -> str:
    if mime_type == PDF:
        text = _pdf_text(data)
    elif mime_type == DOCX:
        text = _docx_text(data)
    else:
        raise UnreadableDocument("Only PDF and DOCX documents are supported.")
    text = _normalise(text)
    if len(text) < MIN_CHARS:
        raise UnreadableDocument(
            "Almost no text could be extracted. The file may be a scanned image, protected, or empty."
        )
    if len(text) > MAX_CHARS:
        # Refuse rather than silently truncate: a partial CV would produce a misleading profile.
        raise DocumentTooLong(
            f"The document contains more than {MAX_CHARS:,} characters of text; upload a shorter CV."
        )
    return text


def _pdf_text(data: bytes) -> str:
    try:
        reader = PdfReader(io.BytesIO(data))
        if reader.is_encrypted and not reader.decrypt(""):
            raise UnreadableDocument("The PDF is password protected.")
        if len(reader.pages) > MAX_PAGES:
            raise DocumentTooLong(f"The PDF has more than {MAX_PAGES} pages.")
        return "\n".join((page.extract_text() or "") for page in reader.pages)
    except (PdfReadError, ValueError, KeyError, TypeError) as e:
        raise UnreadableDocument("The PDF could not be read.") from e


def _docx_text(data: bytes) -> str:
    try:
        with zipfile.ZipFile(io.BytesIO(data)) as zf:
            infos = zf.infolist()
            if len(infos) > MAX_DOCX_ENTRIES or sum(i.file_size for i in infos) > MAX_DOCX_UNCOMPRESSED:
                raise UnreadableDocument("The Word document is unusually large when decompressed.")
    except zipfile.BadZipFile as e:
        raise UnreadableDocument("The Word document is corrupted.") from e

    from docx import Document  # imported lazily: heavy dependency

    try:
        doc = Document(io.BytesIO(data))
    except Exception as e:  # python-docx raises a variety of parser errors
        raise UnreadableDocument("The Word document could not be read.") from e
    parts = [p.text for p in doc.paragraphs]
    for table in doc.tables:
        for row in table.rows:
            parts.append(" | ".join(cell.text for cell in row.cells))
    return "\n".join(parts)


_WS = re.compile(r"[ \t\u00a0]+")
_BLANKS = re.compile(r"\n{3,}")


def _normalise(text: str) -> str:
    text = text.replace("\r\n", "\n").replace("\r", "\n").replace("\x00", "")
    text = _WS.sub(" ", text)
    return _BLANKS.sub("\n\n", text).strip()
