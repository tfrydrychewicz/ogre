# Ogre

Ogre is a go-links tool in two parts. The Ogre server is the Directory: a small server that turns a short name such as `go/roadmap` into the address it stands for. The Ogre extension for Chrome sends `go/…` to that server. The server runs on your own computer and keeps its links in one SQLite file.

**State: milestone L3, the web UI.** A link can have several routes with named, typed variables; the resolver explains every miss and suggests near names. Links are found, created, edited, tried and deleted in the browser at `http://localhost:4655`; the JSON API below does the same for scripts.

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

## Use it

Open `http://localhost:4655` (or type `go/` once the extension is loaded).

| Page | Address | What it does |
|---|---|---|
| Home | `/-/` | Search as you type, ranked: exact name, name prefix, misspelled name, then words in names, descriptions, destinations and owners. `↓` moves into the results, `Enter` opens one. Below the box: your links and the recently added ones |
| New link | `/-/new` | Routes (pattern and destination), a description, and a variables table where each variable gets a type, a note and an example. Everything is checked by the Directory as you type; "Try it" previews the unsaved link with sample values and opens it in a new tab. A name that is taken offers to add your routes, notes and all, to that link. `?pattern=` and `?destination=` prefill it; when another site opened it that way, the page warns and names where each destination leads |
| Link | `/-/links/{name}` | Routes, variables, owner, buttons that copy `go/{name}` and its full address, and a launcher: fill in the values (each is checked as you type it) and open the result |
| Edit, Delete | `/-/links/{name}/edit`, `/-/links/{name}/delete` | Changing the first route's name renames every route. Saving a link someone changed meanwhile is refused and says so, keeping what you typed; so is deleting one. Routes another site asks to add to a link are left out |
| Setup | `/-/setup` | How to load the extension, check it, and the extension-free search shortcut |
| Miss | the address typed | A go link that doesn't resolve answers `404` with "Did you mean" (`Enter` follows the first suggestion), or, for a known name, why each route didn't fit; plus "Create" and a search box holding the name |

The reading pages (home and search, link pages, miss pages, setup) work without JavaScript; creating, editing and deleting need it. The pages load nothing from other sites and run under a strict content security policy (no inline script or style, and Trusted Types: no string ever becomes markup).

## Create a link from the command line

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

## Variables

A link is a name and one or more routes. A route pairs a pattern with a destination; a segment of the pattern in braces is a variable, and the destination uses its value as `{name}`:

```sh
curl -X POST http://localhost:4655/-/api/v1/links \
  -H 'Content-Type: application/json' \
  -d '{"description":"GitHub shortcuts","routes":[
        {"pattern":"gh",                     "destination":"https://github.com/acme"},
        {"pattern":"gh/{repo}",              "destination":"https://github.com/acme/{repo}"},
        {"pattern":"gh/{repo}/pr/{num:int}", "destination":"https://github.com/acme/{repo}/pull/{num}",
         "variables":{"num":{"description":"Pull request number","example":"42"}}}]}'

curl -i http://localhost:4655/gh/ogre/pr/42      # 302 to https://github.com/acme/ogre/pull/42
curl -i http://localhost:4655/gh/ogre/pr/latest  # 404, saying that num doesn't accept "latest"
curl -s 'http://localhost:4655/-/api/v1/resolve?path=gh/ogre/pr/42'   # the same decision, as JSON
```

