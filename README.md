# jcode-web

Monitor and control jcode sessions from a desktop or mobile browser.

## Interface

- Sessions grouped by workspace directory, with running groups first. Compact three-line rows show the task title, AI progress summary, and current status or activity.
- Clock badges show pending scheduled-task counts. Waiting sessions remain visible even when quiet sessions are hidden.
- Live transcripts, tool calls, token counts, and status updates.
- Drag sessions into a Dockview tiled workspace with docking previews. Drag tabs to split at an edge or group in the center, and resize with shared dividers.
- Click a session to focus its existing tab or add a tab in the focused tile without replacing other sessions. Shift-click replaces the focused session in its existing pane. Ctrl or Cmd click opens another tile. Tiles fill the available screen, with optional maximize and restore controls. Split sizes and tab groups are saved in the browser.
- Mobile tabs keep one session visible at a time.
- Send messages, steer a running turn, cancel, or use **btw** to fork a side question without stopping the parent.
- Persistent activity feed and reconnect replay from SQLite.
- Optional AI task titles, session summaries, a milestone feed tile, and a time-window activity overview.
- Summaries start collapsed. Use the pane-header Summary control to expand them or hover to preview. Refresh lives beside it. Sidebar hover also previews each session summary.
- Right-click a sidebar session to open, split, replace, search, copy its ID, or close and hide it locally. Hiding does not stop the agent. Reveal quiet sessions and click it to restore it.
- Switch the sidebar to Content for indexed history search, or use the pane-header search control to search only that session. Results show up to three matching excerpts per session.
- Workspace **+** creates a session in that directory. Load older transcript messages or recall sent prompts with Up/Down and the composer history buttons.
- Scheduled tasks per session, with due times and live countdowns. Empty task sections stay hidden.
- Content links open in a new tab.
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

The session's **Scheduled tasks** section reads Jcode's pending scheduler through the debug socket (`ambient:queue`) every 10 seconds. It includes tasks resuming that session and tasks spawning from it. The sidebar receives counts through the session stream from one shared queue read per discovery poll. This view is read-only and does not create or cancel tasks.

Browser reconnects replay persisted events. Events missed while the backend itself is disconnected from jcode are not currently backfilled. Transcript reconnects recover from main-socket history instead.

## Configuration

Copy `contrib/config.example.yaml` to `~/.config/jcode-web/config.yaml`, or use `-c /path/to/config.yaml`. Defaults work without a config file. Configuration precedence is defaults, YAML, environment, CLI overrides. Existing TOML configurations must be converted to YAML; an old default config is reported rather than silently ignored.

Environment variables use the `JCODE_WEB_` prefix, with `__` separating nested fields. For example:

```sh
JCODE_WEB_LISTEN=127.0.0.1:7650 \
JCODE_WEB_SUMMARY__ENABLED=true \
JCODE_WEB_SUMMARY__BASE_URL=http://madobe:1234/v1 \
JCODE_WEB_SUMMARY__MODEL=openai/gpt-oss-20b \
./jcode-web serve
```

Summaries use an OpenAI-compatible `/chat/completions` endpoint. The example targets LM Studio on madobe with `openai/gpt-oss-20b` and low reasoning effort. They are optional and disabled by default. Completion, attention, plan updates, and write activity queue summaries after a short debounce. Progress checkpoints are limited to one per session per 45 seconds. The pane refresh button and activity overview request them on demand. Requests are serialized to avoid overloading the local model. The **Progress feed** tile replays persisted AI updates and receives new ones live.

Summaries send transcript excerpts and activity to the configured provider. They are generated descriptions, not authoritative status or command results. API keys stay on the backend.

SQLite stores activity, summaries, push subscriptions, and VAPID keys under the configured data directory. Migrations run automatically. Back up the database, including its WAL if the process is running. Transcript history remains owned by jcode. Sent-prompt recall also keeps up to 100 prompts per session in browser local storage. Docking layouts and workspace collapse preferences stay in that browser.

### Saved session content search

Search is enabled by default. `search.sessions_dir` defaults to `$JCODE_HOME/sessions`, or `~/.jcode/sessions` when `JCODE_HOME` is unset. `search.poll_interval` defaults to `15s` (values below `1s` use the default). Set `search.enabled: false` or `JCODE_WEB_SEARCH__ENABLED=false` to disable it. The equivalent directory override is `JCODE_WEB_SEARCH__SESSIONS_DIR`.

The backend reads jcode's JSON snapshots and append-only `.journal.jsonl` files directly. It does not subscribe, resume sessions, write source files, or ask agents for history. Backfill starts in the background, newest snapshots first. Unchanged snapshots are skipped, journal appends are consumed from a persisted byte offset, and snapshot replacement or journal rotation rebuilds only that session. Message IDs suppress duplicate snapshot/journal entries. Inherited fork histories remain searchable in each fork, grouped under that fork's session ID.

