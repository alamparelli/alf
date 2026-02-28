# Stage 1: Build Go daemon
FROM --platform=$BUILDPLATFORM golang:1.24-alpine AS builder
ARG TARGETARCH

WORKDIR /src
COPY go.mod ./
# COPY go.sum ./ (uncomment when dependencies exist)
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=$TARGETARCH go build -ldflags="-s -w" -o /alf-daemon ./cmd/alf-daemon

# Stage 2: Runtime with Claude Code CLI
FROM node:22-slim

ARG TARGETARCH

RUN apt-get update && apt-get install -y --no-install-recommends \
    bash \
    ca-certificates \
    curl \
    git \
    trash-cli \
    && rm -rf /var/lib/apt/lists/*

# Install Go for Claude agent to build CLI tools
RUN curl -fsSL "https://go.dev/dl/go1.24.1.linux-${TARGETARCH}.tar.gz" | tar -C /usr/local -xz
ENV PATH="/usr/local/go/bin:/home/node/go/bin:${PATH}"
ENV GOPATH="/home/node/go"

RUN npm install -g @anthropic-ai/claude-code && npm cache clean --force

COPY --from=builder /alf-daemon /opt/alf/alf-daemon

# Directories with proper ownership — single user model (node).
RUN mkdir -p /home/node/data/logs /home/node/data/sessions \
    && mkdir -p /home/node/data/tools /home/node/data/skills \
    && mkdir -p /home/node/data/.claude \
    && mkdir -p /opt/alf/config \
    && chown -R node:node /home/node \
    && chown -R node:node /opt/alf

WORKDIR /home/node
USER node

EXPOSE 8080

CMD ["/opt/alf/alf-daemon"]
