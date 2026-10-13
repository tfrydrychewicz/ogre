# Implementation Plan: Go Links, local first

| | |
|---|---|
| **Status** | Draft for review |
| **Date** | 2026-10-13 |
| **Based on** | [PRD.md](PRD.md) |

Requirement IDs such as DIR-1 or NFR-8, question numbers such as Q4, and section numbers prefixed "PRD" refer to the PRD.

## 1. Shape of the plan

One rule orders everything here: **the product must be fully usable on one machine before any work goes into cloud deployment.**

"Fully usable locally" means more than the PRD's local MVP. It means a person can depend on it every day: it starts by itself at login, says what is wrong when it isn't running, keeps backups, survives upgrades, and has the features a single user would miss. So this plan pulls the PRD's locally relevant P1 and P2 requirements ahead of the team release, and adds a few local-only items the PRD doesn't mention (section 2.3).

The work runs on two tracks with a gate between them.

| Milestone | Outcome | PRD scope | Size |
|---|---|---|---|
| **Local track** | | | |
| L0 Spike | The Chrome behaviors the design depends on are confirmed in a real browser | PRD M0 | S |
| L1 Walking skeleton | A link created with `curl` redirects when typed as `go/name` in Chrome | A slice of P0 | M |
| L2 Link model | Variables, types, precedence, misses, and suggestions work through the API, proven by the PRD's own examples | P0, PRD section 5 | L |
| L3 Web UI | Everything a person does in the browser: find, create, edit, launch, recover from a miss | P0 | L |
| L4 Extension and MVP | Settings and first-run pages; the README gets a new user running in 5 minutes | P0 complete; equals PRD M1 | M |
| L5 Daily driver | Starts at login, explains outages, backs up, exports, shows usage and history | Local-relevant P1, plus additions | L |
| L6 Power features | Popup, address-bar suggestions, more variable types, query passthrough, restore | Local-relevant P2 | L |
| **Gate: local release** | Ten working days of daily use with no data loss; clean-machine install passes | | |
| **Cloud track** | | | |
| C1 Multi-user | Sign-in, ownership, and roles, verified on a laptop against a local identity provider | P1 | L |
| C2 Deployable | Container image, operations endpoints, backup runbook, packaged extension | P1 | M |
| C3 First deployment | A team uses a hosted Directory; links migrated by export and import | Equals PRD M2 | S |
| **Backlog** | The remaining P2 requirements and all of P3 | | |

Sizes compare milestones with each other; they are not time estimates.

Sequencing rules:

- Nothing in L1–L6 depends on the cloud track, and the cloud track changes no local behavior. After C3, `godir serve` with no configuration behaves exactly as it did at the gate.
- Every milestone ends with a tagged, working product. From L4 on, the owner can use it for real.
- L5 is not optional. If cloud deployment becomes urgent, L6 is where to cut: its tasks are independent of each other and of the cloud track.
- After L1, the extension tasks of L4 (L4.1–L4.5) don't depend on L2 or L3 and can run alongside them.

This plan replaces the sequencing in PRD section 11. The PRD's M1 is L1–L4 here, its M2 is C1–C3, and part of its M3 moves forward into L5 and L6.

## 2. Decisions

### 2.1 PRD questions this plan assumes answered

| Question | Assumed answer | If the answer changes |
|---|---|---|
| Q4, stack | Decided: Go, SQLite, JavaScript for the browser side — the PRD's recommendation, refined in 2.2. The owner confirmed Go on 2026-10-13 (L0.4), after comparing it with Node.js | Sections 3 and 4 and the task wording change; the milestones and their order do not |
| Q5, name matching | Decided by the owner on 2026-10-13: names ignore case only. `-` and `_` are part of the name, so `on-call`, `on_call`, and `oncall` are different links | — |

