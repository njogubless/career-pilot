# syntax=docker/dockerfile:1
FROM python:3.15-slim
ENV PYTHONDONTWRITEBYTECODE=1 PYTHONUNBUFFERED=1 PIP_NO_CACHE_DIR=1
# Apply Debian security updates at build time (the base image can lag them;
# `make scan-images` / CI Trivy fail on fixable HIGH and CRITICAL issues).
RUN apt-get update && apt-get upgrade -y --no-install-recommends && rm -rf /var/lib/apt/lists/*
WORKDIR /app
# requirements.lock pins every transitive dependency with hashes
# (regenerate: pip-compile --generate-hashes --allow-unsafe --strip-extras
#  --no-emit-index-url -o requirements.lock requirements.txt).
COPY requirements.lock .
RUN pip install --require-hashes -r requirements.lock
# Bake the embedding model into the image so the service never downloads
# models at runtime (EMBEDDING_OFFLINE=true below).
ENV EMBEDDING_CACHE_DIR=/opt/models EMBEDDING_OFFLINE=true HF_HUB_DISABLE_TELEMETRY=1
RUN python -c "from fastembed import TextEmbedding; TextEmbedding('BAAI/bge-small-en-v1.5', cache_dir='/opt/models')" \
    && chmod -R a+rX /opt/models
COPY app ./app
ARG VERSION=dev
ARG REVISION=unknown
ENV APP_VERSION=$VERSION
LABEL org.opencontainers.image.title="careerpilot-ai" \
      org.opencontainers.image.description="CareerPilot AI intelligence service" \
      org.opencontainers.image.version=$VERSION \
      org.opencontainers.image.revision=$REVISION
RUN useradd --system --uid 10001 app
USER app
EXPOSE 8090
CMD ["uvicorn", "app.main:create_app", "--factory", "--host", "0.0.0.0", "--port", "8090", "--no-server-header", "--timeout-graceful-shutdown", "20"]
