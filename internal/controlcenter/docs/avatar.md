---
category: Reference
tags: avatar, profile, image, LLM, personalization, security
order: 24
---

# Avatar

The LLM can change its own profile avatar using the `avatar` system tool or the REST API.

## System tool

Avatar management is part of the `config` tool (no dedicated tool — avoids schema bloat for a rarely-used action):

```bash
# Set a new avatar (base64-encoded PNG, JPEG, or WebP)
config avatar-set <base64_image_data>

# Reset to default
config avatar-reset

# Check current status
config avatar-status
```

For API tiers, use the `config` tool with `action: "avatar-set"` and `image: "<base64>"`.

## REST API

| Method | Endpoint | Description |
|--------|----------|-------------|
| `PUT` | `/api/settings/avatar` | Upload avatar (JSON: `{"image": "<base64>"}`) |
| `GET` | `/api/settings/avatar` | Serve current avatar (PNG) or 404 |
| `DELETE` | `/api/settings/avatar` | Reset to default |

## Image sanitization

All uploaded images pass through a mandatory sanitization pipeline:

1. **Size gate** — reject raw input > 256KB
2. **Magic byte validation** — only PNG (`89 50 4E 47`), JPEG (`FF D8 FF`), and WebP (`52 49 46 46`) headers accepted. SVG, GIF, BMP, TIFF are rejected.
3. **Decode** — `image.Decode()` parses the pixel data. Corrupt or polyglot files fail here.
4. **Resize** — scaled to 128x128 using Catmull-Rom interpolation
5. **Re-encode** — output as clean PNG with no metadata

### Why re-encode?

The decode→resize→encode cycle is the core security mechanism. It destroys:

- **SVG script injection** — SVGs are rejected at magic byte validation
- **Polyglot files** — files that are valid images AND valid HTML/JS are neutralized because only pixel data survives re-encoding
- **EXIF/metadata payloads** — all metadata is stripped (Go's PNG encoder writes only pixel data)
- **Steganographic content** — resizing alters pixel values, disrupting hidden data

### Response headers

The `GET` endpoint serves avatars with strict security headers:

```
Content-Type: image/png
X-Content-Type-Options: nosniff
Content-Security-Policy: default-src 'none'
Cache-Control: no-cache
```

## Storage

- **File**: `data/config/avatar.png`
- **Format**: Always PNG, always 128x128
- **Fallback**: Frontend falls back to `/static/favicon.png` if no custom avatar is set (HTTP 404)

## Frontend

Chat messages load the avatar from `/api/settings/avatar` with an `onerror` fallback to the default favicon:

```html
<img src="/api/settings/avatar" onerror="this.src='/static/favicon.png'" />
```