Q1, Q2, Q3, and Q6 are only needed by the cloud track and stay open until then. Q8 (the product's name) is handled with a placeholder.

### 2.2 Technical choices

| Topic | Choice | Reason |
|---|---|---|
| Server language | Go 1.26, the version installed on the development machine; standard-library HTTP server | RE2 regular expressions built in (NFR-8); one static binary |
| Database | SQLite through `modernc.org/sqlite`, a pure-Go driver | Nothing to install, no C compiler, cross-compiles. The store sits behind an interface, so Postgres can be added in C2 if Q1 requires it |
| Schema changes | Numbered SQL files embedded in the binary, applied at startup | DEP-6, with no external tool |
| HTML | `html/template`, rendered on the server | Automatic contextual escaping (NFR-11); miss pages work without JavaScript |
| Browser code | Plain JavaScript modules written as custom elements, for both the web UI and the extension. No framework, no bundler | No build step: `go run` serves the UI, and Chrome loads `extension/` as it is (DEP-1, DEP-2) |
| Types in JavaScript | JSDoc annotations checked by the TypeScript compiler (`tsc --noEmit`) | Type safety without compiling anything |
| Tests | Go's `testing` package, including fuzzing and benchmarks; `node --test` for extension logic; Playwright for end-to-end tests with the extension loaded | Section 4 |
| Logging | `log/slog` | Structured logs from the standard library |
| Working name | `godir`: the binary, Go module, data directory, and extension name | A placeholder until Q8; section 3.1 lists every place it appears |
| Default address | Listens on `127.0.0.1:4655` and `[::1]:4655`; the Directory URL is `http://localhost:4655` | DEP-1 and DEP-3. 4655 spells GOLK on a phone keypad, and nothing uses it on the development machine |

The product has one runtime dependency (the SQLite driver) until C1. Node.js is needed only to run checks and end-to-end tests, never to build or run the product.

### 2.3 Where this plan departs from or adds to the PRD

| # | Change | Why |
|---|---|---|
| 1 | The extension is written in type-checked JavaScript, not TypeScript source | A compile step would add Node.js to the five-minute setup (DEP-2) |
| 2 | Locally relevant P1 and P2 requirements ship before the team release | The local-first rule |
| 3 | Added: a background service that starts at login (`godir service install`) | Without it, "usable locally" means keeping a terminal open |
| 4 | Added: a daily database backup, and a copy before every schema change | A personal Directory holds data that exists nowhere else |
| 5 | Added: request guards in local mode — a Host allow-list, a same-origin check on writes, and JSON-only writes | A server on localhost with no sign-in can be reached by any web page the user visits (section 3.8). The PRD asks for cross-site protection only when deployed (NFR-12) |
| 6 | Added: a short list of reserved names such as `favicon.ico` and `robots.txt` | Browsers request these paths unprompted. The PRD assumed `/-/` was the only reservation |
| 7 | Added: a request with malformed percent-encoding answers `400` instead of a miss | The PRD doesn't define the case |
| 8 | MISS-5 (rescue counting) moves from P1 to C2 | It feeds team metrics, not local use |
| 9 | Deleting is permanent until DIR-9 arrives in L6 | Reserving a deleted name without a restore screen would only confuse |
| 10 | Added: `godir doctor`, a diagnostics command | Local problems have no operator to call |

## 3. Technical design

### 3.1 Repository layout

```text
.
├── cmd/godir/            Command line: serve (default), version, service, export, import,
│                         backup, restore, seed, doctor
├── internal/
│   ├── linkmodel/        The rules of PRD section 5: parse, validate, match, expand, suggest.
│   │   └── testdata/     spec.json — the PRD 5.7 examples as fixtures
│   ├── store/            Store interface, SQLite implementation, migrations/*.sql
│   ├── index/            In-memory snapshot of compiled links; search
│   ├── app/              Use cases: resolve, validate, create, update, delete, search,
│   │                     export, import
│   ├── httpd/            Listeners, request guards, routing, resolver, JSON API
│   ├── web/              Page handlers, templates/, static/ (CSS and JavaScript), embedded
│   ├── config/           Flags, environment variables, data-directory rules
│   └── osservice/        launchd and systemd integration
├── extension/            Manifest V3 extension, loadable unpacked as it is
├── e2e/                  Playwright tests
├── docs/                 PRD.md, this plan, spike-chrome.md, manual-tests.md, perf.md
├── Makefile              run, test, check, e2e, install
├── package.json          Development tools only
├── go.mod
└── README.md
```

Renaming the product (Q8) touches: the Go module path, `cmd/godir`, the binary name, the data-directory name, the `GODIR_` environment prefix, the service label, the `product` field of the `meta` endpoint, the extension's `name`, and the `format` field of export files.

### 3.2 Components and their boundaries

- **`linkmodel` is pure.** It imports nothing else from the project and performs no I/O. Everything about PRD section 5 lives there and nowhere else (NFR-22). Browser code never parses a pattern; it shows what the API returns.
- **`app` is the only package that writes.** Every function takes an `Actor` — who is acting. Local mode supplies the built-in admin user; C1 swaps in the signed-in user without touching the use cases.
- **`index` holds an immutable snapshot**, replaced atomically after each committed write. The resolver reads only the snapshot, so a hit never touches the database.
- **`httpd` and `web` contain no rules.** They translate HTTP into `app` calls and render the results.

### 3.3 Request routing

The outermost handler decides in this order:

1. **Host check** (section 3.8). An unlisted `Host` gets `403`.
2. **`/`** redirects to `/-/` (RES-5).
3. **`/-/…`** goes to the web UI and API, routed by the standard `ServeMux`.
4. **Reserved paths** — `favicon.ico`, `robots.txt`, `sitemap.xml`, `apple-touch-icon*.png`, and anything under `.well-known/` — are answered directly. They are never treated as go links, and the names are rejected when a link is saved.
5. **Everything else** is a go link, resolved from the raw, still-encoded path (`r.URL.EscapedPath()`).

`ServeMux` is deliberately not used at the root: it cleans paths and redirects requests containing `//` or `..`, which would corrupt variable values before the link model sees them.

The resolver accepts `GET` and `HEAD` only and answers:

| Decision | Response |
|---|---|
| Hit | `302` with `Location` and `Cache-Control: no-store` (RES-3) |
| Miss, either kind | `404` with the miss page |
| Malformed encoding | `400`. A malformed percent-escape (`go/a%zzb`, `go/%`) never reaches the handler: Go's HTTP server answers it with its own plain `400` (L0 spike, check 9, headless Chromium). The handler's own `400`, with a short page, is left for paths that reach it and still fail L2.5, such as invalid UTF-8 after decoding; the spike had no such variant |

### 3.4 Data model

`0001_init.sql`, applied in L1:

```sql
CREATE TABLE users (
  id            INTEGER PRIMARY KEY,
  email         TEXT    NOT NULL UNIQUE,
  display_name  TEXT    NOT NULL,
  role          TEXT    NOT NULL CHECK (role IN ('user', 'admin')),
  status        TEXT    NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'deactivated')),
  created_at    TEXT    NOT NULL
);

CREATE TABLE links (
  id           INTEGER PRIMARY KEY,
  name         TEXT    NOT NULL UNIQUE,    -- always lowercase: 'on-call'
  description  TEXT    NOT NULL DEFAULT '',
  owner_id     INTEGER NOT NULL REFERENCES users(id),
  version      INTEGER NOT NULL DEFAULT 1, -- optimistic concurrency
  created_at   TEXT    NOT NULL,
  updated_at   TEXT    NOT NULL
);

CREATE TABLE routes (
  id          INTEGER PRIMARY KEY,
  link_id     INTEGER NOT NULL REFERENCES links(id) ON DELETE CASCADE,
  position    INTEGER NOT NULL,            -- tie-break order of PRD 5.5
  pattern     TEXT    NOT NULL,            -- canonical text: 'gh/{repo}/pr/{num:int}'
  shape       TEXT    NOT NULL,            -- pattern with variable names removed
  destination TEXT    NOT NULL,
  UNIQUE (link_id, shape)
);
CREATE INDEX routes_by_link ON routes (link_id, position);

CREATE TABLE variables (                   -- only what the pattern text doesn't carry
  route_id    INTEGER NOT NULL REFERENCES routes(id) ON DELETE CASCADE,
  name        TEXT    NOT NULL,
  description TEXT    NOT NULL DEFAULT '',
  example     TEXT    NOT NULL DEFAULT '',
  PRIMARY KEY (route_id, name)
);

CREATE TABLE revisions (                   -- written on every change from L2 on
  id        INTEGER PRIMARY KEY,
  link_id   INTEGER NOT NULL,              -- no cascade: history outlives the link
  editor_id INTEGER NOT NULL REFERENCES users(id),
  at        TEXT    NOT NULL,
  action    TEXT    NOT NULL,              -- create, update, delete, restore, import, transfer
  snapshot  TEXT    NOT NULL               -- the whole link as JSON, after the change
);
CREATE INDEX revisions_by_link ON revisions (link_id, id);
```

Notes:

- A variable's type is stored once, inside the pattern text. The API returns it parsed.
- Times are UTC in RFC 3339 form.
- Writes use SQLite's write-ahead log with full synchronous commits, so an acknowledged save survives a crash (NFR-7).

Later migrations, each arriving with the feature that needs it:

| Migration | Milestone | Adds |
|---|---|---|
| `0002_usage` | L5 | `usage_daily (link_id, route_id, day, hits)`; `links.last_used_at` |
| `0003_soft_delete` | L6 | `links.deleted_at` |
| `0004_accounts` | C1 | `sessions`, `link_coowners`, `users.last_active_on` |
| `0005_misses` | C2 | `miss_daily (name, day, misses, rescued)` |

Adding tables only when they are needed means the migration path is exercised several times during the local track, on the owner's real data, before a team depends on it.

### 3.5 API

All endpoints live under `/-/api/v1`.

| Endpoint | Purpose | From |
|---|---|---|
| `GET /meta` | Product identifier, version, API version | L1 |
| `GET /links?q=&limit=` | Ranked search, or a plain list when `q` is empty | L1, search in L3 |
| `POST /links` | Create | L1, full model in L2 |
| `GET`, `PATCH`, `DELETE /links/{id}` | Read, update, delete | L2 |
| `POST /links/validate` | Check a draft without saving | L2 |
| `GET /resolve?path=` | The resolver's decision, without redirecting | L2 |
| `GET /links/{id}/revisions`, `POST /links/{id}/revert` | History and revert | L5 |
| `GET /export`, `POST /import?dryRun=` | Export and import | L5 |
| `POST /backup` | Write a database copy; local mode only | L5 |
| `GET /suggest?q=` | Short result list for the extension | L6 |

A link, as returned by every endpoint:

```json
{
  "id": 12,
  "name": "gh",
  "description": "GitHub shortcuts",
  "owner": { "id": 1, "displayName": "Local user" },
  "version": 3,
  "canEdit": true,
  "routes": [
    {
      "id": 31,
      "pattern": "gh/{repo}/pr/{num:int}",
      "destination": "https://github.com/acme/{repo}/pull/{num}",
      "variables": [
        { "name": "repo", "type": "string", "accepts": "any text", "description": "", "example": "" },
        { "name": "num", "type": "int", "accepts": "a whole number", "description": "", "example": "" }
      ]
    }
  ],
  "usage": { "total": 0, "last30Days": 0, "lastUsedAt": null },
  "createdAt": "2026-10-13T09:00:00Z",
  "updatedAt": "2026-10-13T09:00:00Z"
}
```

`POST` and `PATCH` send `description` and `routes` (each with `pattern`, `destination`, optional `id`, and optional per-variable `description` and `example`). The array order is the route order. `PATCH` also sends `version`; a stale version answers `409`. The link's name is never sent: it is the first segment that all routes must share.

Validation, which drives the whole create and edit form:

```json
// POST /-/api/v1/links/validate
{ "linkId": null,
  "routes": [ { "pattern": "reports/{month:int}/{day",
                "destination": "https://reports.example.com/daily?month={month}&day={day}",
                "samples": { "month": ["10", "x"] } } ] }

// 200
{ "valid": false,
  "name": { "value": "reports", "available": true, "existing": null },
  "routes": [ {
    "tokens": [ { "kind": "literal", "start": 0, "end": 7 },
                { "kind": "variable", "start": 8, "end": 19, "name": "month", "type": "int" } ],
    "problems": [ { "severity": "error", "field": "pattern", "code": "pattern.unclosed_variable",
                    "start": 20, "end": 24, "message": "This variable is missing its closing }." } ],
    "samples": { "month": { "10": true, "x": false } } } ] }
```

The resolver's decision:

```json
// GET /-/api/v1/resolve?path=reports/10/13
{ "kind": "hit",
  "link": { "id": 4, "name": "reports", "description": "Daily reports" },
  "route": { "id": 9, "pattern": "reports/{month:regex(^(0?[1-9]|1[0-2])$)}/{day:int}" },
  "values": { "month": "10", "day": "13" },
  "destination": "https://reports.example.com/daily?month=10&day=13" }

// GET /-/api/v1/resolve?path=reports/13/01
{ "kind": "miss_known",
  "link": { "id": 4, "name": "reports", "description": "Daily reports" },
  "routes": [ { "id": 9, "pattern": "reports/{month:regex(^(0?[1-9]|1[0-2])$)}/{day:int}",
                "reason": { "code": "value_rejected", "variable": "month", "value": "13",
                            "message": "month doesn't accept \"13\". It accepts text matching ^(0?[1-9]|1[0-2])$." } } ] }

// GET /-/api/v1/resolve?path=reprots/10/13
{ "kind": "miss_unknown",
  "requestedName": "reprots",
  "suggestions": [ { "name": "reports", "path": "reports/10/13", "edits": 1, "resolves": true,
                     "description": "Daily reports",
                     "destination": "https://reports.example.com/daily?month=10&day=13" } ] }
```

Every sentence shown to a user about why something failed comes from the server's `message` fields, so the wording has one source as well.

Errors use one shape, `{ "error": { "code": "…", "message": "…", "details": {} } }`, with `400` for malformed requests, `404`, `409` for name and version conflicts, `415` for a wrong content type, and `422` for a draft that fails validation (carrying the same `routes[].problems` as the validate endpoint).

Export files, frozen at the gate because they are the migration path to a deployed Directory:

```json
{ "format": "godir-export", "version": 1, "exportedAt": "2026-10-13T09:00:00Z",
  "links": [ { "name": "gh", "description": "GitHub shortcuts", "owner": "local",
               "routes": [ { "pattern": "gh/{repo}", "destination": "https://github.com/acme/{repo}",
                             "variables": { "repo": { "description": "", "example": "" } } } ] } ] }
```

### 3.6 Web UI

| Page | URL | From |
|---|---|---|
| Home: search, your links, recently added, most used | `/-/` | L3, most used in L5 |
| Link page | `/-/links/{name}` | L3 |
| Create | `/-/new`, optionally with `?pattern=` or `?destination=` | L3 |
| Edit | `/-/links/{name}/edit` | L3 |
| Miss page | Rendered at the missed URL | L3 |
| Setup | `/-/setup` | L3 |
| Settings: export, import, backups, recently deleted, version | `/-/settings` | L5, recently deleted in L6 |

| Component | Does | Talks to |
|---|---|---|
| Search | Queries as the user types, lists results, handles arrow keys and Enter | `GET /links?q=` |
| Route editor | Pattern field with highlighted tokens and an error marker; destination field; messages beside the field they concern | `POST /links/validate` |
| Variables table | Type selector and restriction editor per variable; rewrites the variable's span in the pattern | Offsets from the validate response |
| Regex tester | Sample values, marked pass or fail by the server's engine | `POST /links/validate` |
| Launcher | One field per variable; shows the resulting go link and final URL, or the reason; Open button | `GET /resolve` |

Rules for all browser code:

- No inline script or style; the content security policy forbids both.
- Untrusted text is inserted with `textContent` or DOM methods, never `innerHTML`. `make check` fails if `innerHTML` or `insertAdjacentHTML` appears in `internal/web/static` or `extension/`.
- Calls made while typing are debounced by 200 ms, cancel the previous call, and ignore answers that arrive out of order.
- Reading pages — home, link page, miss page — are rendered by the server and work without JavaScript. Creating and editing require it.
- Static files are served under a prefix derived from a hash of their content, with a long cache lifetime. `--dev` serves templates and static files from disk, so editing them needs no restart.

Pattern highlighting (VAR-2) uses a monospace input with transparent text over a mirror element that renders the tokens the server returned. If that proves unreliable in a browser listed in NFR-20, the fallback is to render the tokens directly beneath the field with the error marker under the offending character; both satisfy the requirement.

### 3.7 Extension

```text
extension/
├── manifest.json
├── background.js          Service worker: installs the redirect rule, opens the first-run
│                          page, replaces failed Directory tabs with the unreachable page
├── lib/config.js          Directory URL: validate, store, build the redirect rule
├── lib/api.js             Calls to the Directory with a timeout
├── options/               Settings page (L4)
├── onboarding/            First-run page (L4)
├── unreachable/           Shown when the Directory doesn't answer (L5)
├── popup/                 Search and "create a go link for this page" (L6)
└── icons/
```

The redirect is one dynamic `declarativeNetRequest` rule, rewritten whenever the Directory URL changes:

```js
{ id: 1, priority: 1,
  condition: { regexFilter: "^https?://go/(.*)$", resourceTypes: ["main_frame"] },
  action: { type: "redirect", redirect: { regexSubstitution: directoryOrigin + "/\\1" } } }
```

Dynamic rules persist across restarts and apply without the service worker running (NFR-4).

| Permission | Purpose | From |
|---|---|---|
| `declarativeNetRequestWithHostAccess` | The redirect rule, with no access beyond the hosts below | L1 |
| Host `*://go/*` | Lets the rule act on go links | L1 |
| Host `http://localhost:4655/*` | Reach the default Directory for the connection test | L1 |
| `storage` | Remember the Directory URL | L4 |
| `webRequest` | Observe go navigations for the first-run check and failed Directory requests for the unreachable page. Observation only, and only for the hosts above | L4 |
| Optional host access, requested for one origin when the user saves a different Directory URL | A Directory that isn't at the default address | L4 |
| `activeTab` | Read the current tab's URL when the user clicks "Create a go link for this page" | L6 |

The manifest must declare broad optional host patterns (`http://*/*`, `https://*/*`) because the Directory's address isn't known in advance. Chrome grants only the one origin the user confirms. The `onErrorOccurred` listener of L5.3 is meant to be registered once with that same broad filter and to compare the request's origin with the configured Directory in its handler. The L0 spike supports part of this: in headless Chromium, an extension with that filter and static host access to one origin received the event for that origin only. Whether a listener registered before the user grants an optional origin then receives that origin's events was not tried; L5.3 must test it.

### 3.8 Safety in local mode

A local Directory has no sign-in, yet any web page open in the user's browser can send requests to `localhost`. Without guards, a hostile page could silently re-point `go/bank` at a phishing site. These guards are part of L1:

| Threat | Guard |
|---|---|
| Another machine on the network | Listen on loopback only. With sign-in off, refuse to start on any other address unless `--allow-unauthenticated-network` is given (DEP-3) |
| DNS rebinding: a hostile domain that resolves to `127.0.0.1` | Serve a request only when its `Host` is `localhost`, `127.0.0.1`, or `[::1]` with the configured port, or a host added with `--allow-host` |
| A web page writing across sites | A write must carry `Content-Type: application/json`, which a cross-site form cannot send without a preflight that is never approved. An `Origin` header, when present, must be the Directory's own; `Sec-Fetch-Site`, when present, must be `same-origin` or `none` |
| A web page reading across sites | The Directory never sends CORS headers |
| Script injection through a name, description, or path | Escaping templates, the `innerHTML` ban, and the content security policy (NFR-11) |

`GET` requests never change state, apart from counting a use.

The write guards apply under `/-/` only, never to the resolver. In the L0 spike (check 4) a go link clicked on the Directory's own page arrived with `Sec-Fetch-Site: cross-site`, in headless Chromium and by hand on Chrome stable. In headless Chromium, a form on a plain-HTTP page of another site that posts to `http://go/…` arrived as a `POST` carrying that site's `Origin`; the same form on an HTTPS page did not arrive (Appendix B). The resolver refuses such a `POST` because it accepts `GET` and `HEAD` only (section 3.3).

### 3.9 Local runtime

| Flag | Environment | Default | Meaning |
|---|---|---|---|
| `--addr` | `GODIR_ADDR` | `localhost:4655`, both loopback addresses | Where to listen |
| `--db` | `GODIR_DB` | `godir.db` in the data directory | Database file |
| `--allow-host` | `GODIR_ALLOW_HOSTS` | none | Extra `Host` values to accept |
| `--log-level` | `GODIR_LOG_LEVEL` | `info` | |
| `--dev` | — | off | Serve templates and static files from disk |

The data directory is `~/Library/Application Support/godir` on macOS, `$XDG_DATA_HOME/godir` (or `~/.local/share/godir`) on Linux, and `%LocalAppData%\godir` on Windows. It holds `godir.db`, `backups/`, and `logs/`. The database lives there and not in the repository checkout, so pulling or deleting the source never touches a user's links.

### 3.10 What the local track builds so the cloud track needs no rewrite

| Built during the local track | Deliberately not built before the gate |
|---|---|
| Every use case takes an `Actor`; a `CanEdit` rule exists and is called, though the local admin always passes | Sign-in, sessions, CSRF tokens, rate limits |
| Each link stores and returns its owner; the UI shows it | Co-owners, transfer, roles, and user screens |
| A `Store` interface with a conformance test suite any implementation must pass | Postgres |
| All URL building goes through one configured base URL; nothing assumes `localhost` | Proxy and TLS handling |
| A sign-in mode setting with one value, `none`, which refuses non-loopback addresses | OpenID Connect |
| A versioned export format, frozen at the gate | Container image, metrics, multi-instance invalidation |

## 4. Testing and quality

| Layer | What it proves | Tool | Runs |
|---|---|---|---|
| Spec fixtures | PRD section 5 behaves as its examples say | Go table tests over `spec.json`, through the Go API and through HTTP | `make check` |
| Unit | Parser, types, templates, matching, distance, ranking, configuration, migrations | Go `testing` | `make check` |
| Fuzz | The parser never panics; canonical text re-parses to the same pattern; no value can change a destination's scheme or host | Go fuzzing | 10 seconds each in `make check`; 5 minutes each before a tag |
| HTTP | Status codes, headers, request guards, escaping, API shapes | `net/http/httptest` | `make check` |
| Extension logic | URL validation and rule building | `node --test` | `make check` |
| Type check | All browser and extension code | `tsc --noEmit` | `make check` |
| End to end | The redirect through the real extension; PRD scenarios 1–8; the unreachable page | Playwright with its bundled Chromium | `make check` |
| Accessibility | No serious or critical issue on the main pages (NFR-21) | axe, run by Playwright | `make check` |
| Performance | NFR-1, NFR-2, NFR-3, NFR-5 with 100,000 links | Go benchmarks and a load script | Before each tag |
| Manual | Address-bar behavior, permission prompts, the service after a reboot | `docs/manual-tests.md` | Before each tag |

Two things automated tests cannot cover, which is why the manual list exists: Playwright cannot type into Chrome's address bar, and Chrome-branded builds no longer accept the `--load-extension` flag that test tools rely on (Chromium still does).

A task is done when its code and tests land together, `make check` passes, any behavior a user sees is described in the README or on the setup page, and the commit names the PRD requirements it advances.

`make check` is the gate before every commit. When the repository gets a remote, a workflow runs the same command on Linux, with Chromium under a virtual display.

## 5. Local track

Each milestone lists what it **completes** (requirements fully met at its end) and, where relevant, what it **starts**.

### L0 — Spike

**Goal.** Confirm in a real browser the Chrome behaviors the design rests on, before any product code exists.

**Status, 2026-10-13: done.** All checks have a recorded result and none failed, so no fallback of Appendix B applies. Checks 1 to 6, 8 to 15, and 17 matched their expected outcome by hand on Chrome 155 stable, and check 16 was recorded there (Chrome's error page). Two were run in a narrower form than Appendix B words them: check 4 with host access to `go` and the Directory, and check 5's hand-off from another app as a command-line hand-off to the test profile. Check 7 rests on the automated run in headless Chromium 156, with a resolver switch standing in for a hosts entry. The stack (Go) and the address-bar keyword (`go/`) are confirmed. The working name stays a placeholder (Q8), and port 4655 was free in every run. Name matching (Q5) is decided: names ignore case only, and `-` and `_` are significant. Within check 5 and check 14, the go link opened with Chrome closed was reported as working by the owner but was not seen in the stub's log. Results are in [spike-chrome.md](spike-chrome.md).

- **L0.1 Throwaway extension and stub.** A minimal Manifest V3 extension with the rule of section 3.7, and a 30-line Go server on port 4655 that logs each request and answers `302` or `404`. Both live in `spike/` and are deleted when L1 starts.
- **L0.2 Run the checklist** of Appendix B on current Chrome stable on macOS, in a fresh profile. Record the Chrome version, the date, and what was observed for each check in `docs/spike-chrome.md`.
- **L0.3 Apply the fallbacks.** For each check that fails, take the "if it fails" action in Appendix B and update this plan and PRD Appendix B.
- **L0.4 Confirm section 2**: stack, name matching, working name, port, and the address-bar keyword for L6.8.

**Done when** every check has a recorded result, and every failed check has a workaround written into this plan.

### L1 — Walking skeleton

**Goal.** The thinnest end-to-end slice on the final architecture: persistence, migrations, routing, request guards, the redirect rule, and the test harness.

**You can now** create `roadmap` with `curl`, type `go/roadmap` in Chrome, and land on its destination — and it still works after a restart.

**Completes:** RES-3, RES-6, EXT-1, AUTH-1, DEP-1, DEP-3, DEP-6, NFR-4, NFR-7. **Starts:** RES-1, API-1.

- **L1.1 Repository.** `git init`; the layout of section 3.1; `go.mod`; a `Makefile` with `run`, `test`, and `check`; `package.json` with `typescript`, `@types/chrome`, `@playwright/test`, and `@axe-core/playwright`; `.gitignore`; `gofmt`, `go vet`, and `staticcheck`.
- **L1.2 Configuration.** The flags and environment variables of section 3.9; the per-OS data directory; `godir version`; the refusal to listen beyond loopback while sign-in is off.
- **L1.3 Store and migrations.** Open SQLite with the write-ahead log, foreign keys on, and full synchronous commits. A migration runner over embedded `NNNN_name.sql` files, each applied in a transaction and recorded in `schema_migrations`. Before migrating an existing database, copy the file aside. Apply `0001_init.sql` and insert the built-in local user.
- **L1.4 Link model, first cut.** Lowercasing of names (the only normalization, per Q5), literal-only patterns, and a destination that must be an absolute `http` or `https` URL. The package's types are final; L2 completes the rules.
- **L1.5 Index.** Load every link into a snapshot keyed by lowercase name at startup; swap in a new snapshot after each committed write.
- **L1.6 HTTP server.** Listeners on both loopback addresses; the routing of section 3.3; the guards of section 3.8; security headers; graceful shutdown. When the port is taken, say whether another `godir` holds it (by asking its `meta` endpoint) or some other program.
- **L1.7 Endpoints.** `GET /-/api/v1/meta`, `GET /-/healthz`, `POST` and `GET /-/api/v1/links`, the resolver answering `302` or a bare `404`, and a placeholder page at `/-/`.
- **L1.8 Extension skeleton.** `manifest.json` and a service worker that installs the redirect rule for the default Directory URL on install and on browser startup. No pages yet.
- **L1.9 Tests.** Store and migration tests on temporary databases. HTTP tests for the redirect's status and headers and for each request guard. One Playwright test that starts the server, loads the extension, opens `http://go/x`, and asserts the browser lands on a fixture destination.
- **L1.10 `make check`** as described in section 4.

**Done when** the "you can now" works by hand, `make check` passes, and killing the process and restarting it loses no saved link.

### L2 — Link model and resolver core

**Goal.** PRD section 5 implemented once, in `linkmodel`, and proven by the PRD's own examples.

**You can now** create links with typed variables and several routes through the API, and get the right redirect, miss kind, reasons, and suggestions for any path from `GET /-/api/v1/resolve`.

**Completes:** VAR-1, RES-1, RES-4, RES-7, NFR-8, NFR-9, NFR-10, NFR-17, NFR-22. **Starts:** DIR-2, RES-2, MISS-1, MISS-2.

- **L2.1 Fixtures first.** Transcribe PRD 5.7 into `spec.json`: the links, every request with its expected outcome, and every save attempt. Mark request 18 as pending RES-8. Write the test that replays them; it fails until L2.9.
- **L2.2 Pattern parser.** A tokenizer, not a split on `/`: inside `regex(` it follows nested parentheses, escapes, and character classes (PRD 5.2). It produces segments with source offsets, reports problems as a code, a message, and a start and end offset, and enforces the character rules, the literal first segment, unique variable names, and the limits. It also provides the canonical text form, the shape, and the lowercasing of names.
- **L2.3 Types.** A small interface — does it accept this value, how is it described to a user, is it restricted, how is it written — implemented for `string`, `int`, `number`, and `regex`. A regex is compiled by Go's `regexp`, wrapped as `^(?:…)$`, and limited to 200 characters. L6 adds types through the same interface.
- **L2.4 Destination templates.** Validate per PRD 5.4: absolute `http` or `https`, at most 2,048 characters, no placeholder in the scheme or authority (checked on the text, before URL parsing), every placeholder declared, unused variables as a warning, not a go link and not a URL on the Directory. Record whether each placeholder sits in the path, the query, or the fragment. Expand by percent-encoding everything except unreserved characters. After expanding, assert that the scheme and host equal the template's; a mismatch is a logged bug and a `500`, never a redirect.
- **L2.5 Request splitting.** PRD 5.5 step 1 on the raw path: drop the query and fragment, split on `/`, discard empty segments, decode each segment once. A malformed escape or invalid UTF-8 is a bad request.
- **L2.6 Matching.** PRD 5.5 steps 2–4: candidate routes, acceptance per segment, specificity compared from the left (literal, then restricted variable, then text variable, then `path`), then route order. A variable whose placeholder sits in the destination's path rejects `.` and `..` (NFR-10). Every route that doesn't match yields a reason — wrong segment count, literal mismatch, or value rejected — with the variable, the value, what the variable accepts, and a sentence ready to show.
- **L2.7 Save rules.** A name unique ignoring case; all routes of a link share one name; a unique shape within the link; at most 20 routes; the reserved names of section 3.3.
- **L2.8 Suggestions.** Damerau–Levenshtein distance (the optimal-string-alignment form), stopping early once the allowed distance is exceeded. Thresholds of 0, 1, and 2 edits by the length of the requested name. Scan all names; for each candidate, check whether its name plus the rest of the request resolves. Order by edits, then name; return at most five. Thresholds and the limit are configuration.
- **L2.9 `Resolve`.** One function from a snapshot and a raw path to a decision: home, hit, unknown-name miss, known-name miss, or bad request.
- **L2.10 Use cases and persistence.** Create, update, and delete with the full model. Optimistic concurrency on `links.version`. A revision snapshot on every change — history is captured from now on, though its screen arrives in L5. Snapshot swap after commit. Renaming changes the first segment of every route.
- **L2.11 API.** `POST /links/validate`, `GET /resolve`, and full create, read, update, delete on `/links`, with the shapes of section 3.5. Creating a link whose name exists answers `409` with the existing link and whether the caller may edit it (DIR-4).
- **L2.12 Resolver handler.** Renders the decision as `302`, `404` (a plain page until L3), or `400`. Request logs name the matched link, never the raw path (NFR-17).
- **L2.13 Robustness.** The fuzz targets of section 4. Benchmarks with 100,000 generated links for a hit, an unknown-name miss, and a known-name miss.

**Done when** every P0 fixture passes through the Go API and through HTTP; each fuzz target runs clean for 5 minutes; and the benchmarks show a hit under 1 ms and an unknown-name miss under 50 ms in process, leaving ample room inside NFR-1 and NFR-2.

### L3 — Web UI

**Goal.** Everything a person does in the browser works against the full Directory URL, with or without the extension.

**You can now** carry out PRD scenarios 1 to 7 at `http://localhost:4655`.

**Completes:** DIR-1, DIR-2, DIR-3, DIR-4, DIR-6, DIR-7, VAR-2, VAR-3, VAR-4, VAR-5, SRCH-1, SRCH-2, SRCH-3, SRCH-4, MISS-1, MISS-2, MISS-3, RES-2, RES-5, API-1, NFR-11, NFR-20, NFR-21. **Starts:** DIR-5.

- **L3.1 Foundation.** Layout template; stylesheet with color tokens for light and dark; static files under a content-hash prefix; `--dev` mode; the content security policy. Focus styles, labels, and landmarks from the first page on.
- **L3.2 Search.** The ranking of SRCH-2, computed in the index: exact name, name prefix, misspelled name (the distance of L2.8), then substring matches in descriptions, destinations, and owners. Unit tests pin the order.
- **L3.3 Home.** Search box focused on load. "Your links" and "Recently added" rendered by the server. The search component lists results with variables highlighted and opens links without variables directly.
- **L3.4 Link page.** Description, owner, routes with destinations, variables with types, buttons that copy `go/…` and the full URL, the launcher, and Edit and Delete.
- **L3.5 Launcher.** One field per variable of the chosen route. Each debounced keystroke asks `GET /resolve` and shows the resulting go link and final URL, or the server's reason. Open is enabled only on a hit.
- **L3.6 Create and edit form.** Route rows with add, remove, move up, and move down; a description. The validate endpoint drives the rest: token highlighting, an error marker at the reported character, messages beside the field concerned, name availability, and conflicts that link to the existing link. The variables table shows a type selector per variable and, for a regex, an expression field with the sample tester. Changing a row rewrites that variable in the pattern; editing the pattern refreshes the table. The launcher serves as the live preview. The form opens prefilled from `?pattern=` and `?destination=`. When the name belongs to a link the user may edit, it offers to add the route there. It reports a version conflict when the link changed in another tab.
- **L3.7 Delete.** A confirmation that names the link. From L5.6 it also shows when the link was last used.
- **L3.8 Miss page.** Rendered by the server from the decision, at the missed URL, with status `404`, working without JavaScript. For an unknown name: up to five suggestions with description and destination, a "Create `go/<typed path>`" button, and a search box holding the name. For a known name: the link's description and each route with the reason it didn't fit.
- **L3.9 Setup page.** How to load the extension, how to check that it works, what to do when Chrome searches instead of navigating, that in Incognito, where the extension is not allowed by default, a typed go link ended on Chrome's error page (L0, check 16), and how to allow the extension there (that this makes go links work in Incognito was not tried in L0), and the extension-free alternative: a browser search shortcut pointing at `http://localhost:4655/%s`.
- **L3.10 Reserved paths** of section 3.3, answered directly and never shown as misses.
- **L3.11 Tests.** Page tests with hostile names, descriptions, and paths to prove escaping. Playwright flows for scenarios 1–7. The accessibility scan on home, form, link page, and miss page. A pass at a width of 375 pixels on the miss and link pages.

**Done when** scenarios 1–7 pass as Playwright tests with the content security policy enforced and no violation reported, and the accessibility scan finds no serious or critical issue.

### L4 — Extension and local MVP

**Goal.** Finish the extension's P0 pages, then prove the whole P0 set on a clean machine. This closes the PRD's M1.

**Completes:** EXT-2, EXT-3, EXT-4, DEP-2, NFR-1, NFR-2, NFR-3, NFR-5, NFR-18. **Starts:** NFR-19.

- **L4.1 Settings logic.** Validate a Directory URL — `http` or `https`, no credentials, no path — and reduce it to its origin; build the rule; store the URL in `chrome.storage.local`. Unit-tested with `node --test`.
- **L4.2 Settings page.** A URL field defaulting to `http://localhost:4655`. "Test connection" fetches the `meta` endpoint and checks the product identifier and API version. Save requests host access for the new origin, replaces the redirect rule in a single call, and releases access to the old origin. A malformed URL cannot be saved; a well-formed one that doesn't answer can, after a warning.
- **L4.3 First-run page.** Opens on install. It shows whether the Directory answers, and how to start it if not. It asks the user to type `go/` and press Enter, and confirms by itself when it sees the navigation (check 13 of Appendix B holds: on Chrome stable the extension saw typed go navigations). Then it has the user try a link without a trailing slash and explains the fallback. Two findings of L0 shape this page. It asks for `go/` only once the Directory answers: in headless Chromium, a visit to `http://go/` made with the "typed" transition while the Directory was down left no `go` entry in history, and `chrome://omnibox` still reported a search for `go/foo` (check 3, proxy evidence; not repeated by hand). It accepts `https://go/` as well as `http://go/` as the navigation it waits for: in headless Chromium with "Always use secure connections" at its strict level, the address was rewritten to `https://go/` before the extension saw it (check 6). By hand on Chrome stable the strict level was only shown to reach the Directory; the scheme the extension saw there was not recorded.
- **L4.4 Permission audit.** The manifest holds exactly the permissions of section 3.7. `extension/README.md` states the purpose of each. No content scripts, no remotely loaded code, no telemetry (NFR-18).
- **L4.5 Extension tests.** Playwright: path, query, and percent-encoding survive the redirect; changing the Directory URL re-points the rule, checked with two test servers; the first-run page renders. The permission prompt for a custom URL belongs to the manual list. So does start-up behavior: with `--load-extension`, Chromium fires `onInstalled` (reason `install`) on every launch and never `onStartup` (L0 spike, check 14), so in these tests the first-run page opens in every session and the `onStartup` path is never exercised.
- **L4.6 README.** Prerequisites, the one command, loading the extension, the first link, troubleshooting. Timed on a clean macOS user account by someone new to the project: five minutes or less.
- **L4.7 Performance run.** Generate 100,000 links. Over real HTTP, measure server time at the 95th percentile for hits, miss pages, and search, plus memory use and startup time. Record the results in `docs/perf.md` against NFR-1, NFR-2, NFR-3, and NFR-5.
- **L4.8 Manual browser pass.** Write `docs/manual-tests.md` — the address-bar checks no automated test can perform — and run it on Chrome stable on macOS.
- **L4.9 Tag `v0.1.0`.**

**Done when** every P0 requirement in Appendix A is met and the PRD's M1 criteria hold: the P0 fixtures pass, and the timed README run succeeds.

### L5 — Daily driver

**Goal.** A person can depend on the local product every day without thinking about it.

**You can now** reboot and have `go/…` work without opening a terminal, see a helpful page when the Directory is down, restore yesterday's data, move links between machines, see what you use, and undo a bad edit.

**Completes:** DIR-5, VAR-6, MISS-4, EXT-5, INS-1, INS-2, INS-3, DEP-10, NFR-16.

- **L5.1 Background service.** `godir service install`, `uninstall`, `status`, `restart`, and `logs`. On macOS, a LaunchAgent that runs at login and restarts the process if it exits. On Linux, a systemd user unit. On Windows, a documented Task Scheduler command. The service runs the binary that executed `install`, and refuses when that binary is a `go run` temporary file. Log files rotate at a size cap.
- **L5.2 Install path.** `make install`, and a README section "Use it every day": install, `godir service install`, done.
- **L5.3 Unreachable page.** The service worker watches for failed top-level requests to the Directory's origin and replaces the tab with an extension page. The page names the configured URL, offers Retry and Settings, and — for a local URL — shows the command that starts the Directory. A failed Retry must not loop. From the L0 spike (check 12, headless Chromium only): when `onErrorOccurred` fired, `chrome.tabs.get` never gave a `pendingUrl`. It gave a `url` only when the tab was already showing the extension's own unreachable page (the failed Retry and the Back case); for a tab coming from a page the extension may not read it gave neither. So the handler cannot check that the tab still shows the failed address; the spike skipped `net::ERR_ABORTED` and non-active documents instead. Pressing Back on the unreachable page behaved differently in the two runs: headless Chromium returned to the failed address and landed on the unreachable page again, while Chrome 155 stable, by hand, returned to the page the tab showed before. Test Back on Chrome stable and keep that behavior.
- **L5.4 Export and import.** `GET /export` returns the document of section 3.5. `POST /import?dryRun=true` reports what would be created, skipped as conflicting, or rejected as invalid, with reasons; without `dryRun`, it applies the same plan in one transaction. Buttons on the settings page; `godir export` and `godir import` on the command line. A round-trip test: export, empty the database, import, compare.
- **L5.5 Backups.** A daily copy of the database made with `VACUUM INTO`, keeping the last seven. `godir backup <file>` on demand. `godir restore <file>` with the server stopped. Restore steps in the README.
- **L5.6 Usage counts.** Migration `0002`. Hits are counted in memory per route and written every 10 seconds and at shutdown. The link page shows totals and last use; the home page gains "Most used"; recent use breaks ties in search and orders suggestions; the delete confirmation shows last use. Nothing per user and nothing per value is stored (NFR-16, NFR-17).
- **L5.7 History and revert.** The link page lists revisions with what changed in each. Any revision can be viewed and restored; restoring writes a new revision and validates against the Directory as it is now.
- **L5.8 Variable descriptions and examples.** Editable in the variables table. An example must satisfy its variable's type. Both appear in the launcher, in search results, and in usage help, and examples prefill the form's preview.
- **L5.9 Keyboard-first miss page.** The first suggestion has focus, Enter follows it, and the digits 1–5 choose.
- **L5.10 Sample links.** `godir seed` adds a handful of links to public sites that show each variable type, and only into an empty Directory.
- **L5.11 Diagnostics.** `godir doctor` reports the address and whether the server answers, the database path and its integrity check, the schema version, the service's state, and the age of the last backup.
- **L5.12 Upgrade safety.** A `v0.1.0` database fixture migrates cleanly in tests. The `meta` endpoint reports the API version, and the extension warns when it doesn't support it.
- **L5.13 Tag `v0.2.0`.**

**Done when** a typed go link works after a reboot with no manual step; stopping the service produces the unreachable page; the export–import round trip is lossless; a revert brings back an earlier destination; and yesterday's backup exists and restores.

### L6 — Power features

**Goal.** The remaining features a single user would notice the absence of.

**Completes:** DIR-8, DIR-9, VAR-7, VAR-8, VAR-9, SRCH-7, RES-8, RES-10, MISS-6, MISS-7, EXT-7, EXT-8.

The tasks are independent of each other and can be done in any order.

- **L6.1 More types.** Integer ranges, `enum`, and `path` with `{+name}` expansion. `path` is allowed only as the last segment, ranks below a text variable, captures one or more segments, and rejects dot segments. The variables table gains bounds and value-list editors. New fixtures cover each type and the precedence between `path` and longer routes. The overlap warning of PRD 5.5 arrives with them.
- **L6.2 Query passthrough.** The request's parameters are appended unless the template already sets that key, and are placed before any fragment. Fixture 18 is switched on.
- **L6.3 Preview.** A one-segment request that ends in `+`, whose stem is a link, opens that link's page.
- **L6.4 Test box.** The validate endpoint accepts test paths and evaluates them against the unsaved draft. The form shows which route wins and why, or why the path misses.
- **L6.5 Destination autocomplete.** Typing `{` in a destination offers the route's variables.
- **L6.6 Richer misses.** The launcher on usage help, prefilled with the values that were valid. Prefix suggestions for requested names of three or more characters. "Did you mean" for a typo in a later literal segment.
- **L6.7 Popup.** Search the Directory from the toolbar. "Create a go link for this page" opens the creation form with the tab's URL. `GET /suggest` serves this and L6.8.
- **L6.8 Address-bar suggestions.** The keyword chosen in L0; suggestions as the user types; Enter opens the selection.
- **L6.9 Keyboard shortcuts.** `/` focuses search, arrow keys move, Enter opens, `c` starts a new link, `?` lists the shortcuts.
- **L6.10 Duplicate hint.** The validate endpoint reports links that already point at the same destination.
- **L6.11 Restore deleted links.** Migration `0003`. Deleting marks the link deleted and reserves its name for 30 days. The settings page lists recently deleted links with Restore and Delete permanently, and creating a link under a reserved name offers the same two choices. A daily job purges what has expired.
- **L6.12 Tag `v1.0.0`.**

**Done when** each task's fixtures and Playwright checks pass and `make check` is green.

## 6. Gate: local release

Cloud work starts when all of these hold:

1. Every requirement assigned to L1–L6 in Appendix A is met.
2. The owner has used the product as their only go-link setup for ten working days, with the service installed, without losing data and without restarting it by hand. Every problem found is fixed or deliberately deferred.
3. The README's five-minute path passes again on a clean machine.
4. A database created by `v0.1.0` upgrades to `v1.0.0` in an automated test.
5. The export format is frozen at version 1.

## 7. Cloud track

The cloud track is also built and verified on a laptop. Deployed mode runs on `localhost` in a container, against an OpenID Connect provider (Dex) started with Docker Compose. Only C3 touches a real host.

It needs the PRD's open questions answered: Q2 and Q3 before C1 ends, Q1 before C2.4, Q6 before C2.7.

### C1 — Multi-user

**Goal.** Sign-in, ownership, and roles, switched on by configuration.

**Completes:** AUTH-2, AUTH-3, AUTH-4, AUTH-5, AUTH-6, AUTH-7, DEP-4, EXT-6, NFR-12, NFR-13, NFR-15.

- **C1.1 Configuration.** Settings for the public base URL, the sign-in mode, the OpenID Connect issuer and client, allowed email domains, admin emails, and whether resolving requires sign-in. Startup fails with a message that names the missing or invalid setting. No configuration at all still means local mode.
- **C1.2 Sign-in.** The OpenID Connect authorization-code flow with PKCE, state, and nonce (`coreos/go-oidc` and `golang.org/x/oauth2`). The email-domain allow-list. An account is created at first sign-in; admins come from configuration.
- **C1.3 Sessions.** Migration `0004`. Session identifiers stored hashed. A cookie with the `__Host-` prefix, `Secure`, `HttpOnly`, and `SameSite=Lax`. Expiry, a fresh session at sign-in, sign-out.
- **C1.4 Return after sign-in.** An unauthenticated go link is remembered through the sign-in round trip and resolved afterwards. Only relative paths on the Directory are accepted as return targets.
- **C1.5 Access rules.** `CanEdit` becomes real: owner, co-owners, admins. Reading and resolving require sign-in according to configuration; the API answers `401` as JSON, and pages redirect to sign-in.
- **C1.6 Ownership screens.** Co-owners, transfer, role management, and a user list for admins. The "owned by X" message of DIR-4 now has a second user to show.
- **C1.7 Write protection.** A per-session CSRF token on top of the guards of section 3.8 (NFR-12). Rate limits on sign-in, link creation, and API writes, keyed by user or client address, with a setting for trusted proxies (NFR-13).
- **C1.8 Attribution.** Ownership changes join the revision history; admins get a view of recent changes across all links (NFR-15). A per-user last-active date is recorded, and nothing finer (NFR-16).
- **C1.9 Extension.** Warn when the Directory URL is unencrypted and not local (EXT-6). The popup and address-bar suggestions show "Sign in" when the API answers `401`.
- **C1.10 Tests.** A fake OpenID Connect issuer inside the Go tests. A permission matrix test, role by action. Playwright with two users. A Docker Compose file that runs the Directory in deployed mode against Dex on `localhost`.

**Done when**, on a laptop, two users sign in through Dex, each can edit only their own links, an admin can edit both, and an unauthenticated `go/reports/10/13` lands on its destination after sign-in — while the local track's whole test suite still passes unchanged with no configuration.

### C2 — Deployable

**Goal.** An artifact and a set of procedures that someone can operate.

**Completes:** MISS-5, DEP-5, DEP-7, DEP-8, DEP-9, NFR-14, NFR-19.

- **C2.1 Container image.** A multi-stage build to a static binary on a minimal base image; a non-root user; a `/data` volume; a health check. The same image also runs local mode, for anyone who prefers Docker.
- **C2.2 Operations endpoints.** `/-/healthz` and `/-/readyz`. JSON logs with request identifiers and no raw paths. Metrics for requests, hits, misses, and latency on a separate listener that is not public.
- **C2.3 Behind TLS.** Forwarded-protocol headers are trusted only from configured proxies. Cookies and redirects are HTTPS-only, with HSTS. Secrets never reach the logs (NFR-14).
- **C2.4 Storage, per Q1.** By default, SQLite on a persistent volume with scheduled `VACUUM INTO` backups shipped off the host. If the host has no persistent disk, a Postgres implementation of the store interface that passes the same conformance tests.
- **C2.5 Runbook.** Backup, restore, upgrade, and rollback. Migrating a local Directory by export and import, including how imported links get their owners.
- **C2.6 Miss and rescue counts.** Migration `0005`. Suggestion links pass through a counting redirect. This yields the numbers for the PRD's success metrics.
- **C2.7 Extension package.** A versioned zip, a store listing with a justification for each permission and a privacy disclosure, published through the channel chosen in Q6. Before publishing, run the manual list on Windows, Linux, and ChromeOS and on the two previous Chrome versions (NFR-19).
- **C2.8 Security review.** Walk NFR-8 to NFR-15 with evidence for each; run `govulncheck`; check the response headers; fix what turns up.
- **C2.9 Load test.** 100,000 links at 200 resolutions per second in a container limited to 1 vCPU and 512 MB (NFR-5).

**Done when** `docker run` with a configuration file serves a signed-in Directory on `localhost` behind a TLS-terminating proxy, a restore from backup has been rehearsed, and the review has no open high-severity finding.

### C3 — First deployment

**Completes:** NFR-6.

- **C3.1 Provision** the host chosen in Q1, a DNS name, TLS, and the identity-provider client from Q2.
- **C3.2 Deploy** and run the manual list against the hosted URL.
- **C3.3 Migrate.** Export locally, import with a dry run, then import.
- **C3.4 Onboard the first team.** They install the extension from the chosen channel and change one setting.
- **C3.5 Watch for two weeks**: availability against NFR-6, the miss rate, the rescue rate. Triage what comes up.

**Done when** the PRD's M2 criterion holds: a deployed Directory with single sign-on is in daily use by a first team, whose links were migrated by export and import.

## 8. Backlog

Ordered after the team release, from what usage shows.

| Requirements | Note |
|---|---|
| SRCH-5, SRCH-6 | Tags and filters. Worth more once a Directory holds thousands of links |
| INS-4 | Admin reports. The miss counts of C2.6 already feed "most missed" |
| API-2, API-3 | Access tokens and an OpenAPI description |
| RES-9 | Serving hits while the database is down. Matters with Postgres |
| DEP-11 | Several instances serving one database |
| AUTH-8 | Sign-in through an identity header from a reverse proxy |
| DIR-10, VAR-10, VAR-11, EXT-9, EXT-10, EXT-11, EXT-12, AUTH-9, AUTH-10, INS-5 | The PRD's P3 list |

## 9. Risks and open points

| Risk | Effect | Response |
|---|---|---|
| A Chrome behavior in Appendix B doesn't hold | A design change in the extension | L0 comes before any product code, and each check has a fallback |
| Chrome's Local Network Access rules tighten | A go link clicked on a public page may prompt or fail when the Directory is on loopback | Check 11; rerun the manual list on each Chrome release during the local track; the fallback is to route clicked links through an extension page |
| Pattern highlighting misbehaves in some browser | L3.6 takes longer | The fallback of section 3.6 still meets VAR-2 |
| Browser code without a build step becomes hard to maintain | Slower UI work in L5 and L6 | Type-checked from day one; small components. Switching to TypeScript sources later is confined to `internal/web/static` and `extension/` |
| The pure-Go SQLite driver is slower than expected | NFR-3 or NFR-5 missed | The resolver never touches the database; L4.7 measures the rest; a cgo driver fits behind the same interface |
| Unpacked extensions need Chrome's developer mode | Friction for daily local use | Acceptable for the owner. If it grates, bring C2.7 forward — packaging doesn't depend on the rest of the cloud track |
| Autostart differs between launchd and systemd | The service doesn't come up after login on some machine | A reboot test per OS on the manual list; `godir doctor` |
| L5 and L6 delay the cloud track | A later team release | L6 is the cut line |

Open points that don't block L0–L4:

- The product's name (Q8). The rename list is in section 3.1.
- Where the repository is hosted. This decides the Go module path and when the CI workflow is wired.
- A license, if the code is to be shared.
- How far to support Windows for the local server. This plan documents a Task Scheduler command and builds nothing more.

## Appendix A. Requirement traceability

Each PRD requirement appears once, under the milestone that completes it.

| Milestone | Requirements |
|---|---|
| L1 | RES-3, RES-6, EXT-1, AUTH-1, DEP-1, DEP-3, DEP-6, NFR-4, NFR-7 |
| L2 | VAR-1, RES-1, RES-4, RES-7, NFR-8, NFR-9, NFR-10, NFR-17, NFR-22 |
| L3 | DIR-1, DIR-2, DIR-3, DIR-4, DIR-6, DIR-7, VAR-2, VAR-3, VAR-4, VAR-5, SRCH-1, SRCH-2, SRCH-3, SRCH-4, MISS-1, MISS-2, MISS-3, RES-2, RES-5, API-1, NFR-11, NFR-20, NFR-21 |
| L4 | EXT-2, EXT-3, EXT-4, DEP-2, NFR-1, NFR-2, NFR-3, NFR-5, NFR-18 |
| L5 | DIR-5, VAR-6, MISS-4, EXT-5, INS-1, INS-2, INS-3, DEP-10, NFR-16 |
| L6 | DIR-8, DIR-9, VAR-7, VAR-8, VAR-9, SRCH-7, RES-8, RES-10, MISS-6, MISS-7, EXT-7, EXT-8 |
| C1 | AUTH-2, AUTH-3, AUTH-4, AUTH-5, AUTH-6, AUTH-7, DEP-4, EXT-6, NFR-12, NFR-13, NFR-15 |
| C2 | MISS-5, DEP-5, DEP-7, DEP-8, DEP-9, NFR-14, NFR-19 |
| C3 | NFR-6 |
| Backlog | DIR-10, VAR-10, VAR-11, SRCH-5, SRCH-6, RES-9, EXT-9, EXT-10, EXT-11, EXT-12, AUTH-8, AUTH-9, AUTH-10, INS-4, INS-5, API-2, API-3, DEP-11 |

Notes on requirements that span milestones:

- SRCH-2, SRCH-3, and VAR-3 are complete in L3 as far as their own wording goes. The parts they tie to later features arrive with those features: the usage tie-break and the most-used list in L5.6, the range and value-list editors in L6.1.
- NFR-17 holds from L2 on. It is checked again whenever something new is recorded: usage in L5.6, misses in C2.6.
- NFR-16's per-user last-active date arrives in C1.8; the rest is L5.6.
- DEP-7's health endpoint and structured logs exist from L1; readiness and metrics complete it in C2.2.
- DEP-8's backups and export exist from L5; the runbook completes it in C2.5.

## Appendix B. Chrome spike checklist

Run in L0.2, on current Chrome stable, in a fresh profile, with the stub server on port 4655.

| # | Check | If it fails |
|---|---|---|
| 1 | Typing `go/` and pressing Enter reaches the stub | The design doesn't work as described. Stop, and redesign around the address-bar keyword |
| 2 | Before `go/` has ever been typed, type `go/foo`: record whether Chrome searches or navigates | A search is expected. It is the reason the first-run page exists |
| 3 | After `go/` has been typed once, `go/foo` navigates | Teach the trailing slash (`go/foo/`) on the first-run page, and build the opt-in search-rescue rule of PRD risk R1 in L4 |
| 4 | A link to `http://go/foo` on a third-party HTTPS page redirects, with host access to `go` and the Directory only, and none to the page the link is on | Request broader host access as an optional permission, document the reduced default, and revisit EXT-4 |
| 5 | The same from a bookmark, and from another app (`open http://go/foo`) | Document the limitation |
| 6 | `https://go/foo` redirects; so does `http://go/foo` with "Always use secure connections" switched on | Adjust the rule. If an interstitial can't be avoided, explain it on the first-run page |
| 7 | The redirect happens when `go` doesn't resolve, and when a hosts entry points `go` somewhere else | Document that the extension cannot override an existing `go` host on that network |
| 8 | `go/foo?x=1#frag` delivers `?x=1` to the stub, and `#frag` survives to the destination | A lost query means fixing the rule. A lost fragment is documented as a limitation |
| 9 | `go/a%20b%2Fc` reaches the stub byte for byte | Adapt L2.5 to what actually arrives |
| 10 | The address bar's autocomplete offers go links used before | Nothing to build. Leave the claim out of the documentation |
| 11 | Clicking a go link on a public page reaches the loopback Directory with no Local Network Access prompt or block | Route clicked links through an extension page, keeping the direct rule for typed navigations if those are unaffected |
| 12 | With `webRequest` and host access to the Directory's origin only, `onErrorOccurred` fires when the stub is stopped, and the tab can be replaced by an extension page | L5.3 falls back to checking the Directory from the service worker when a tab settles on its origin |
| 13 | `webRequest.onBeforeRequest` on `*://go/*` sees typed go navigations | The first-run page confirms by instruction, not automatically |
| 14 | The redirect works with the service worker stopped, and after a browser restart | Reinstall the rule on every startup event and investigate |
| 15 | Registering the address-bar keyword `go/` leaves a plain `go/foo` plus Enter unchanged | Choose a different keyword for L6.8 |
| 16 | In an Incognito window, with the extension not allowed there: record what happens (L0: Chrome's error page) | Document it on the setup page |
| 17 | The extension's own pages and service worker can fetch the Directory's `meta` endpoint on loopback with no Local Network Access prompt (added in L0) | Explain the prompt next to "Test connection" on the settings and first-run pages |

Results are in [spike-chrome.md](spike-chrome.md). One limitation found in L0 lies outside these checks, seen in headless Chromium 156 only (the run by hand had no form step): a form on an HTTPS page that submits to `http://go/…` stops at Chrome's "The information you're about to submit is not secure" warning, with or without the extension. "Send anyway" was not clicked.
