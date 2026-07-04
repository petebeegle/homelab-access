# homelab-access

Discord-driven access broker for the homelab.

The foundation build exposes:

- `GET /healthz`
- `GET /readyz`
- `GET /metrics`
- `POST /discord/interactions`
- `GET /download/{token}` placeholder

The Discord endpoint validates Discord Ed25519 signatures, answers PING with
PONG, and acknowledges `/access request` with an ephemeral message. Future
builds will persist admin-approved requests, create Authentik invitations,
create wg-easy peers, and deliver single-use WireGuard configs.

## Configuration

| Variable | Default | Purpose |
| --- | --- | --- |
| `HTTP_ADDR` | `:8080` | Listen address |
| `PUBLIC_BASE_URL` | none | Public one-time download base URL |
| `DISCORD_APP_ID` | none | Discord application ID |
| `DISCORD_PUBLIC_KEY` | none | Discord interaction signature public key |
| `DISCORD_BOT_TOKEN` | none | Discord bot token for future outbound calls |
| `DISCORD_ADMIN_USER_IDS` | none | Comma-separated Discord user IDs allowed to approve or deny access requests |
| `DISCORD_ADMIN_ROLE_IDS` | none | Comma-separated Discord role IDs allowed to approve or deny access requests |
| `AUTHENTIK_BASE_URL` | none | Authentik base URL |
| `AUTHENTIK_TOKEN` | none | Authentik API token for future provisioning |
| `WGEASY_BASE_URL` | none | wg-easy base URL |
| `WGEASY_PASSWORD` | none | wg-easy administrative password for future provisioning |
| `ACCESS_STORE_PATH` | `/data/homelab-access.json` | Runtime access request store path |
| `DATABASE_PATH` | none | Backward-compatible fallback for `ACCESS_STORE_PATH` |
| `DOWNLOAD_TOKEN_TTL` | `15m` | Default one-time link TTL |
