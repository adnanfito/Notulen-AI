# syntax=docker/dockerfile:1

# CPU-only build. faster-whisper's CTranslate2 wheel ships its own CPU kernels
# and PyAV bundles ffmpeg, so a plain Python image is enough — no CUDA/cuDNN
# layers, which keeps the image a fraction of the size of the GPU variant.
FROM python:3.10-slim-bookworm

ENV PYTHONUNBUFFERED=1 \
    PYTHONDONTWRITEBYTECODE=1 \
    HF_HOME=/models

RUN apt-get update && apt-get install -y --no-install-recommends \
        curl \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app

COPY requirements.txt .
# Slow links truncate large wheels mid-download, and --no-cache-dir made every
# failure restart all of them. The cache mount keeps completed wheels between
# build attempts; it lives outside the layer, so the image stays the same size.
RUN --mount=type=cache,target=/root/.cache/pip \
    pip install --retries 10 --timeout 120 -r requirements.txt

COPY app ./app

# Model weights live on a named volume so rebuilds don't re-download them.
RUN mkdir -p /models

EXPOSE 8000

HEALTHCHECK --interval=15s --timeout=5s --start-period=180s --retries=3 \
    CMD curl -fsS http://127.0.0.1:8000/health || exit 1

# Single worker: one process, one model in RAM (PRD §5).
CMD ["uvicorn", "app.main:app", "--host", "0.0.0.0", "--port", "8000", "--workers", "1"]
