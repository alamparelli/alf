# Memory System

ALF's memory lets the assistant remember facts, preferences, and context across conversations.

## How It Works

Memory has two components:

### 1. Extraction (LLM)

After each conversation, an LLM reads the messages and extracts useful facts (user preferences, decisions, project context). These are stored in a SQLite database.

**Configuration:** Tiers > Memory LLM button. By default, uses the same backend and model as the router — no extra setup needed.

| Setting | Default | Description |
|---------|---------|-------------|
| Backend | same as router | Which API provider runs extraction |
| Model | same as router | Which model analyzes conversations |

You only need to change this if you want extraction to use a different (cheaper/faster) model than your router.

### 2. Embedding (Vector Search)

When the assistant receives a message, it searches memory for relevant facts using semantic similarity. This requires an embedding model that converts text into vectors.

**Options:**

| Option | Setup | Quality | Cost |
|--------|-------|---------|------|
| **Local (default)** | Embed service sidecar container | Good | Free (runs on your hardware) |
| **None** | No embed service | Basic (FTS5 keyword search only) | Free |

The local embed service runs automatically if `EMBED_URL` is set in your docker-compose (included in standard install). It uses a small ONNX model (~100MB) for fast local inference.

### Memory Flow

```
User message
    |
    v
[Recall] -- embedding search --> relevant memories injected into prompt
    |
    v
[LLM responds]
    |
    v
[Extraction] -- LLM analyzes conversation --> new facts stored
```

## Troubleshooting

**"max instances reached" (429):** The embed service has a connection limit. Restart the ALF container to clear stale registrations.

**"extraction failed: git diff":** On fresh installs with few commits, extraction may fail on the first run. This resolves after a few more conversations.

**Memory not recalling:** Check that the embed service is running (`docker compose ps`). Without it, only keyword search (FTS5) is used — less accurate for semantic queries.
