# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.1.0] - 2026-04-15

## [0.0.3] - 2026-04-15

## [0.0.2] - 2026-04-15

## [0.1.0] - 2026-04-15

### Added
- One-way firehose from Mattermost to a single Telegram chat (public,
  private, DM, GDM).
- Direct `model.Client4` + `model.WebSocketClient4` integration with
  `GetStatuses` keepalive (every 20s) to keep the WS alive on servers
  that reap quiet clients.
- Personal access token support via `MM_PASS=token=<PAT>`.
- HTML-formatted Telegram messages with channel-type icons
  (`#`, `#🔒`, `💬`, `👥`, `❓`), bold sender, italic location, and
  safe chunking for long messages.
- `MM_LOGLEVEL=info` structured forward logs with metadata only
  (type, team, channel, sender, body length).
- Multi-arch (amd64, arm64) GitHub Actions build to GHCR and Docker Hub.
- Release automation via `release.sh` + `VERSION` + tag-triggered
  `release.yml` workflow.
