# 🐱 NekoDrop

NekoDrop is a lightweight, web-based file & message sharing service written in
Go. Open a room by URL path or share a short **code**, and everyone who joins
the same room can exchange text messages and files in a chat-room style
interface — quick, mutual, no account required.

## Features

- **Rooms by path or code** — visit `/r/<code>` or type the same code on the
  landing page to join the same room. Codes and paths are normalized to the
  same room key (`Team Cats` → `team-cats`).
- **Chat-room style UI** — a clean, responsive interface with a live message
  feed and a composer.
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

| Flag           | Env var               | Default | Description                       |
| -------------- | --------------------- | ------- | --------------------------------- |
| `-addr`        | `NEKODROP_ADDR`       | `:8080` | HTTP listen address               |
| `-max-upload`  | `NEKODROP_MAX_UPLOAD` | `33554432` (32 MiB) | Max single-file upload size (bytes) |

```bash
./nekodrop -addr :9000 -max-upload 67108864
```

## HTTP API

The web UI is the primary interface, but the underlying API is simple:

| Method & path                    | Description                                  |
| -------------------------------- | -------------------------------------------- |
| `GET /`                          | Landing page                                 |
| `GET /r/{room}`                  | Chat-room page                               |
| `GET /api/stream/{room}`         | Server-Sent Events stream (history + live)   |
| `POST /api/messages/{room}`      | Send a text message (`{"sender","text"}`)    |
| `POST /api/files/{room}`         | Upload a file (multipart `file`, `sender`)   |
| `GET /api/files/{room}/{id}`     | Download a shared file                       |

## Project layout

```
.
├── main.go                  # Server entry point, flags, graceful shutdown
└── internal/
    ├── room/                # In-memory rooms, messages, files, pub/sub hub
    └── server/              # HTTP handlers + embedded web UI
        └── web/             # HTML, CSS, JS (embedded via go:embed)
```

## Development

```bash
go vet ./...
go test ./...
```

## Notes

Rooms and files are stored **in memory** and are not persisted across restarts,
which keeps NekoDrop simple and fast for quick, ephemeral transfers. Uploaded
files are always served with `Content-Disposition: attachment` and
`X-Content-Type-Options: nosniff` to prevent them being executed in the
browser.

## License

MIT — see [LICENSE](LICENSE).
