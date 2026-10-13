# Ogre

Ogre is a go-links tool in two parts. The Ogre server is the Directory: a small server that turns a short name such as `go/roadmap` into the address it stands for. The Ogre extension for Chrome sends `go/…` to that server. The server runs on your own computer and keeps its links in one SQLite file.

**State: milestone L1, the walking skeleton.** Links are literal names only and are created with `curl`; there is no web UI yet.

## Prerequisites

- Go 1.26 or newer. This is all you need to build and run the Ogre server.
- Node.js, `npm install`, and `npx playwright install chromium`, only for `make check` (type-checking and browser tests).

## Run it

```sh
go run ./cmd/ogre
```

It listens on `http://localhost:4655` (loopback only) and creates its database on first run in your per-user data directory: `~/Library/Application Support/ogre/ogre.db` on macOS, `~/.local/share/ogre/ogre.db` on Linux. Pass `--db <file>` to keep it somewhere else, and `go run ./cmd/ogre help` for the other flags. Stop it with Ctrl-C.

## Load the extension

Open `chrome://extensions`, switch on "Developer mode", choose "Load unpacked", and select the `extension/` directory. It appears as "Ogre". There is no build step. [extension/README.md](extension/README.md) explains each permission it asks for.

## Create a link

```sh
curl -i -X POST http://localhost:4655/-/api/v1/links \
  -H 'Content-Type: application/json' \
  -d '{"description":"Product roadmap","routes":[{"pattern":"roadmap","destination":"https://example.com/roadmap"}]}'
```

Then open `go/roadmap` in Chrome. In a fresh Chrome profile, type `go/` and press Enter once first (or open `http://go/roadmap`): until Chrome has seen `go/` typed, it searches for `go/roadmap` instead of opening it. Without the extension:

```sh
curl -i http://localhost:4655/roadmap        # 302 to the destination
curl -s http://localhost:4655/-/api/v1/links # every link
```

Names ignore case and nothing else: `go/Roadmap` is `go/roadmap`, while `on-call`, `on_call` and `oncall` are three different links. The `Content-Type: application/json` header is required on every write; it is one of the guards that keep web pages from changing your links.

A draft that can't be saved answers `422`. Its `error.details.routes` has one entry per route you sent, in the same order, each with that route's `problems`; `error.details.problems` holds the ones that belong to no single route. A problem's `start` and `end` mark the spot in the pattern or destination, counted as JavaScript counts string positions (UTF-16 code units).

## Without the extension

Go links also work at the Directory's own address, from any browser or script: `http://localhost:4655/roadmap`.

To make `http://go/roadmap` work without the extension, point the name `go` at this computer (a line `127.0.0.1 go` in `/etc/hosts`) and tell the Ogre server to answer to that name. It refuses names it wasn't told about, which is what keeps a hostile web page from reaching it under a made-up name:

```sh
go run ./cmd/ogre --allow-host go        # then open http://go:4655/roadmap
```

`http://go/roadmap` with no port means port 80. Either forward port 80 to 4655 and start the server with `--allow-host go:80`, or listen there directly with `--addr localhost:80 --allow-host go`, which needs the rights to open a low port.

## Check your changes

```sh
npm install                       # once
npx playwright install chromium   # once: the browser the end-to-end tests drive
make check
```

`make check` is the gate before every commit: `gofmt`, `go vet`, `staticcheck`, the Go tests with the race detector, each fuzz target for 10 seconds, the type check and unit tests of the extension, and the end-to-end tests in headless Chromium. `make test` runs the Go tests alone.

- The end-to-end tests need port 4655 free, because that is where the extension sends go links. Stop a running Ogre server first; the tests use a temporary database, never yours. If a test run was killed and left an `ogre` process behind, the next run says so and names its process id.
- `staticcheck` is fetched at a pinned version and needs the network the first time. Offline, `make check STATICCHECK_OPTIONAL=1` skips that one step and says so.

Before a release, also check by hand what the tests can't reach: with `extension/` loaded in Chrome, type `go/roadmap` in the address bar, click a go link on a web page, open one from a bookmark, and repeat after restarting the server and after restarting Chrome.

## Where things are

| Path | Holds |
|---|---|
| `cmd/ogre` | The command line: `serve` (the default) and `version` |
| `internal/` | The server: link rules (`linkmodel`), storage (`store`), the in-memory index (`index`), use cases (`app`), HTTP (`httpd`, `web`), settings (`config`) |
| `extension/` | The Ogre extension for Chrome, loadable as it is |
| `e2e/` | Playwright tests |
| [docs/PRD.md](docs/PRD.md) | What the product is and why |
| [docs/IMPLEMENTATION_PLAN.md](docs/IMPLEMENTATION_PLAN.md) | Architecture and milestones |
| [docs/spike-chrome.md](docs/spike-chrome.md) | What was verified about Chrome's behavior |

## License

[MIT](LICENSE).
