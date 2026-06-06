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
- **Lightweight identities** — every visitor is issued a stable UID rendered after their nickname (`alice#123`). No password or sign-up.
- **Public & private channels** — *public* channels can be read by anyone; *private* channels are members-only.
- **Owner & admin controls** — the creator sets the name, description, visibility, join policy, and speak permission. They can promote admins, ban/mute/kick members, approve join requests, and dissolve the channel.
- **Public channel directory** — opt a channel into the home-page list with *Show in public list*.
- **Announcements** — owners and admins pin notices to the top of a channel.
- **@-mentions** — type `@` to pick a participant; you get a 🔔 notification with click-to-locate when you are mentioned.
- **Inline media & link previews** — images, video and audio appear inline and open in a lightbox.
- **Live presence** — see how many people are currently online in a channel.
- **Text & file sharing** — send messages or drop a file; every member sees it instantly.
- **Real-time updates** — powered by Server-Sent Events, no page refresh needed.
- **Zero external dependencies** — built on the Go standard library; the web UI is embedded into the binary.

## Configuration

NekoDrop can be configured with command-line flags or environment variables:

| Flag | Env var | Default | Description |
| ---- | ------- | ------- | ----------- |
| `-addr` | `NEKODROP_ADDR` | `:8080` | HTTP listen address |
| `-max-upload` | `NEKODROP_MAX_UPLOAD` | `33554432` (32 MiB) | Max single-file upload size (bytes) |
| `-max-channels-per-user` | `NEKODROP_MAX_CHANNELS` | `5` | Channels a single user may own at once (`0` = unlimited) |

Example:

```bash
./nekodrop -addr :9000 -max-upload 67108864 -max-channels-per-user 10
```

## Notes

Channels, identities and files are stored **in memory** and are not persisted across restarts, which keeps NekoDrop simple and fast for quick, ephemeral transfers. A dissolved channel's path becomes available again, but its retired channel ID is never reissued. Uploaded files are always served with `Content-Disposition: attachment` and `X-Content-Type-Options: nosniff` to prevent execution in the browser.

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
    ├── user/                # In-memory identities and UID assignment
    ├── room/                # In-memory channels, membership, messages, files, events
    └── server/              # HTTP handlers + embedded web UI
        └── web/             # HTML, CSS, JS (embedded via go:embed)
```

## HTTP API

The web UI is the primary interface, but the underlying HTTP API is straightforward. A visitor is identified by the `nekodrop_token` cookie, issued automatically on first contact.

| Method & path | Description |
| ------------- | ----------- |
| `GET /` | Landing page (join, create, public directory) |
| `GET /r/{room}` | Channel page |
| `GET /api/me` | Current identity (`{uid,name}`); sets the cookie |
| `POST /api/me` | Update display name (`{"name"}`); UID is unchanged |
| `GET /api/channels` | List channels opted into the public directory |
| `POST /api/channels` | Create an owned channel (name, visibility, settings…) |
| `GET /api/channels/{room}` | Channel metadata + your role (+ pending list if admin) |
| `PATCH /api/channels/{room}` | Update settings (owner/admin) |
| `DELETE /api/channels/{room}` | Dissolve the channel (owner) |
| `POST /api/channels/{room}/join` | Join (or request approval) |
| `POST /api/channels/{room}/leave` | Leave the channel |
| `POST /api/channels/{room}/moderate` | Moderation action (`{action,uid}`): promote, demote, ban, unban, mute, unmute, kick, approve, reject |
| `POST /api/channels/{room}/announcements` | Post an announcement (owner/admin) |
| `GET /api/stream/{room}` | Server-Sent Events stream (announcements, history, presence + live events) |
| `POST /api/messages/{room}` | Send a text message (`{sender,text,preview}`) |
| `POST /api/files/{room}` | Upload a file (multipart `file`, `sender`) |
| `GET /api/files/{room}/{id}` | Download a shared file (`?inline=1` renders whitelisted media inline) |

## Releases

Pushing a `v*` tag triggers the [release workflow](.github/workflows/release.yml), which cross-compiles self-contained binaries for Linux, macOS, and Windows on `amd64` and `arm64`, packages them (`.tar.gz` for Linux/macOS, `.zip` for Windows) along with a `checksums.txt`, and publishes them to a GitHub release.

```bash
git tag v1.0.0
git push origin v1.0.0
```

## License

MIT — see [LICENSE](LICENSE).
