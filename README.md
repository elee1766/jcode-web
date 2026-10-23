# jcode-web

Monitor and control jcode sessions from a desktop or mobile browser.

## Interface

- Sessions grouped by workspace directory, with running groups first.
- Live transcripts, tool calls, token counts, and status updates.
- Click to switch sessions. Ctrl, Shift, or Cmd click opens another pane.
- Mobile tabs keep one session visible at a time.
- Send messages, steer a running turn, cancel, or use **btw** to fork a side question without stopping the parent.
- Persistent activity feed and reconnect replay from SQLite.
- Optional AI session summaries and a time-window activity overview.
- Installable PWA with optional Web Push notifications.

## Build and run

Requires Go 1.26+, Node.js, and pnpm 10. Generated Go and TypeScript clients are checked in.

```sh
make build
./jcode-web check
./jcode-web serve
```

The default address is `127.0.0.1:7650`. With Nix:

```sh
nix build
./result/bin/jcode-web serve
# Development tools
nix develop
```

Run `make gen` after changing protobuf definitions. It requires Buf. Run `make check` for protobuf lint, Go vet, and Svelte/TypeScript checks.

## jcode connection

Run beside the jcode server as the same user. Defaults are `$XDG_RUNTIME_DIR/jcode.sock` and `$XDG_RUNTIME_DIR/jcode-debug.sock`, falling back to `/run/user/<uid>/`.

The debug socket must allow control. Enable it in jcode's configuration or create its opt-in marker:

```sh
touch ~/.jcode/debug_control
```

The main socket provides session attachments and commands. It does not replace existing TUI attachments. Turns started by the web client continue when the browser or web server disconnects. The debug socket supplies discovery and the server-wide activity bus. Polling repairs session status after missed updates.

Browser reconnects replay persisted events. Events missed while the backend itself is disconnected from jcode are not currently backfilled. Transcript reconnects recover from main-socket history instead.

## Configuration

Copy `contrib/config.example.toml` to `~/.config/jcode-web/config.toml`, or use `-c /path/to/config.toml`. Defaults work without a config file. Configuration precedence is defaults, TOML, environment, CLI overrides.

Environment variables use the `JCODE_WEB_` prefix, with `__` separating nested fields. For example:

```sh
JCODE_WEB_LISTEN=127.0.0.1:7650 \
JCODE_WEB_SUMMARY__ENABLED=true \
JCODE_WEB_SUMMARY__BASE_URL=http://madobe:1234/v1 \
JCODE_WEB_SUMMARY__MODEL=openai/gpt-oss-20b \
./jcode-web serve
```

Summaries use an OpenAI-compatible `/chat/completions` endpoint. The example targets LM Studio on madobe with `openai/gpt-oss-20b` and low reasoning effort. They are optional and disabled by default. Completion transitions queue session summaries. The pane refresh button and activity overview request them on demand. Requests are serialized to avoid overloading the local model.

Summaries send transcript excerpts and activity to the configured provider. They are generated descriptions, not authoritative status or command results. API keys stay on the backend.

SQLite stores activity, summaries, push subscriptions, and VAPID keys under the configured data directory. Migrations run automatically. Back up the database, including its WAL if the process is running. Transcript history remains owned by jcode.

## Deployment

This application has **no login or authorization layer**. Anyone who can reach it can read session content and run commands through jcode. Keep it on loopback and expose it only through a trusted tailnet or authenticated reverse proxy. Cross-origin browser requests are rejected, but that is not authentication. Do not expose it to the public internet.

Use HTTPS through the tailnet proxy for PWA installation and Web Push on remote devices. On iOS, install the app to the home screen before enabling notifications. Notification delivery depends on browser permission and the browser push service. A notification may contain a generated session summary.

`contrib/systemd/jcode-web.service` is an example user unit. Install the binary at `~/.local/bin/jcode-web`, copy the unit into `~/.config/systemd/user/`, then run:

```sh
systemctl --user daemon-reload
systemctl --user enable --now jcode-web
```

## Implementation

Go with chi, Connect-Go v2, protobuf-generated TypeScript clients, SQLite with plain SQL and Goose, koanf, kong, and slog. The frontend uses Svelte, Vite, and Connect server streaming over HTTP. No WebSocket transport or second database is needed. Production assets are embedded into one binary. No embedded tailnet daemon is included.

The jcode socket protocol is currently tied to the server implementation tested with jcode `35fe5e4a9`. Unknown stream events are preserved as raw events. Changes to upstream socket messages may require adapter updates.
