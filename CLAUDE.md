# matterbot2telegram — project context for Claude

## What this is

A one-way firehose: logs into Mattermost as a regular user and forwards
**every** event that account can see (public channels, private channels,
direct messages, group DMs) to a single Telegram chat. No reply path,
no gateway, no routing config. Read-only observer.

Written because matterbridge does not bridge DMs and group-DMs — it
only bridges explicitly configured channels, which can't include
per-user direct channels.

## Origin

Extracted from https://github.com/styliteag/matterbridge branch
`private-messages`. That branch contains WebSocket stability fixes in
`vendor/github.com/matterbridge/matterclient/`:

- atomic reconnect flag (prevents double-reconnect races)
- handler supervisor (restarts `handleMatterClient` if it panics)
- stale-pong watchdog (detects dead but not-yet-closed WS)
- drop-oldest buffer behavior (instead of blocking when MessageChan full)
- ALIVE counters and MM-SEND-GW logging

**Important:** this repo currently uses the *released* matterclient
(`v0.0.0-20240817214420-3d4c3aef3dc1`), which does **NOT** have those
fixes. Options to resolve:

1. Fork `github.com/matterbridge/matterclient` to
   `github.com/styliteag/matterclient`, commit the fixes from the
   `private-messages` vendor tree, and add a `replace` directive in
   `go.mod`.
2. Vendor matterclient into this repo and apply the patches locally
   under `vendor/`.

Option 1 is cleaner. Neither has been done yet.

## Architecture (all in `cmd/mm2tg/main.go`)

1. Read config from env (`MM_*`, `TG_TOKEN`, `TG_CHAT_ID`).
2. `matterclient.New(...)` then `Login()` — opens the websocket.
3. Range over `mc.MessageChan` — matterclient delivers every event the
   logged-in user can see, including DMs/GDMs.
4. For each message: look up channel type via
   `mc.Client.GetChannel(ctx, id, "")` to label as PUB/PRIV/DM/GDM.
5. Format as `[TYPE team/channel] <user> text`, split at 3900 chars,
   send to Telegram with `DisableNotification=true`.
6. If `MessageChan` ever closes, `log.Fatal` so the container restarts.

## Key dependencies

- `github.com/matterbridge/matterclient` — Mattermost WS client
- `github.com/mattermost/mattermost/server/public` v0.1.6 — model types,
  note `GetChannel` signature is `(ctx, id, etag)`
- `github.com/go-telegram-bot-api/telegram-bot-api/v5` v5.5.1

## Deployment

GitHub Actions builds a multi-arch (amd64, arm64) image on every push
to `master`/`main` and on tag `v*`, publishing to
`ghcr.io/styliteag/matterbot2telegram`. Run via `docker-compose.yml`
with a `.env` file (see `.env.example`).

## Conventions

- Commits: `<type>: <description>` (feat, fix, refactor, docs, test, chore, perf, ci)
- No co-authored-by attribution
- Go: `gofmt`, `go vet`. Accept interfaces, return structs. Use
  `context.Context` for timeouts. Wrap errors with `%w`.
- Functions under 50 lines. No console noise — use `log` package.

## Mattermost account requirements

- Must be a member of every channel/DM you want forwarded.
- Use a dedicated human-style user account (not a bot account) because
  bot accounts can't receive DMs from humans unless System Console
  → Bot Accounts has "Enable Bot Account Creation" + DM permissions
  configured. A regular user account is simpler.
- A personal access token is preferred over a password in `MM_PASS`.

## Things likely to come up next

- Port matterclient WS fixes (see Origin section) — highest value.
- Add a small allow/deny filter (e.g. skip system messages, skip
  channels matching a pattern).
- Markdown/HTML formatting for Telegram (currently plain text).
- Attachment handling (currently ignored; only `m.Text` is forwarded).
- Prometheus metrics endpoint.

## Do NOT

- Add a reply path back to Mattermost. This is intentionally read-only.
- Pull in matterbridge's gateway/config/bridge machinery. The whole
  point of this repo is avoiding that complexity.
- Add retry/reconnect logic around `mc.MessageChan` in main — that's
  matterclient's job. If it's broken, fix matterclient.
