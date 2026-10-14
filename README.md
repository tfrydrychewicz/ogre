# Ogre

Ogre is a go-links tool in two parts. The Ogre server is the Directory: a small server that turns a short name such as `go/roadmap` into the address it stands for. The Ogre extension for Chrome sends `go/…` to that server. The server runs on your own computer and keeps its links in one SQLite file.

**State: milestone L4, the local MVP.** Everything a single user needs on one computer: the server and its web UI, and the extension with its first-run and settings pages. A link can have several routes with named, typed variables; the resolver explains every miss and suggests near names. Links are found, created, edited, tried and deleted in the browser at `http://localhost:4655`; the JSON API below does the same for scripts.

## Get started

About five minutes on a Mac once Chrome, Go and git are installed, most of it reading; installing Go or Apple's developer tools first adds a few minutes more. You need:

- **Google Chrome** 116 or newer.
- **Go** 1.26 or newer, the only thing Ogre needs to build and run. Install it from [go.dev/dl](https://go.dev/dl/) (the macOS package) or with `brew install go`, then check with `go version`. A Go from 1.21 to 1.25 also works: it fetches Go 1.26 by itself the first time, which needs the network. If `go` says instead that `go.mod requires go >= 1.26`, your Go is set not to fetch toolchains (some packaged builds are): install Go 1.26 from [go.dev/dl](https://go.dev/dl/).
- **git**, to get the code. On a Mac without it, the first `git` command offers to install Apple's command line developer tools: accept, and run the command again once they are installed. Or download the code as a ZIP from the repository's GitHub page and unpack it.

You do not need Node.js, npm, or a build step to use Ogre; those are only for developing it ([Develop Ogre](#develop-ogre)).

### 1. Start the server

```sh
git clone https://github.com/tfrydrychewicz/ogre.git
cd ogre
go run ./cmd/ogre
```

The first run downloads the SQLite driver and compiles everything, which takes up to a minute; later starts take a second. It is running when it prints a line with `msg=listening addr=127.0.0.1:4655`. Leave this terminal open: the server runs until you press Ctrl-C there.

The server listens on `http://localhost:4655`, reachable from this computer only. On first run it creates its database in your per-user data directory: `~/Library/Application Support/ogre/ogre.db` on macOS, `~/.local/share/ogre/ogre.db` on Linux. Pass `--db <file>` to keep it somewhere else, and see `go run ./cmd/ogre help` for the other flags.

### 2. Load the extension

1. In Chrome, open a new tab and type `chrome://extensions` in the address bar.
2. Switch on **Developer mode**, top right.
3. Click **Load unpacked** and choose the folder named `extension` inside the `ogre` folder you cloned. In the file dialog, Cmd+Shift+G opens a box to paste a path into; [http://localhost:4655/-/setup](http://localhost:4655/-/setup) shows this folder's full path with a Copy button.

"Ogre" appears among your extensions, and its first-run page opens in a new tab:

- **Step 1** checks that your server answers.
- **Step 2** asks you to type `go/` in the address bar and press Enter. This opens the Directory's home page, and teaches Chrome that `go` is a site, so that afterwards `go/name` opens the link instead of searching for it. The page notices by itself when you have done it.
- **Step 3** has you try a go link without the trailing slash.

[extension/README.md](extension/README.md) explains each permission the extension asks for. It reads no pages, keeps no history, and talks to no server but yours.

### 3. Create your first link

On the Directory's home page (`go/`, or [http://localhost:4655](http://localhost:4655)), click **New link**. (If step 3 of the first-run page already left you on "No link go/roadmap", its **Create go/roadmap** button opens the same form with the name filled in.) Enter `roadmap` as the pattern and any web address as the destination, for example `https://example.com/roadmap`, and click **Create link**. Now type `go/roadmap` in the address bar and press Enter: Chrome opens the destination.

Or, from a second terminal:

```sh
curl -X POST http://localhost:4655/-/api/v1/links \
  -H 'Content-Type: application/json' \
  -d '{"description":"Product roadmap","routes":[{"pattern":"roadmap","destination":"https://example.com/roadmap"}]}'
```

### When something is wrong

**Chrome searches for `go/roadmap` instead of opening it.** Chrome doesn't know `go` as a site yet. Type `go/`, with the slash, and press Enter once; from then on `go/roadmap` opens. Until then, `go/roadmap/` (trailing slash) and `http://go/roadmap` always open. In an Incognito window the extension does not run unless you allow it: on `chrome://extensions`, **Details** under Ogre, **Allow in Incognito**. (Not tested yet: whether go links then work in Incognito has not been tried, as the setup page also says.)

**The server says the port is in use.** `go run ./cmd/ogre` stops with one of two messages:

- `another ogre (version "…") is already running there`: an Ogre server is already running, perhaps in another terminal window. Use that one, or stop it with Ctrl-C in its window and start again.
- `the port is in use by another program`: something else holds port 4655. `lsof -nP -iTCP:4655 -sTCP:LISTEN` names it. Stop it, or run Ogre on another port, `go run ./cmd/ogre --addr localhost:4656`, and tell the extension (next item). The first-run page shows this case as **Wrong server**.

Started with `--addr`, the server is at that address instead: use it wherever this README says `localhost:4655`, including the setup page (`http://localhost:4656/-/setup`) and the curl commands. Start it the same way every time, or put the address in the environment once, `export OGRE_ADDR=localhost:4656` in your shell's profile, so that a plain `go run ./cmd/ogre` uses it.

**The extension says the Directory isn't answering** (step 1 of the first-run page says "Not answering"; "Test connection" on the settings page says "Could not connect"; a go link ends on Chrome's "This site can't be reached… localhost refused to connect"):

- The server isn't running: its terminal was closed or Ctrl-C was pressed. Start it again with `go run ./cmd/ogre` in the `ogre` folder (with the same `--addr` as before, if you gave one), then click **Check again** on the first-run page.
- The server runs at another address than the extension's: the server prints its address in its first line (`url=http://localhost:…`). Open the extension's settings (on `chrome://extensions`, **Details** under Ogre, then **Extension options**), enter that address, **Save**, and click **Allow** when Chrome asks whether Ogre may access that site. An open first-run page picks up the new address by itself.

**Nothing happens when you type `go/`.** Check on `chrome://extensions` that Ogre is switched on and shows no **Errors** button. If you moved or deleted the `ogre` folder, load the extension again from its new place.

### Stop and start again

Ctrl-C in the server's terminal stops it; your links stay in the database file. To start it again, run `go run ./cmd/ogre` in the `ogre` folder, with the same `--addr` if you started it with one. The extension needs nothing: it keeps working across Chrome restarts. (Starting the server by itself at login comes in a later milestone.)

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

The extension has two pages of its own. The **first-run page** opens once, when the extension is installed (step 2 above). The **settings page** (on `chrome://extensions`, **Details** under Ogre, then **Extension options**) holds the Directory URL, `http://localhost:4655` unless you change it: **Test connection** asks the server there whether it is an Ogre server and shows its version, and **Save** sends go links there from then on. An address that is not plainly `http://` or `https://`, a host and an optional port is refused with a sentence that says what to change; one where no server answers can still be saved after a warning. Saving another address asks Chrome for access to that one site and gives up access to the one before.

The reading pages (home and search, link pages, miss pages, setup) work without JavaScript; creating, editing and deleting need it. The pages load nothing from other sites and run under a strict content security policy (no inline script or style, and Trusted Types: no string ever becomes markup).

## Links from the command line

The curl command of step 3 creates a link without the browser. Without the extension, a go link also opens at the Directory's own address:

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

## Develop Ogre

Everything below is for working on Ogre itself, not for using it.

### Tools

- Go 1.26 or newer, as above.
- Node.js 20 or newer (the minimum of Playwright 1.64, which the end-to-end tests use; they are run with Node 26 here) and npm. Node.js runs only the checks: type-checking, the unit tests of the browser code, and the browser tests. Nothing in Ogre is built with it.
- Playwright's own builds of Chromium, Firefox and WebKit, a separate download of about 1 GB on disk. The tests launch only these, never your Chrome.

```sh
npm install                                        # once
npx playwright install chromium firefox webkit     # once, and after each Playwright upgrade
make check
```

### Check your changes

`make check` is the gate before every commit: `gofmt`, `go vet`, `staticcheck`, the Go tests with the race detector, each fuzz target for 10 seconds, the type check of the browser code, the unit tests of the extension and of the web UI's modules (`e2e/unit`), a scan of the browser code for DOM sinks that parse markup, the end-to-end tests in headless Chromium with the extension loaded (`make e2e`), and the web UI's tests in Playwright's Firefox and WebKit (`make e2e-browsers`). Every page the end-to-end tests open, the extension's included, fails the test on a content security policy violation or a script error.

| Test file | Covers |
|---|---|
| `e2e/scenarios.spec.mjs` | The PRD's scenarios 1 to 7, through the extension and the pages |
| `e2e/ui.spec.mjs`, `e2e/behaviour.spec.mjs` | The accessibility scan of every page in light and dark, a 375-pixel-wide pass, the pages without JavaScript, search by keyboard alone; hostile text, deleting, renaming, late answers, long names |
| `e2e/redirect.spec.mjs`, `e2e/variables.spec.mjs`, `e2e/extension.spec.mjs` | The redirect from `go/…` to the destination, the request guards, restarts of the server; the extension as installed: its permissions and its one rule |
| `e2e/passthrough.spec.mjs` | Path, query, fragment, percent-encoding, spaces and non-ASCII text through both redirects |
| `e2e/settings.spec.mjs` | The settings page with two servers: re-pointing go links, refusing unusable addresses, the warning for an address that doesn't answer, giving up access |
| `e2e/first-run.spec.mjs` | The first-run page with the server up and down: it opens once, notices `go/` and a go link, and never opens again once done |
| `e2e/fetch-metadata.spec.mjs` | Which `Sec-Fetch-Site` the server receives through the extension, and how the create and edit forms treat a prefill that comes that way |
| `e2e/browsers.spec.mjs`, `e2e/webkit-select-csp.spec.mjs` | Scenarios 1 to 7, the accessibility scan and phone width in Firefox and WebKit, and the repro of WebKit's report for `<select>` |
| `e2e/harness.spec.mjs` | Negative controls: the policy watch and the accessibility scan fail when they should |

Other targets: `make test` runs the Go tests alone, `make fuzz FUZZTIME=5m` fuzzes for longer, `make bench` runs the benchmarks at 100,000 links, and `make perf` measures the built server over HTTP at 100,000 links ([docs/perf.md](docs/perf.md)). `make check` also lists the rows of the PRD's examples that wait for a later milestone (`make spec-pending`).

- The end-to-end tests run their own server on a temporary database, never yours, at `http://localhost:4655`, where the extension sends go links by default. If your own Ogre server is running there, stop it, or run the tests on another port: `OGRE_E2E_PORT=46555 make check`. With another port, each test browser is first pointed at the test server through the extension's settings, as a user would point it at a Directory elsewhere; the only request that can still reach port 4655 is the first-run page, opened at install, asking whether a server runs there; the tests close that page as soon as the extension has started, except the first-run page's own tests, where it follows the test server's address. If a test run was killed and left an `ogre` process behind, the next run says so and names its process id.
- `staticcheck` is fetched at a pinned version and needs the network the first time. Offline, `make check STATICCHECK_OPTIONAL=1` skips that one step and says so. Likewise, without Playwright's Firefox and WebKit `make check` fails and says how to install them; `make check E2E_BROWSERS_OPTIONAL=1` skips them, out loud.

What no automated test can reach — typing in Chrome's address bar, Chrome's permission prompt, a real restart of Chrome, Incognito, the real Firefox and Safari — is in [docs/manual-tests.md](docs/manual-tests.md), about 20 minutes, run before each release.

### Work on the web UI

The pages are Go templates in `internal/web/templates`, with stylesheets and plain ES modules in `internal/web/static`: no framework, no bundler, no build step. The modules are type-checked from their JSDoc comments by `npm run typecheck`. The server serves them under a content-hash prefix (`/-/static/<hash>/…`, cached for a year), so a changed file gets a new address.

```sh
go run ./cmd/ogre --dev --addr localhost:4656 --db /tmp/ogre-dev.db
```

`--dev` reads templates and static files from the checkout on every request, so an edit shows on reload without a restart; the footer says "development mode". It looks for the checkout from the working directory up, then where the binary was built, and refuses to start without one. The command above keeps it apart from your own Directory: another port and a scratch database.

Browsers' own requests (`/favicon.ico`, `/robots.txt`, `/apple-touch-icon*.png`, `/.well-known/…`, `/sitemap.xml`) are answered directly and never treated as go links or logged as misses.

### Work on the extension

The extension is plain ES modules too, loaded by Chrome as they are; after an edit, click the reload arrow on Ogre's card on `chrome://extensions`. [extension/README.md](extension/README.md) describes its files, permissions and storage. `npm run test:extension` runs its unit tests.

### Where things are

| Path | Holds |
|---|---|
| `cmd/ogre` | The command line: `serve` (the default) and `version` |
| `cmd/ogre-perf` | The performance run behind `make perf`; not part of the product |
| `internal/` | The server: link rules (`linkmodel`), storage (`store`), the in-memory index and search (`index`), use cases (`app`), HTTP and pages (`httpd`), templates, styles and browser modules (`web`), settings (`config`), the performance run (`perf`) |
| `extension/` | The Ogre extension for Chrome, loadable as it is |
| `e2e/` | Playwright tests |
| [docs/PRD.md](docs/PRD.md) | What the product is and why |
| [docs/IMPLEMENTATION_PLAN.md](docs/IMPLEMENTATION_PLAN.md) | Architecture and milestones |
| [docs/spike-chrome.md](docs/spike-chrome.md) | What was verified about Chrome's behavior |
| [docs/manual-tests.md](docs/manual-tests.md), [docs/perf.md](docs/perf.md) | The checks made by hand, and the performance method and results |

## License

[MIT](LICENSE).
