# STYLiTE Orbit Mattermost to Telegrambot

One-way firehose from Mattermost to a single Telegram chat.

Logs into Mattermost as a regular user (or personal access token) and
forwards **every** event that account can see — public channels,
private channels, direct messages, group DMs — to one Telegram chat.
Read-only: there is no reply path.

## Build

```bash
go build -o mm2tg ./cmd/mm2tg
```

## Run (local)

```bash
cp .env.example .env
# edit .env
set -a; . ./.env; set +a
./mm2tg
```

## Run (Docker)

```bash
docker build -t mm2tg .
docker run --rm --env-file .env mm2tg
```

Or via compose:

```bash
docker compose up -d
```

## Environment

| Variable      | Required | Notes                                              |
|---------------|----------|----------------------------------------------------|
| `MM_SERVER`   | yes      | host only, e.g. `chat.example.com`                 |
| `MM_TEAM`     | yes      | team URL slug                                      |
| `MM_LOGIN`    | yes      | username or email                                  |
| `MM_PASS`     | yes      | password or personal access token                  |
| `MM_MFA`      | no       | MFA token if enabled                               |
| `MM_LOGLEVEL` | no       | `debug` / `info` / `warn` (default `info`)         |
| `TG_TOKEN`    | yes      | Telegram bot token from @BotFather                 |
| `TG_CHAT_ID`  | yes      | target chat id; negative for groups/channels       |

## Caveats

- The Mattermost account must be a member of the channels/DMs you want
  to see. Use a dedicated user account added to everything relevant.
- A true bot account cannot receive DMs from humans unless DMs-to-bots
  are enabled in System Console → Bot Accounts.
- Getting `TG_CHAT_ID`:
  - **Private DM (bot sends only to you):** open `@userinfobot` in
    Telegram and press Start — it replies with your numeric user id
    (a positive number, e.g. `165089403`). Use that as `TG_CHAT_ID`.
    You must also send `/start` (or any message) to your own bot once,
    otherwise it cannot initiate the DM.
  - **Group chat (bot posts into a shared group):** add your bot to
    the group, then also add `@RawDataBot` (or `@myidbot`). It posts
    the group's chat id, which starts with `-` (e.g. `-1001234567890`
    for supergroups). Remove the helper bot afterwards. Your own bot
    must stay in the group to post.
  - **Channel (bot broadcasts into a Telegram channel):** add your
    bot to the channel as an **admin** with post permissions. Forward
    any message from the channel to `@RawDataBot` to get its id
    (also starts with `-100…`).
- Messages are split at 3900 chars (Telegram's limit is 4096).
- Notifications are muted (`disable_notification=true`).