The disposable index is `session-search.db` in `data_dir`, separate from `jcode-web.db`, with its own Goose migrations. SQLite FTS5 uses the trigram tokenizer with compact, position-free postings. Text is split into overlapping chunks, and queries intersect literal trigrams before checking the exact lowercased substring in candidates. Queries do not open or scan transcript files. Quotes, SQL/FTS operators, punctuation, and wildcard characters have no special query meaning. Matching uses Unicode lowercasing without accent removal, stemming, or fuzzy matching. Queries must contain 3 to 256 Unicode characters and no NUL.

One worker indexes with at most 128 database operations per transaction and a short yield between batches. Source reads are capped at 32 MiB/s, one source record at 64 MiB, and one source file at 1 GiB. Separate WAL reader connections and 8 MiB connection page caches keep indexing off the activity store's single connection. There are at most two concurrent database readers and one archived-history reader. Search requests have a five-second deadline. Index size depends on the saved text, including duplicated fork history, and can exceed source JSON size.

`SessionService.SearchSessions` accepts `query`, `limit` (default 20 sessions, maximum 100), and optional `session_id` to search within one session before applying limits. It returns one result per session, at most three plain-text excerpts with saved-message positions, session metadata, index status, and `truncated` when more matching sessions exist. Result order is index order, not relevance or recency. `GetSearchIndexStatus` returns `{ status }` with enabled/available/indexing flags, total/indexed/failed session counts, the last completed scan timestamp, and an error string. Results are partial during backfill. Failed updates preserve the last published version; indexed and failed counts can therefore overlap. Startup index failures do not stop the other services.

Ordinary message text, reasoning, tool names, tool inputs, and tool results are indexed. Binary images, encrypted provider payloads, assembled system prompts outside the conversation, replay-only UI events, backups, and orphan journals without snapshots are not. Text is visible only after jcode saves it and the worker reaches it. A corrupt complete journal record or oversized source is reported, not silently skipped. Incomplete trailing journal lines wait for the next append. A file changed during a checkpoint is retried on the next scan. Unchanged failed sources use a one-minute retry delay, while changed files are retried immediately on the next scan. A stopped process can leave unpublished work, which is cleaned up on restart.

Archived results use the existing `Attach` RPC to read saved history without starting an agent. `History.read_only` is true and the stream then ends with `Detached`. This view includes up to the trailing 1,000 rendered rows and 8 MiB, and reports the full row count in `History.total`. Individual rows over 8 MiB are replaced with an explicit display-limit notice. Search still covers earlier indexed text, but archived history pagination and jumping to a search position are not implemented. Live sessions retain their existing attachment behavior. Disabling search leaves normal upstream attachments unchanged.

The index contains private conversation text. Keep the data directory private and treat its backups like transcripts. It can be rebuilt by removing only the search database and its WAL/SHM files while jcode-web is stopped. Never remove the activity database to rebuild search.

The progress feed shows live relative ages, full timestamps on hover, and dates for updates older than 24 hours. Session discovery includes headless swarm workers, but the sidebar lists only top-level sessions. Quiet top-level sessions stay hidden unless requested, open, scheduled, or hosting active workers. Parent panes list their sub-sessions with status and inspection links. Inspect and btw open a tab alongside the parent, rather than splitting the workspace. Middle-click closes a workspace tab. Tabs use Dockview's pointer dragging, and worker panes link back to their coordinator.

## Deployment

This application has **no login or authorization layer**. Anyone who can reach it can read session content and run commands through jcode. Keep it on loopback and expose it only through a trusted tailnet or authenticated reverse proxy. Cross-origin browser requests are rejected, but that is not authentication. Do not expose it to the public internet.

Use HTTPS through the tailnet proxy for PWA installation and Web Push on remote devices. On iOS, install the app to the home screen before enabling notifications. Notification delivery depends on browser permission and the browser push service. A notification may contain a generated session summary.

`contrib/systemd/jcode-web.service` is an example user unit. Install the binary at `~/.local/bin/jcode-web`, copy the unit into `~/.config/systemd/user/`, then run:

```sh
systemctl --user daemon-reload
systemctl --user enable --now jcode-web
```

## Implementation

Go with chi, Connect-Go v2, protobuf-generated TypeScript clients, SQLite with plain SQL and Goose, koanf, kong, and slog. The frontend uses Svelte, Vite, and Connect server streaming over HTTP. A separate local SQLite search cache isolates indexing from the activity store. No WebSocket transport is needed. Production assets are embedded into one binary. No embedded tailnet daemon is included.

The jcode socket protocol is currently tied to the server implementation tested with jcode `35fe5e4a9`. Unknown stream events are preserved as raw events. Changes to upstream socket messages may require adapter updates.
