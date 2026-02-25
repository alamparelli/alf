# Stage 1: Build Go daemon
FROM golang:1.24-alpine AS builder

WORKDIR /src
COPY go.mod ./
# COPY go.sum ./ (uncomment when dependencies exist)
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /alf-daemon ./cmd/alf-daemon

# Stage 2: Runtime with Claude Code CLI
FROM node:22-alpine

# Reuse node user (uid/gid 1000 already exists in node:22-alpine)
RUN npm install -g @anthropic-ai/claude-code && npm cache clean --force

COPY --from=builder /alf-daemon /opt/alf/alf-daemon

RUN mkdir -p /home/node/.claude && chown -R node:node /home/node

USER node
WORKDIR /home/node

ENTRYPOINT ["/opt/alf/alf-daemon"]