| Type | Written | Accepts |
|---|---|---|
| `string` | `{repo}` | Any text; the default |
| `int` | `{num:int}` | A whole number: `42`, `-7`, `007` |
| `number` | `{ratio:number}` | A number, with or without a fraction: `3`, `-0.5` |
| `regex` | `{key:regex([A-Z]+-[0-9]+)}` | Text of at most 256 bytes that matches the whole expression ([RE2 syntax](https://github.com/google/re2/wiki/Syntax)) |

- All routes of a link begin with the link's name, and the name is never sent on its own: it is that first segment.
- A request matches a route with the same number of segments whose literals are equal (ignoring case) and whose variables accept their values. When several routes match, the most specific wins, compared from the left: a literal beats a typed variable, which beats `{text}`.
- A name nobody has answers `404`, with "Did you mean" for existing names one or two edits away (none for names of one or two characters); what was typed after the name is carried over when it fits. A suggestion that fits says where it leads; one that doesn't lists how that link is used.
- Values are case-sensitive and are percent-encoded into the destination, so a value can never add a path segment or a query parameter. A variable may stand in the path, query or fragment of a destination, never in its scheme or host. A value that goes into the destination's path can't be `.` or `..`, or have either between slashes (`../x`).
- Regular expressions run in linear time, and their size is limited, one by one and for a link as a whole, so that no saved link can make a request slow (about 10 ms at worst; `make bench` measures it).
- A destination is an absolute `http` or `https` URL without a user name or password, and may not be a go link or an address of the Directory itself, however that address is written (`127.1`, `0x7f.0.0.1` and `[0:0:0:0:0:0:0:1]` are recognized).

The rules, with worked examples, are in [docs/PRD.md](docs/PRD.md), section 5. The examples are also data, [internal/linkmodel/testdata/spec.json](internal/linkmodel/testdata/spec.json), replayed by the tests against the link model, the use cases and the running server.

## API

Everything is under `http://localhost:4655/-/api/v1`; [docs/IMPLEMENTATION_PLAN.md](docs/IMPLEMENTATION_PLAN.md), section 3.5, shows the JSON.

| Endpoint | Does |
|---|---|
| `GET /links` | Lists every link. `?q=daily+report` searches instead: the same link objects in rank order, each with a `rank` (`exact`, `prefix`, `misspelled`, `name`, `description`, `destination` or `owner`) and the `tokens` of each route's pattern; `limit` (default 20, at most 100) |
| `POST /links` | Creates a link from `description` and `routes`; `201`, or `409 link.name_taken` naming the link that has the name |
| `GET /links/{id}` | Returns one link |
| `PATCH /links/{id}` | Replaces `description` and `routes` (send all of them; a route keeps its identity through its `id`). Needs `version` as last read; a stale one answers `409 link.version_conflict`, before anything is said about the draft. Routes with another first segment rename the link. `403 link.forbidden` names the link and its owner |
| `DELETE /links/{id}` | Deletes the link; `204`. `?version=N` deletes only if the link is still at that version |
| `POST /links/validate` | Checks a draft without saving, by the same rules as saving: problems with positions, the tokens of each pattern, whether the name is free, and whether `samples` values would be accepted. `"describeVariables": true` adds each route's variables with their positions; `"explainSamples": true` adds `sampleReasons`, the sentence each refused sample would get; a route's `"expressions": {"v": "[0-9"}` checks regular expressions on their own, answered in the route's `expressions`; `"preview": {"route": 0, "path": "reports/10/13"}` says what the draft, saved as it is, would do with that request (`hit` with the destination, `miss_known` with reasons) |
| `GET /resolve?path=reports/10/13` | Says what the resolver would do with `go/reports/10/13`, without redirecting: `hit` with the values and destination, `miss_known` with a reason per route, `miss_unknown` with suggestions, `home` or `bad_request` |
| `GET /meta` | Product, version and API version |

A draft that can't be saved answers `422 link.invalid`. Its `error.details.routes` has one entry per route you sent, in the same order, each with that route's `problems`; `error.details.problems` holds the ones that belong to no single route. A problem has a `code` that stays stable, a `message` to show, a `field` (`pattern` or `destination`, or `route` or `link` for a problem with the route or the link as a whole), and `start` and `end` marking the spot in the pattern or destination, counted as JavaScript counts string positions (UTF-16 code units). Warnings (`"severity": "warning"`) do not stop a save.

Every change to a link also writes a revision to the database. Nothing reads them yet; history and revert arrive in a later milestone.

A page of another site cannot use the API: writes are refused, and so are reads made from a script, an image or a frame (`403 request.cross_site`). `curl` and other programs are not affected.

The request log names the decision and the link (`decision=hit link=gh`), never the values, the path or the query.

## Without the extension

Go links also work at the Directory's own address, from any browser or script: `http://localhost:4655/roadmap`.

To make `http://go/roadmap` work without the extension, point the name `go` at this computer (a line `127.0.0.1 go` in `/etc/hosts`) and tell the Ogre server to answer to that name. It refuses names it wasn't told about, which is what keeps a hostile web page from reaching it under a made-up name:

```sh
go run ./cmd/ogre --allow-host go        # then open http://go:4655/roadmap
```

`http://go/roadmap` with no port means port 80. Either forward port 80 to 4655 and start the server with `--allow-host go:80`, or listen there directly with `--addr localhost:80 --allow-host go`, which needs the rights to open a low port.

## Work on the web UI

The pages are Go templates in `internal/web/templates`, with stylesheets and plain ES modules in `internal/web/static`: no framework, no bundler, no build step. The modules are type-checked from their JSDoc comments by `npm run typecheck`. The server serves them under a content-hash prefix (`/-/static/<hash>/…`, cached for a year), so a changed file gets a new address.

```sh
go run ./cmd/ogre --dev --db /tmp/ogre-dev.db
```

`--dev` reads templates and static files from the checkout on every request, so an edit shows on reload without a restart; the footer says "development mode". It looks for the checkout from the working directory up, then where the binary was built, and refuses to start without one.

Browsers' own requests (`/favicon.ico`, `/robots.txt`, `/apple-touch-icon*.png`, `/.well-known/…`, `/sitemap.xml`) are answered directly and never treated as go links or logged as misses.

## Check your changes

```sh
npm install                       # once
npx playwright install chromium   # once: the browser the end-to-end tests drive
make check
```

`make check` is the gate before every commit: `gofmt`, `go vet`, `staticcheck`, the Go tests with the race detector, each fuzz target for 10 seconds, the type check of the browser code, the unit tests of the extension and of the web UI's modules (`e2e/unit`), a scan of the browser code for DOM sinks that parse markup, and the end-to-end tests in headless Chromium. Those include the PRD's scenarios 1 to 7 driven through the pages (`e2e/scenarios.spec.mjs`, with the extension), the accessibility scan of every page in light and dark, a 375-pixel-wide pass, the pages without JavaScript and search by keyboard alone (`e2e/ui.spec.mjs`), what the pages' modules do (`e2e/behaviour.spec.mjs`: hostile text, deleting, renaming, late answers, long names), and negative controls that show the policy watch and the accessibility scan fail when they should (`e2e/harness.spec.mjs`); every page they open fails the test on a content security policy violation or a script error. `make test` runs the Go tests alone, `make fuzz FUZZTIME=5m` fuzzes for longer, and `make bench` runs the benchmarks at 100,000 links and the costliest request a saved link allows. `make check` also lists the rows of the PRD's examples that wait for a later milestone (`make spec-pending`).

- The end-to-end tests need port 4655 free, because that is where the extension sends go links. Stop a running Ogre server first; the tests use a temporary database, never yours. If a test run was killed and left an `ogre` process behind, the next run says so and names its process id.
- `staticcheck` is fetched at a pinned version and needs the network the first time. Offline, `make check STATICCHECK_OPTIONAL=1` skips that one step and says so.

Before a release, also check by hand what the tests can't reach: with `extension/` loaded in Chrome, type `go/roadmap` in the address bar, click a go link on a web page, open one from a bookmark, and repeat after restarting the server and after restarting Chrome.

## Where things are

| Path | Holds |
|---|---|
| `cmd/ogre` | The command line: `serve` (the default) and `version` |
| `internal/` | The server: link rules (`linkmodel`), storage (`store`), the in-memory index and search (`index`), use cases (`app`), HTTP and pages (`httpd`), templates, styles and browser modules (`web`), settings (`config`) |
| `extension/` | The Ogre extension for Chrome, loadable as it is |
| `e2e/` | Playwright tests |
| [docs/PRD.md](docs/PRD.md) | What the product is and why |
| [docs/IMPLEMENTATION_PLAN.md](docs/IMPLEMENTATION_PLAN.md) | Architecture and milestones |
| [docs/spike-chrome.md](docs/spike-chrome.md) | What was verified about Chrome's behavior |

## License

[MIT](LICENSE).
