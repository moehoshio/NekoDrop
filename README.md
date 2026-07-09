# 🐱 NekoDrop

NekoDrop is a lightweight, web-based file & message sharing service. Open a room by entering a short **code**, and everyone who joins the same room can exchange text messages and files instantly — no account required.

## Download & Run

Download the latest pre-built binary for your platform from the [Releases](../../releases/latest) page.

| Platform | File |
| -------- | ---- |
| Linux (x86-64) | `nekodrop-linux-amd64.tar.gz` |
| Linux (ARM64) | `nekodrop-linux-arm64.tar.gz` |
| macOS (x86-64) | `nekodrop-darwin-amd64.tar.gz` |
| macOS (Apple Silicon) | `nekodrop-darwin-arm64.tar.gz` |
| Windows (x86-64) | `nekodrop-windows-amd64.zip` |
| Windows (ARM64) | `nekodrop-windows-arm64.zip` |

Extract the archive and run the binary:

```bash
# Linux / macOS
./nekodrop

# Windows
nekodrop.exe
```

Then open <http://localhost:8080> in your browser, enter a room code (or generate a random one), and start sharing. Send the same code or the room URL to others so they can join.

## Features

- **Channels by path or code** — visit `/r/<code>` or type the same code on the landing page to join the same channel. Codes are normalized (`Team Cats` → `team-cats`). Every channel also gets a permanent, unique ID shown after its name (e.g. `Team Cats #8d1272`).
- **Lightweight identities** — every visitor is issued a stable UID rendered after their nickname (`alice#123`). No password or sign-up. Messages are keyed by UID, never by name, so renaming yourself relabels all of your past messages instead of looking like a new person.
- **Per-channel nicknames** — set a display nickname that applies only to one channel from the box left of the composer; leave it empty to fall back to your global name. Changing it updates your existing messages in that channel live.
- **Public & private channels** — *public* channels can be read by anyone; *private* channels are members-only.
- **Owner & admin controls** — the creator sets the name, description, visibility, join policy, and speak permission. They can promote admins, ban/mute/kick members, approve join requests, and dissolve the channel.
- **Public channel directory** — opt a channel into the home-page list with *Show in public list*.
- **Announcements** — owners and admins pin notices to the top of a channel.
- **@-mentions** — type `@` to pick a participant, or click anyone's name in chat to mention them. Mentions target a UID rather than a name, so they keep pointing at the same person after a rename. You get a 🔔 notification with click-to-locate when you are mentioned.
- **Replies** — reply to an earlier message; the reply shows a quote of the original that you can click to jump straight to it.
- **Edit & delete messages** — edit or delete your own messages (edits are marked *(edited)* for everyone). Owners and admins can delete any member's message, and banning a member offers to delete all of their messages in one go.
- **Joined channels** — the landing page lists the channels you have joined (alongside the ones you own), so you can return to them without remembering the code.
- **Desktop notifications & sound** — opt in, per channel, to a browser notification and/or a sound for new messages. Turning message notifications off still alerts you for @-mentions and announcements. Browser pop-ups appear only when the tab is in the background.
- **Inline media & link previews** — images, video and audio appear inline and open in a lightbox.
- **Live presence** — see how many people are currently online in a channel.
- **Text & file sharing** — send messages or drop a file; every member sees it instantly.
- **Real-time updates** — powered by Server-Sent Events, no page refresh needed. The public directory and your own channels refresh themselves live — no manual refresh button.
- **Multi-language UI** — English, 繁體中文, 简体中文, 日本語, Español and Русский built in. Auto-detected from the browser on first visit, switchable from a 🌐 picker, and remembered in a cookie.
- **Light & dark themes** — auto, dark or light, switchable from a 🌓 picker. *Auto* follows the operating system's colour scheme and updates live; the choice is remembered in a cookie.
- **Paste & drop attachments** — paste an image from the clipboard or drop files onto the chat; click a staged file to preview its contents or info, and remove it, before sending.
- **Files with a caption** — type a message while a file is staged and the two are sent as a single message describing the file; or send the file on its own.
- **Account migration** — off by default, per user: opt in to get a persistent migration code, then enter it in another browser to inherit the same account there (see [Account migration](#account-migration)).
- **Admin panel** — an operator backend at `/admin`, disabled by default: disable channels, ban users site-wide, grant per-channel/per-user upload-size exemptions, and edit the runtime configuration online (see [Admin panel](#admin-panel)).
- **Pluggable storage** — keep everything in memory (default) or persist to SQLite or MySQL so channels and history survive restarts.
- **Configurable** — a YAML or JSON config file plus environment variables and flags control the listen address, limits and storage. On first run a commented `config.yaml` is generated for you.

## Configuration

Settings are resolved from a **config file** (YAML or JSON), then **environment variables**, then **command-line flags** (each layer overrides the previous). Every setting has a default, so NekoDrop runs with no configuration at all.

The config file may be **YAML** (`.yaml`/`.yml`) or **JSON** (`.json`) — the format is chosen by the file extension, and the keys are identical either way. When you do not name a file, NekoDrop looks for `config.yaml`, `config.yml`, then `config.json`; if none exists it writes a commented [`config.yaml`](config.example.yaml) filled with the defaults so you have something to edit.

| Flag | Env var | Config key | Default | Description |
| ---- | ------- | ---------- | ------- | ----------- |
| `-config` | `NEKODROP_CONFIG` | — | auto (`config.yaml`) | Path to a YAML or JSON config file (optional) |
| `-host` | `NEKODROP_HOST` | `host` | `` (all) | IP address to listen on |
| `-port` | `NEKODROP_PORT` | `port` | `8080` | TCP port to listen on |
| `-addr` | `NEKODROP_ADDR` | — | `:8080` | `host:port` shorthand |
| `-max-upload` | `NEKODROP_MAX_UPLOAD` | `maxUploadBytes` | `33554432` (32 MiB) | Max single-file upload size (bytes) |
| `-max-channels-per-user` | `NEKODROP_MAX_CHANNELS` | `maxChannelsPerUser` | `5` | Channels a single user may own at once (`0` = unlimited) |
| `-storage` | `NEKODROP_STORAGE_BACKEND` | `storage.backend` | `memory` | Storage backend: `memory`, `sqlite`, or `mysql` |
| `-storage-dsn` | `NEKODROP_STORAGE_DSN` | `storage.dsn` | — | Backend DSN (see below) |
| — | `NEKODROP_ADMIN_ENABLED` | `admin.enabled` | `false` | Turn on the admin panel at `/admin` |
| — | `NEKODROP_ADMIN_TOKEN` | `admin.token` | — | Admin access token (required for the panel to activate) |

Example config file ([`config.example.yaml`](config.example.yaml) — see the file for a fully commented version):

```yaml
host: "0.0.0.0"
port: 8080
maxUploadBytes: 33554432
maxChannelsPerUser: 5
storage:
  backend: sqlite
  dsn: nekodrop.db
admin:
  enabled: false
  token: ""
```

The same settings in JSON ([`config.example.json`](config.example.json)):

```json
{
  "host": "0.0.0.0",
  "port": 8080,
  "storage": { "backend": "sqlite", "dsn": "nekodrop.db" }
}
```

```bash
./nekodrop                 # first run writes a commented config.yaml, then edit it
./nekodrop -config config.yaml
# or purely from flags:
./nekodrop -host 0.0.0.0 -port 9000 -storage sqlite -storage-dsn nekodrop.db
```

### Storage backends

| Backend | DSN | Notes |
| ------- | --- | ----- |
| `memory` | — | Default. Nothing is persisted; fastest and simplest. |
| `sqlite` | file path, e.g. `nekodrop.db` | Embedded, single-file, pure-Go (no cgo). |
| `mysql` | `user:pass@tcp(host:3306)/nekodrop` | Standard Go MySQL DSN; works with MySQL/MariaDB. |

When a persistent backend is selected, channels, membership, message history, uploaded files and announcements survive restarts. A dissolved channel's path becomes available again, but its retired channel ID is never reissued. Uploaded files are always served with `Content-Disposition: attachment` and `X-Content-Type-Options: nosniff` to prevent execution in the browser.

### Resource limits

Because the default backend keeps everything in memory, NekoDrop enforces finite, configurable bounds so that untrusted traffic cannot exhaust server memory. Each limit falls back to a safe default when unset (a non-positive value never means "unlimited").

| Config key (`limits.*`) | Env var | Default | Bounds |
| ----------------------- | ------- | ------- | ------ |
| `maxMessagesPerChannel` | `NEKODROP_MAX_MESSAGES` | `1000` | Recent messages kept per channel; older ones are evicted with their files |
| `maxFileBytesPerChannel` | `NEKODROP_MAX_FILE_BYTES` | `134217728` (128 MiB) | In-memory uploaded-file bytes per channel |
| `maxBytesPerChannel` | `NEKODROP_MAX_CHANNEL_BYTES` | `167772160` (160 MiB) | Total resident memory per channel (message text + file payloads); the oldest messages are evicted when exceeded |
| `maxSubscribersPerChannel` | `NEKODROP_MAX_SUBSCRIBERS` | `512` | Concurrent live connections per channel |
| `maxChannels` | `NEKODROP_MAX_LIVE_CHANNELS` | `10000` | Live channels; idle ownerless channels are reclaimed to make room |
| `maxUsers` | `NEKODROP_MAX_USERS` | `100000` | Identities retained in memory; oldest unnamed ones are evicted |

With a persistent backend the full history still lives in the database; these limits only cap what is held resident in memory.

### Admin panel

A server-operator backend, **disabled by default**. Enable it in the config file (both keys are required — a bare `enabled: true` without a token never exposes the panel):

```yaml
admin:
  enabled: true
  token: "choose-a-long-random-secret"
```

Then open `/admin` and unlock it with the token (sent as an `X-Admin-Token` header on every request). While disabled, `/admin` and the whole admin API answer 404. The panel manages site-wide state layered on top of the per-channel owner/admin moderation model:

- **Dashboard** — a self-refreshing statistics overview: live channels, known/named/migration-enabled users, people online and active connections, resident messages/files/announcements and their memory footprint, Go process memory (heap and OS), goroutines, uptime, and — on persistent backends — database size and stored totals.
- **Availability** — disable (ban) whole channels or ban users site-wide. A disabled channel disappears from every listing and rejects reading, posting, joining and downloads; a banned user can still read but cannot post, upload, join, or create channels.
- **Exemptions** — give an individual channel or user their own upload size limit, independent of the global one. A per-user limit wins over a per-channel one, which wins over the global configuration, so an exemption can either raise or tighten the effective limit.
- **Runtime configuration** — edit the configuration knobs online (upload size, per-user channel quota, and every `limits.*` value). Changes apply immediately to the live server and are stored as overrides on top of the config file; clearing a field reverts to the file's value.

Admin state (bans, exemptions, config overrides) persists through the storage backend, so it survives restarts with `sqlite`/`mysql`. Enforcement stays active even if the panel is later switched off — only the management UI/API is gated.

### Account migration

NekoDrop identities normally live in a single browser's cookie. Account migration lets a user carry one account across browsers or devices. It is **off by default for every user** and each user opts in individually:

1. On the landing page, open *Account migration* and enable it. The server mints a persistent **migration code** bound to your account.
2. In another browser, open the same section, paste the code, and confirm. That browser's identity cookie is re-bound to your account — same UID, name, channels and history.
3. The code stays valid (so more browsers can follow) until you regenerate or disable migration, either of which invalidates the old code immediately.

Treat the code like a password: anyone who has it can use the account. Codes are 256-bit random values, never shown to other users, and users with migration enabled are kept in memory in preference to plain anonymous visitors when the identity cap forces eviction.

---

## Building from Source

Requires Go 1.24+.

```bash
# Run directly without building
go run .

# Build a single self-contained binary
go build -o nekodrop .
./nekodrop
```

## Development

```bash
go vet ./...
go test ./...
```

## Project Layout

```
.
├── main.go                  # Server entry point, flags, graceful shutdown
└── internal/
    ├── config/              # YAML/JSON file + env + flag configuration
    ├── storage/             # Persistence: memory / sqlite / mysql backends
    ├── user/                # Identities and UID assignment
    ├── room/                # Channels, membership, messages, files, events
    └── server/              # HTTP handlers + embedded web UI
        └── web/             # HTML, CSS, JS (embedded via go:embed)
```

## HTTP API

The web UI is the primary interface, but the underlying HTTP API is straightforward. A visitor is identified by the `nekodrop_token` cookie, issued automatically on first contact.

| Method & path | Description |
| ------------- | ----------- |
| `GET /` | Landing page (join, create, public directory) |
| `GET /r/{room}` | Channel page |
| `GET /api/me` | Current identity (`{uid,name,named,migration}`); sets the cookie |
| `POST /api/me` | Update display name (`{"name"}`); UID is unchanged |
| `GET /api/me/migration` | Account-migration status for this identity (`{enabled,code}`) |
| `POST /api/me/migration` | Enable migration / regenerate the code (invalidates the old one) |
| `DELETE /api/me/migration` | Disable migration and invalidate the code |
| `POST /api/migrate` | Adopt the account behind `{"code"}` on this browser (re-binds the cookie) |
| `GET /api/channels` | Public directory (`channels`), your own channels (`mine`) and channels you have joined (`joined`) |
| `POST /api/channels` | Create an owned channel (name, visibility, settings…) |
| `GET /api/channels/{room}` | Channel metadata + your role (+ pending & member roster if admin) |
| `PATCH /api/channels/{room}` | Update settings (owner/admin) |
| `DELETE /api/channels/{room}` | Dissolve the channel (owner) |
| `POST /api/channels/{room}/join` | Join (or request approval) |
| `POST /api/channels/{room}/leave` | Leave the channel |
| `POST /api/channels/{room}/moderate` | Moderation action (`{action,uid}`): promote, demote, ban, unban, mute, unmute, kick, approve, reject; `ban` also accepts `purgeMessages: true` to delete everything the member sent |
| `POST /api/channels/{room}/announcements` | Post an announcement (owner/admin) |
| `GET /api/stream/{room}` | Server-Sent Events stream (announcements, history, presence + live events) |
| `POST /api/messages/{room}` | Send a text message (`{sender,text,preview}`, optional `nick` per-channel name and `replyTo` message ID) |
| `PATCH /api/messages/{room}/{id}` | Edit your own message (`{"text"}`); the message is flagged as edited |
| `DELETE /api/messages/{room}/{id}` | Delete a message (your own, or any member's if you are an admin) |
| `POST /api/files/{room}` | Upload a file (multipart `file`, `sender`, optional `nick`, `text` caption, `preview` and `replyTo`) |
| `GET /api/files/{room}/{id}` | Download a shared file (`?inline=1` renders whitelisted media inline) |

When the admin panel is enabled, these additional endpoints exist (all require the `X-Admin-Token` header; everything answers 404 while the panel is disabled):

| Method & path | Description |
| ------------- | ----------- |
| `GET /admin` | Admin panel page |
| `GET /api/admin/overview` | Dashboard statistics: channels, users, online/connections, messages, files, resident memory, process memory, uptime, database totals |
| `GET /api/admin/channels` | All live channels (`?q=` filters), with ban state and upload exemption |
| `PATCH /api/admin/channels/{id}` | Set `{"banned"}` and/or `{"maxUploadBytes"}` (`null` clears) for a channel |
| `GET /api/admin/users` | Known users (`?q=` filters), with ban state and upload exemption |
| `PATCH /api/admin/users/{uid}` | Set `{"banned"}` and/or `{"maxUploadBytes"}` (`null` clears) for a user |
| `GET /api/admin/config` | Runtime configuration: base values, overrides, effective values |
| `PATCH /api/admin/config` | Set runtime overrides (any config field; `null` reverts to the file value) |

## Releases

Pushing a `v*` tag triggers the [release workflow](.github/workflows/release.yml), which cross-compiles self-contained binaries for Linux, macOS, and Windows on `amd64` and `arm64`, packages them (`.tar.gz` for Linux/macOS, `.zip` for Windows) along with a `checksums.txt`, and publishes them to a GitHub release.

```bash
git tag v1.0.0
git push origin v1.0.0
```

## License

MIT — see [LICENSE](LICENSE).
