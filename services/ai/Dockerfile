# syntax=docker/dockerfile:1
FROM python:3.12-slim
ENV PYTHONDONTWRITEBYTECODE=1 PYTHONUNBUFFERED=1 PIP_NO_CACHE_DIR=1
WORKDIR /app
COPY requirements.txt .
RUN pip install -r requirements.txt
# Bake the embedding model into the image so the service never downloads
# models at runtime (EMBEDDING_OFFLINE=true below).
ENV EMBEDDING_CACHE_DIR=/opt/models EMBEDDING_OFFLINE=true HF_HUB_DISABLE_TELEMETRY=1
RUN python -c "from fastembed import TextEmbedding; TextEmbedding('BAAI/bge-small-en-v1.5', cache_dir='/opt/models')" \
    && chmod -R a+rX /opt/models
COPY app ./app
RUN useradd --system --uid 10001 app
USER app
EXPOSE 8090
CMD ["uvicorn", "app.main:create_app", "--factory", "--host", "0.0.0.0", "--port", "8090", "--no-server-header"]
