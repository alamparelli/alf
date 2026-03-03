# ALF Project Guide

## Quick Links

- **Architecture:** [ARCHITECTURE.md](.plans/ARCHITECTURE.md) — System design, security model, filesystem layout
- **Deployment:** `./scripts/dev-deploy.sh` — Local homelab deploy
- **Release:** `./scripts/release.sh` — Version bump and CI trigger

## Development

### Local Build
```bash
go build ./...
```

### Deploy to Homelab
```bash
./scripts/dev-deploy.sh
```

Builds CLI + Docker image, transfers to 192.168.129.101, restarts.

### Testing Changes
After modifying daemon/CC:
1. Build: `go build ./...`
2. Deploy: `./scripts/dev-deploy.sh`
3. Check logs: `docker logs -f alf`

## Key Principles

- **User isolation:** Claude runs as restricted `claude` user (uid 1001)
- **Config protection:** `/opt/alf/config/` is read-only for Claude
- **System tools:** Image-baked in `/opt/alf/tools/`, discovered via `/home/node/data/tools.d` symlink
- **No overwrites:** New commits, not amends (unless explicitly requested)

## Architecture Overview

See [ARCHITECTURE.md](./.claude/ARCHITECTURE.md) for full details.

**Process model:**
```
alf-daemon (root) → spawns claude -p (claude user, uid 1001)
                  ↳ hosts Control Center HTTP (port 8080)
                  ↳ runs Telegram bot loop
```

**Security:**
- Daemon runs as root for `setuid` on subprocesses
- Claude can read config, write to data, but **not** write config
- Tools are rx-only for Claude

## File Structure

```
cmd/
  alf/          — CLI binary (host-side)
  alf-daemon/   — Main daemon (container)
  extract-video/ — Tool for video analysis

internal/
  cli/          — CLI commands
  controlcenter/ — CC HTTP server
  media/        — Frame/audio extraction
  voice/        — Whisper transcription
  router/       — Message routing
  session/      — Chat session tracking
  tierfs/       — Per-tier filesystem

scripts/
  dev-deploy.sh — Homelab deployment
  release.sh    — Version release
  transcribe.py — Faster-whisper server
```

## Common Tasks

### Add a New System Tool

1. Create `cmd/<tool>/main.go`
2. Update `Dockerfile` build step:
   ```dockerfile
   RUN ... && go build -o /<tool> ./cmd/<tool>
   ```
3. Copy to tools dir:
   ```dockerfile
   COPY --from=builder /<tool> /opt/alf/tools/<tool>
   ```
4. Tool auto-available at `/home/node/data/tools.d/<tool>`

### Update Tiers Configuration

Via Control Center:
1. Navigate to `http://<cc-url>:8080`
2. Login via `/login` Telegram command
3. Edit tiers in dashboard
4. Save → triggers daemon reload

### Add Context

Edit `/home/node/data/context/index.md` via CC or host filesystem.

Auto-injected into every Claude prompt.

## Troubleshooting

### Claude can write to config
Check user isolation:
```bash
docker exec alf ps aux | grep claude
# Should show claude running as uid 1001
```

### Video processing fails
Check ffmpeg:
```bash
docker exec alf ffmpeg -version
```

### Whisper not transcribing
Check model loading:
```bash
docker logs alf | grep "Model loaded"
```
