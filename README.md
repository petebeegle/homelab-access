# homelab-access

Discord-driven access broker for the homelab.

The foundation build exposes:

- `GET /healthz`
- `GET /readyz`
- `GET /metrics`
- `POST /discord/interactions`
- `GET /download/{token}` (non-consuming confirmation page)
- `POST /download/{token}` (single-use configuration download)

The Discord endpoint validates Discord Ed25519 signatures, answers PING with
PONG, acknowledges `/access request`, and lets configured admins approve or deny
requests with ephemeral responses. Approval creates or reuses an Authentik user.
Approval also creates or reuses a wg-easy peer and returns a single-use
WireGuard config download link. Approval is deferred immediately and the
ephemeral response is updated after provisioning completes.

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
| `AUTHENTIK_TOKEN` | none | Authentik API token for user provisioning |
| `WGEASY_BASE_URL` | none | wg-easy base URL |
| `WGEASY_USERNAME` | `admin` | wg-easy administrative username |
| `WGEASY_PASSWORD` | none | wg-easy administrative password for peer provisioning |
| `ACCESS_STORE_PATH` | `/data/homelab-access.json` | Runtime access request store path |
| `DATABASE_PATH` | none | Backward-compatible fallback for `ACCESS_STORE_PATH` |
| `DOWNLOAD_TOKEN_TTL` | `15m` | Default one-time link TTL |

## Container images

CI builds the container on every pull request without publishing it. Push events
publish `ghcr.io/petebeegle/homelab-access` with these tags:

| Event | Published tags |
| --- | --- |
| Commit merged to `main` | `sha-<full-40-character-commit-sha>`, `main` |
| Git tag matching `v*` | `sha-<full-40-character-commit-sha>`, the unchanged Git tag |

The `sha-*` tag is the immutable deployment identifier. `main` is only a
discovery alias and can move. Version tags retain their exact Git tag spelling.
Published images include `org.opencontainers.image.source`,
`org.opencontainers.image.revision`, and `org.opencontainers.image.created`
labels. The CI image job exposes the registry digest as its `digest` output and
records the digest and published tags in the workflow summary.

GitOps consumers should resolve the `sha-*` image to its registry digest and pin
the deployment to that digest. Updating the homelab manifests and automating
that promotion are part of the separate S02B release-hygiene slice.
