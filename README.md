# homelab-access

Discord-driven access broker for the homelab.

The foundation build exposes:

- `GET /healthz`
- `GET /readyz`
- `GET /metrics`
- `POST /discord/interactions` placeholder
- `GET /download/{token}` placeholder

Future builds will implement admin-approved Discord access requests, Authentik
invite enrollment, wg-easy peer creation, and single-use WireGuard config
delivery.

## Configuration

| Variable | Default | Purpose |
| --- | --- | --- |
| `HTTP_ADDR` | `:8080` | Listen address |
| `PUBLIC_BASE_URL` | none | Public one-time download base URL |
| `DISCORD_APP_ID` | none | Discord application ID |
| `DISCORD_PUBLIC_KEY` | none | Discord interaction signature public key |
| `DISCORD_BOT_TOKEN` | none | Discord bot token |
| `AUTHENTIK_BASE_URL` | none | Authentik base URL |
| `AUTHENTIK_TOKEN` | none | Authentik API token |
| `WGEASY_BASE_URL` | none | wg-easy base URL |
| `WGEASY_PASSWORD` | none | wg-easy administrative password |
| `DATABASE_PATH` | `/data/homelab-access.db` | Runtime database path |
| `DOWNLOAD_TOKEN_TTL` | `15m` | Default one-time link TTL |
