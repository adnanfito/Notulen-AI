# syntax=docker/dockerfile:1

# CTranslate2 4.x (under faster-whisper 1.x) links against cuDNN 9, so the
# plain -runtime image is not enough — it must be a -cudnn- tag or the model
# fails at load time rather than at build time.
FROM nvidia/cuda:12.4.1-cudnn-runtime-ubuntu22.04

ENV DEBIAN_FRONTEND=noninteractive \
    PYTHONUNBUFFERED=1 \
    PYTHONDONTWRITEBYTECODE=1 \
    HF_HOME=/models

# Ubuntu 22.04 ships Python 3.10, which satisfies the PRD's 3.10+ requirement.
RUN apt-get update && apt-get install -y --no-install-recommends \
        python3 \
        python3-pip \
        curl \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app

COPY requirements.txt .
# Slow links truncate large wheels mid-download, and --no-cache-dir made every
# failure restart all of them. The cache mount keeps completed wheels between
# build attempts; it lives outside the layer, so the image stays the same size.
RUN --mount=type=cache,target=/root/.cache/pip \
    pip3 install --retries 10 --timeout 120 -r requirements.txt

COPY app ./app

# Model weights live on a named volume so rebuilds don't re-download them.
RUN mkdir -p /models

EXPOSE 8000

HEALTHCHECK --interval=15s --timeout=5s --start-period=180s --retries=3 \
    CMD curl -fsS http://127.0.0.1:8000/health || exit 1

# Single worker: one process, one model, one GPU (PRD §5).
CMD ["uvicorn", "app.main:app", "--host", "0.0.0.0", "--port", "8000", "--workers", "1"]
