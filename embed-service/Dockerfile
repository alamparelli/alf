# Embed service: ONNX embedding server (same pattern as whisper-service).
# Builds the Go binary, bundles ONNX runtime + multilingual-e5-small model.

# Stage 1: Build Go binary with CGO (sqlite-vec for vec0 table format).
FROM golang:1.25-bookworm AS builder

ARG TARGETARCH

RUN apt-get update && apt-get install -y --no-install-recommends \
    gcc g++ libc6-dev libsqlite3-dev && rm -rf /var/lib/apt/lists/*

# Copy vault-proxy source (needed as Go module dependency).
WORKDIR /vault-proxy
COPY third_party/vault-proxy/ .

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod edit -replace github.com/alessandrolamparelli/vault-proxy=/vault-proxy \
    && go mod download

COPY . .
RUN go mod edit -replace github.com/alessandrolamparelli/vault-proxy=/vault-proxy

RUN CGO_ENABLED=1 go build -tags fts5 -ldflags="-s -w" -o /embed-server ./cmd/embed-server

# Stage 2: Minimal runtime with ONNX.
FROM debian:bookworm-slim

ARG TARGETARCH

RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates curl && rm -rf /var/lib/apt/lists/*

# ONNX Runtime shared library (CPU-only, ~20 MB).
RUN if [ "${TARGETARCH}" = "amd64" ]; then ARCH="x64"; \
    elif [ "${TARGETARCH}" = "arm64" ]; then ARCH="aarch64"; \
    else ARCH="${TARGETARCH}"; fi \
    && curl -fsSL "https://github.com/microsoft/onnxruntime/releases/download/v1.24.2/onnxruntime-linux-${ARCH}-1.24.2.tgz" \
       | tar xz --strip-components=2 -C /usr/local/lib \
         "onnxruntime-linux-${ARCH}-1.24.2/lib/libonnxruntime.so.1.24.2" \
         "onnxruntime-linux-${ARCH}-1.24.2/lib/libonnxruntime.so.1" \
         "onnxruntime-linux-${ARCH}-1.24.2/lib/libonnxruntime.so" \
    && ldconfig

# Pre-download embedding model (multilingual-e5-small: 100 languages, 384 dims).
RUN mkdir -p /opt/alf/models/multilingual-e5-small \
    && curl -fsSL -o /opt/alf/models/multilingual-e5-small/model.onnx \
       "https://huggingface.co/intfloat/multilingual-e5-small/resolve/main/onnx/model.onnx" \
    && curl -fsSL -o /opt/alf/models/multilingual-e5-small/tokenizer.json \
       "https://huggingface.co/intfloat/multilingual-e5-small/resolve/main/tokenizer.json"

RUN useradd -m -u 1000 embed

COPY --from=builder /embed-server /usr/local/bin/embed-server

USER embed

EXPOSE 8090

HEALTHCHECK --interval=30s --timeout=5s --retries=3 \
    CMD curl -sf http://localhost:8090/health || exit 1

CMD ["embed-server"]
