# Stage 1: Build
FROM golang:1.23-alpine AS builder

WORKDIR /src
COPY go.mod ./
# COPY go.sum ./ (uncomment when dependencies exist)
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /alf-daemon ./cmd/alf-daemon

# Stage 2: Runtime
FROM alpine:3.21

RUN addgroup -g 1000 alf && adduser -u 1000 -G alf -D alf

COPY --from=builder /alf-daemon /opt/alf/alf-daemon

RUN mkdir -p /home/alf/user-space && chown -R alf:alf /home/alf

USER alf
WORKDIR /home/alf

EXPOSE 8080

ENTRYPOINT ["/opt/alf/alf-daemon"]
