# 🐱 NekoDrop

NekoDrop is a lightweight, web-based file & message sharing service written in
Go. Open a room by URL path or share a short **code**, and everyone who joins
the same room can exchange text messages and files in a chat-room style
interface — quick, mutual, no account required.

## Features

- **Channels by path or code** — visit `/r/<code>` or type the same code on the
  landing page to join the same channel. Codes and paths are normalized to the
  same key (`Team Cats` → `team-cats`). Every channel also gets a permanent,
  unique ID shown after its name (e.g. `Team Cats #8d1272`).
- **Lightweight identities** — every visitor is issued a stable UID rendered
  after their nickname (`alice#123`), so people sharing a name stay
  distinguishable and mentions can target a specific person. No password or
  sign-up.
- **Public & private channels** — *public* channels can be read by anyone
  (joining is only needed to speak); *private* channels are members-only.
- **Owner & admin controls** — the creator sets the name, description,
  visibility, whether joining is allowed, whether joining needs approval, and
  whether members may speak. They can promote admins, ban/mute/kick members,
  approve join requests, and dissolve the channel. Dissolving frees the path for
  reuse while permanently retiring the channel ID.
- **Public channel directory** — opt a channel into the home-page list with
  *Show in public list*.
- **Announcements** — owners and admins pin notices to the top of a channel.
- **@-mentions** — type `@` to pick a participant; mentions are highlighted, and
  you get a 🔔 notification with click-to-locate when you are mentioned.
- **Inline media & link previews** — images, video and audio appear inline and
  open in a lightbox. Senders choose per-message whether to render link/media
  previews for everyone.
- **Live presence** — see how many people are currently online in a channel.
- **Text & file sharing** — send messages or drop a file; every member sees it
  instantly and can download shared files (mutual transfer).
- **Real-time updates** — powered by Server-Sent Events, no page refresh
  needed.
- **Zero external dependencies** — built entirely on the Go standard library;
  the web UI is embedded into the binary.

## Quick start

Requires Go 1.24+.

```bash
# Run directly
go run .

# or build a single self-contained binary
go build -o nekodrop .
./nekodrop
```

Then open <http://localhost:8080>, enter a room code (or create a random one),
and start sharing. Share the code or the room URL with others to let them join.

## Configuration

| Flag                      | Env var                 | Default | Description                       |
| ------------------------- | ----------------------- | ------- | --------------------------------- |
| `-addr`                   | `NEKODROP_ADDR`         | `:8080` | HTTP listen address               |
| `-max-upload`             | `NEKODROP_MAX_UPLOAD`   | `33554432` (32 MiB) | Max single-file upload size (bytes) |
| `-max-channels-per-user`  | `NEKODROP_MAX_CHANNELS` | `5`     | Channels a single user may own at once (`0` = unlimited) |

```bash
./nekodrop -addr :9000 -max-upload 67108864 -max-channels-per-user 10
```

## HTTP API

The web UI is the primary interface, but the underlying API is simple. A visitor
is identified by the `nekodrop_token` cookie, issued automatically on first
contact and bound to a stable UID.

| Method & path                              | Description                                            |
| ------------------------------------------ | ------------------------------------------------------ |
| `GET /`                                    | Landing page (join, create, public directory)          |
| `GET /r/{room}`                            | Channel page                                           |
| `GET /api/me`                              | Current identity (`{uid,name}`); sets the cookie       |
| `POST /api/me`                             | Update display name (`{"name"}`); UID is unchanged     |
| `GET /api/channels`                        | List channels opted into the public directory          |
| `POST /api/channels`                       | Create an owned channel (name, visibility, settings…)  |
| `GET /api/channels/{room}`                 | Channel metadata + your role (+ pending list if admin) |
| `PATCH /api/channels/{room}`               | Update settings (owner/admin)                          |
| `DELETE /api/channels/{room}`              | Dissolve the channel (owner)                           |
| `POST /api/channels/{room}/join`           | Join (or request approval)                             |
| `POST /api/channels/{room}/leave`          | Leave the channel                                      |
| `POST /api/channels/{room}/moderate`       | Moderation action (`{action,uid}`): promote, demote, ban, unban, mute, unmute, kick, approve, reject |
| `POST /api/channels/{room}/announcements`  | Post an announcement (owner/admin)                     |
| `GET /api/stream/{room}`                   | Server-Sent Events stream (announcements, history, presence + live events) |
| `POST /api/messages/{room}`                | Send a text message (`{sender,text,preview}`)          |
| `POST /api/files/{room}`                   | Upload a file (multipart `file`, `sender`)             |
| `GET /api/files/{room}/{id}`               | Download a shared file (`?inline=1` renders whitelisted media inline) |

## Project layout

```
.
├── main.go                  # Server entry point, flags, graceful shutdown
└── internal/
    ├── user/                # In-memory identities and UID assignment
    ├── room/                # In-memory channels, membership, messages, files, events
    └── server/              # HTTP handlers + embedded web UI
        └── web/             # HTML, CSS, JS (embedded via go:embed)
```

## Development

```bash
go vet ./...
go test ./...
```

## Releases

Pushing a `v*` tag (e.g. `v1.0.0`) triggers the
[release workflow](.github/workflows/release.yml), which cross-compiles
self-contained binaries for Linux, macOS, and Windows on `amd64` and `arm64`,
packages them (`.tar.gz` for Linux/macOS, `.zip` for Windows) along with a
`checksums.txt`, and publishes them to a GitHub release.

```bash
git tag v1.0.0
git push origin v1.0.0
```

## Notes

Channels, identities and files are stored **in memory** and are not persisted
across restarts, which keeps NekoDrop simple and fast for quick, ephemeral
transfers. A dissolved channel's path becomes available again, but its retired
channel ID is never reissued. Uploaded
files are always served with `Content-Disposition: attachment` and
`X-Content-Type-Options: nosniff` to prevent them being executed in the
browser.

## License

MIT — see [LICENSE](LICENSE).
