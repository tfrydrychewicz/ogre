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
| L4 Extension and MVP | Settings and first-run pages; the README gets a new user running in 5 minutes | P0 complete, except one clause of DIR-5 (L5.6); equals PRD M1 | M |
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

- Nothing in L1–L6 depends on the cloud track, and the cloud track changes no local behavior. After C3, `ogre serve` with no configuration behaves exactly as it did at the gate.
- Every milestone ends with a tagged, working product. From L4 on, the owner can use it for real.
- L5 is not optional. If cloud deployment becomes urgent, L6 is where to cut: its tasks are independent of each other and of the cloud track.
- After L1, the extension tasks of L4 (L4.1–L4.5) don't depend on L2 or L3 and can run alongside them.

This plan replaces the sequencing in PRD section 11. The PRD's M1 is L1–L4 here, its M2 is C1–C3, and part of its M3 moves forward into L5 and L6.

One P0 clause falls outside L1–L4. DIR-5's delete confirmation "shows how recently it was used", and that needs usage counts (INS-1, P1), which arrive in L5.6. So M1 is complete at the end of L4 except for that clause: until L5.6 the confirmation names the link but cannot say when it was last used. PRD DIR-5 says the same.

## 2. Decisions

### 2.1 PRD questions this plan assumes answered

| Question | Assumed answer | If the answer changes |
|---|---|---|
| Q4, stack | Decided: Go, SQLite, JavaScript for the browser side — the PRD's recommendation, refined in 2.2. The owner confirmed Go on 2026-10-13 (L0.4), after comparing it with Node.js | Sections 3 and 4 and the task wording change; the milestones and their order do not |
| Q5, name matching | Decided by the owner on 2026-10-13: names ignore case only. `-` and `_` are part of the name, so `on-call`, `on_call`, and `oncall` are different links | — |

Q1, Q2, Q3, and Q6 are only needed by the cloud track and stay open until then. Q8 is decided: the whole product is called Ogre (section 2.2).

### 2.2 Technical choices

| Topic | Choice | Reason |
|---|---|---|
| Server language | Go 1.26, the version installed on the development machine; standard-library HTTP server | RE2 regular expressions built in (NFR-8); one static binary |
| Database | SQLite through `modernc.org/sqlite`, a pure-Go driver | Nothing to install, no C compiler, cross-compiles. The store sits behind an interface, so Postgres can be added in C2 if Q1 requires it |
| Schema changes | Numbered SQL files embedded in the binary, applied at startup | DEP-6, with no external tool |
| HTML | `html/template`, rendered on the server | Automatic contextual escaping (NFR-11); miss pages work without JavaScript |
| Browser code | Plain JavaScript modules, for both the web UI and the extension; in the web UI they add behavior to pages the server renders. No custom elements, no framework, no bundler | No build step: `go run` serves the UI, and Chrome loads `extension/` as it is (DEP-1, DEP-2) |
| Types in JavaScript | JSDoc annotations checked by the TypeScript compiler (`tsc --noEmit`) | Type safety without compiling anything |
| Tests | Go's `testing` package, including fuzzing and benchmarks; `node --test` for extension logic; Playwright for end-to-end tests with the extension loaded | Section 4 |
| Logging | `log/slog` | Structured logs from the standard library |
| Names | The whole product is called **Ogre** (decided by the owner, 2026-10-13). Its two parts are the Ogre server, which the PRD calls the Directory, and the Ogre extension. The binary is `ogre`; the Go module path is `github.com/tfrydrychewicz/ogre`, which is also the repository on GitHub; the data directory is named `ogre` | Q8 is closed; section 3.1 says where the name lives |
| Default address | Listens on `127.0.0.1:4655` and `[::1]:4655`; the Directory URL is `http://localhost:4655` | DEP-1 and DEP-3. 4655 spells GOLK on a phone keypad, and nothing uses it on the development machine |

The product has one runtime dependency (the SQLite driver) until C1. Node.js is needed only to run checks and end-to-end tests, never to build or run the product.

### 2.3 Where this plan departs from or adds to the PRD

| # | Change | Why |
|---|---|---|
| 1 | The extension is written in type-checked JavaScript, not TypeScript source | A compile step would add Node.js to the five-minute setup (DEP-2) |
| 2 | Locally relevant P1 and P2 requirements ship before the team release | The local-first rule |
| 3 | Added: a background service that starts at login (`ogre service install`) | Without it, "usable locally" means keeping a terminal open |
| 4 | Added: a daily database backup, and a copy before every schema change | A personal Directory holds data that exists nowhere else |
| 5 | Added: request guards in local mode — a Host allow-list, a same-origin check on writes, and JSON-only writes | A server on localhost with no sign-in can be reached by any web page the user visits (section 3.8). The PRD asks for cross-site protection only when deployed (NFR-12) |
| 6 | Added: a short list of reserved names such as `favicon.ico` and `robots.txt` | Browsers request these paths unprompted. The PRD assumed `/-/` was the only reservation |
| 7 | Added: a request with malformed percent-encoding answers `400` instead of a miss | The PRD doesn't define the case |
| 8 | MISS-5 (rescue counting) moves from P1 to C2 | It feeds team metrics, not local use |
| 9 | Deleting is permanent until DIR-9 arrives in L6 | Reserving a deleted name without a restore screen would only confuse |
| 10 | Added: `ogre doctor`, a diagnostics command | Local problems have no operator to call |

## 3. Technical design

### 3.1 Repository layout

This is the layout at the local-release gate. At L1 these parts do not exist yet: the `service`, `export`, `import`, `backup`, `restore`, `seed`, and `doctor` commands (L5); `internal/linkmodel/testdata/spec.json` (L2.1); `internal/osservice/` (L5.1); `docs/manual-tests.md` (L4.8); `docs/perf.md`, `cmd/ogre-perf/`, and `internal/perf/` (L4.7); and the Makefile's `install` target (L5.2). `docs/` also holds `spike-results/`, the raw data of L0. Of the extension, only `manifest.json`, `background.js`, and `lib/config.js` exist at L1; section 3.7 has its layout.

```text
.
├── cmd/ogre/             Command line: serve (default), version, service, export, import,
│                         backup, restore, seed, doctor
├── cmd/ogre-perf/        The performance run of L4.7 (make perf); not part of the shipped binary
├── internal/
│   ├── linkmodel/        The rules of PRD section 5: parse, validate, match, expand, suggest.
│   │   └── testdata/     spec.json — the PRD 5.7 examples as fixtures
│   ├── store/            Store interface, SQLite implementation, migrations/*.sql
│   ├── index/            In-memory snapshot of compiled links; search
│   ├── app/              Use cases: resolve, validate, create, update, delete, search,
│   │                     export, import
│   ├── httpd/            Listeners, request guards, routing, resolver, JSON API, page handlers
│   ├── web/              Rendering, templates/, static/ (CSS and JavaScript), embedded
│   ├── config/           Flags, environment variables, data-directory rules
│   ├── perf/             Generates links, drives the real binary over HTTP, judges NFR-1/2/3/5
│   └── osservice/        launchd and systemd integration
├── extension/            Manifest V3 extension, loadable unpacked as it is
├── e2e/                  Playwright tests
├── docs/                 PRD.md, this plan, spike-chrome.md, manual-tests.md, perf.md
├── Makefile              run, test, check, e2e, e2e-browsers, perf, install
├── package.json          Development tools only
├── go.mod
└── README.md
```

Q8 is closed, so no rename is planned. For the record, the name lives in these places:

- the repository and the Go module path, which every import of an internal package repeats;
- `cmd/ogre` and `bin/ogre`, both named in the `Makefile` and in the end-to-end harness;
- the data-directory name and the database file name `ogre.db`;
- the `OGRE_` environment prefix;
- the `product` field of the `meta` endpoint, which the server itself and the end-to-end harness check to recognise a running server;
- what the command prints: its help text, the `ogre:` error prefix, the start and stop log lines, and the messages that name the program (such as `another ogre` and `start ogre with --allow-host`);
- the web UI's page titles, header, and footer, the setup page, and the `ogre.` prefix of the keys the browser code keeps in `sessionStorage`;
- the `name` and `description` in `package.json`, and the `name` in `package-lock.json`;
- the `ogre-e2e-` prefix of the tests' temporary directories;
- `extension/manifest.json` and the `[ogre]` log prefix of `extension/background.js`;
- the comments, and the tests that assert any of the above;
- the two READMEs, this plan, and Q8 in the PRD.

Two more arrive in L5: the service label and the `format` field of export files.

### 3.2 Components and their boundaries

- **`linkmodel` is pure.** It imports nothing else from the project and performs no I/O. Everything about PRD section 5 lives there and nowhere else (NFR-22). Browser code never parses a pattern; it shows what the API returns.
- **`app` is the only package that writes.** Every use case that reads or changes data on someone's behalf takes an `Actor` — who is acting; resolving does not. Local mode supplies the built-in admin user; C1 swaps in the signed-in user without touching the use cases.
- **`index` holds an immutable snapshot**, replaced atomically after each committed write. The resolver reads only the snapshot, so a hit never touches the database.
- **`httpd` and `web` contain no rules.** They translate HTTP into `app` calls and render the results. Even "may this user edit this link" is asked of `app` (`CanEdit`), and the save rules have one home, `linkmodel.ValidateDraft`.
- **`serve` binds the address before it opens the database**, so a second instance fails on the port without touching the data.

### 3.3 Request routing

The outermost handler decides in this order:

1. **Host check** (section 3.8). An unlisted `Host` gets `403`.
2. **`/`** redirects to `/-/` (RES-5).
3. **`/-/…`** goes to the web UI and API, routed by the standard `ServeMux`. A page of another site cannot load these as an image, a frame, or a fetch (section 3.8).
4. **Reserved paths** — `favicon.ico`, `robots.txt`, `sitemap.xml`, `apple-touch-icon*.png`, and anything under `.well-known/` — are answered directly. They are never treated as go links, and the names are rejected when a link is saved.
5. **Everything else** is a go link, resolved from the raw, still-encoded path (`r.URL.EscapedPath()`).

`ServeMux` is deliberately not used at the root: it cleans paths and redirects requests containing `//` or `..`, which would corrupt variable values before the link model sees them.

The resolver accepts `GET` and `HEAD` only and answers:

| Decision | Response |
|---|---|
| Hit | `302` with `Location` and `Cache-Control: no-store` (RES-3) |
| Miss, either kind | `404` with the miss page |
| Malformed encoding | `400`. A malformed percent-escape (`go/a%zzb`, `go/%`) never reaches the handler: Go's HTTP server answers it with its own plain `400` (L0 spike, check 9, headless Chromium). The handler's own `400`, with a short page, is left for paths that reach it and still fail L2.5, such as invalid UTF-8 after decoding; the spike had no such variant. Headers over 64 KiB get Go's own bare `431` the same way. Neither of Go's own answers carries the security headers |

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
- Writes use SQLite's write-ahead log with full synchronous commits, and `fullfsync` on macOS, so an acknowledged save survives a crash (NFR-7). A killed process is tested; power loss is designed for and not tested.
- The store refuses a link name that is not already lowercase; lowercasing is the link model's rule (`NormalizeName`), applied by `app` before it writes.

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
| `GET /meta` | Product identifier (`"product": "ogre"`), version, API version | L1 |
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

The create and edit form and the launcher ask for more through optional fields, added in L3. A request without them gets exactly the answer above.

- `"describeVariables": true` adds each route's `variables`: `name`, `type`, `accepts`, `expression`, and the `start` and `end` of the variable's span, braces included, which the variables table rewrites.
- `"explainSamples": true` adds each route's `sampleReasons`: for every refused sample, the sentence a miss page would show.
- A route's `expressions`, one regular expression per variable name, is checked on its own and answered in that route's `expressions` as `{ "valid", "code", "message" }`.
- `"preview": { "route": 0, "path": "reports/10/13" }` adds `preview`: what the draft, saved as it is, would do with that request. A `hit` carries `route`, `destination`, and `values`; a `miss_known` carries `reasons`, each with the index of its route; `bad_request` and `invalid` carry a `message`. Routes with errors take no part, and nothing is read from the store.

Search, `GET /links?q=`, answers `{ "links": [ … ] }`: links as above, best first, each with a `rank`, and each route with the `tokens` of its pattern so results can highlight variables. The ranks, best first, are `exact`, `prefix`, `misspelled`, `name`, `description`, `destination`, and `owner`. All but the first three mean that every word of the query occurs somewhere in the link, ranked by the best field one occurs in; `name` is a word found inside a name, as `status` in `platform-status`. A single word pasted as `go/reports/10/13` is searched as the name `reports`. `limit` defaults to 20 and is at most 100. Without `q` the endpoint lists every link, as in L2, and does not apply `limit`.

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

Errors use one shape, `{ "error": { "code": "…", "message": "…", "details": {} } }`, with `400` for malformed requests, `404`, `409` for name and version conflicts, `415` for a wrong content type, `413` for a body over 1 MiB, and `422` for a draft that fails validation. A `422` carries `details.routes`, one entry per route sent and in the same order, each with that route's `problems`, plus `details.problems` for those that belong to no single route. Every `start` and `end` in the API counts UTF-16 code units, as JavaScript does.

Export files, frozen at the gate because they are the migration path to a deployed Directory:

```json
{ "format": "ogre-export", "version": 1, "exportedAt": "2026-10-13T09:00:00Z",
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
| Launcher | One field per variable; shows the resulting go link and final URL, or the reason; Open button | `GET /resolve`; `POST /links/validate` for each value on its own and for the form's preview |

Rules for all browser code:

- No inline script or style; the content security policy forbids both. Every response carries `default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'; require-trusted-types-for 'script'; trusted-types 'none'` (NFR-11). The last two directives allow no Trusted Types policy, so a browser that knows them (Chromium) refuses every sink that turns a string into markup.
- Untrusted text is inserted with `textContent` or DOM methods, never as markup. `make check` (`dom-guard`) fails if any of these appears in `internal/web/static` or `extension/`: `innerHTML`, `outerHTML`, `insertAdjacentHTML`, `document.write`, `srcdoc`, `DOMParser`, `parseFromString`, `createContextualFragment`, `setHTMLUnsafe`, `parseHTMLUnsafe`.
- Calls made while typing are debounced by 200 ms, cancel the previous call, and ignore answers that arrive out of order.
- Reading pages — home, link page, miss page — are rendered by the server and work without JavaScript. Creating and editing require it.
- Static files are served under `/-/static/<hash>/`, with a long cache lifetime. One hash covers all of them, not one per file, because the modules import each other by relative path. `--dev` serves templates and static files from the source checkout, read on every request, so editing them needs no restart.
- Pages send `Referrer-Policy: same-origin`. A go link's address carries values, and a redirect to another site already gets no `Referer` under this policy. `no-referrer` would leak nothing more, but a browser then sends `Origin: null` on the Directory's own writes, which the write guard of section 3.8 refuses. Miss pages and the bad-request page make no writes; they send `no-referrer`, and `X-Robots-Tag: noindex`.

Pattern highlighting (VAR-2) uses a monospace input with transparent text over a mirror element that renders the tokens the server returned. If that proves unreliable in a browser listed in NFR-20, the fallback is to render the tokens directly beneath the field with the error marker under the offending character; both satisfy the requirement.

### 3.7 Extension

```text
extension/
├── manifest.json
├── background.js          Service worker: installs the redirect rule, opens the first-run
│                          page, changes the Directory URL, gives up unused host access;
│                          from L5, replaces failed Directory tabs with the unreachable page
├── lib/config.js          Directory URL: validate, normalize, build the redirect rule (no chrome.*)
├── lib/api.js             The call to the Directory's meta endpoint, with a timeout
├── lib/settings.js        Read the stored URL, ask for host access, ask the worker to change it
├── lib/page.js            Small DOM helpers the extension's pages share
├── shared/page.css        The pages' stylesheet: a copy of the web UI's color tokens and
│                          components from site.css, kept in step by hand (section 9)
├── options/               Settings page (L4), opens in a tab
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
| `storage` | Remember the Directory URL and whether the first run is done | L4 |
| `webRequest` | Observe go navigations for the first-run check (L4) and failed Directory requests for the unreachable page (L5). Observation only, and only for the hosts above. In L4 only the first-run page listens, and only while it is open; the service worker has no listener | L4 |
| Optional host access, requested for one origin when the user saves or tests a different Directory URL | A Directory that isn't at the default address. Access no saved Directory uses is given up on the next save, and when Chrome starts or the extension is installed or updated | L4 |
| `activeTab` | Read the current tab's URL when the user clicks "Create a go link for this page" | L6 |

The manifest must declare broad optional host patterns (`http://*/*`, `https://*/*`) because the Directory's address isn't known in advance. Chrome grants only the one origin the user confirms. The `onErrorOccurred` listener of L5.3 is meant to be registered once with that same broad filter and to compare the request's origin with the configured Directory in its handler. The L0 spike supports part of this: in headless Chromium, an extension with that filter and static host access to one origin received the event for that origin only. Whether a listener registered before the user grants an optional origin then receives that origin's events was not tried; L5.3 must test it.

As built in L4, the manifest also sets `minimum_chrome_version` to 116, for `runtime.getContexts`, which keeps the first-run page from opening a second time beside a copy already open. The extension keeps `directoryUrl`, `firstRunDone`, and `firstRunSawGo` in `chrome.storage.local`, and `installHandled` in `chrome.storage.session`, which only the tests read. The first-run page cannot tell a typed `go/` from one a web page in another tab opened: Chrome names the page that started a request only to an extension with access to that page. Telling them apart would take the `webNavigation` permission and its warning about browsing history, which a hint does not justify.

### 3.8 Safety in local mode

A local Directory has no sign-in, yet any web page open in the user's browser can send requests to `localhost`. Without guards, a hostile page could silently re-point `go/bank` at a phishing site. These guards are part of L1:

| Threat | Guard |
|---|---|
| Another machine on the network | Listen on loopback only. With sign-in off, refuse to start on any other address unless `--allow-unauthenticated-network` is given (DEP-3) |
| DNS rebinding: a hostile domain that resolves to `127.0.0.1` | Serve a request only when its `Host` is `localhost`, `127.0.0.1`, or `[::1]` with the configured port, or a host added with `--allow-host`. This guards against browsers, not against other machines; the loopback listener does that |
| A web page writing across sites | A write must carry `Content-Type: application/json`, which a cross-site form cannot send without a preflight that is never approved. An `Origin` header, when present, must be the Directory's own; `Sec-Fetch-Site`, when present, must be `same-origin` or `none`. Each of these headers may appear at most once, and an empty `Origin` counts as a mismatch |
| A web page reading across sites | The Directory never sends CORS headers |
| A web page probing which go links exist, by loading one as an image, a frame, or a fetch | The resolver answers a fixed `403`, before any lookup, when `Sec-Fetch-Site` is present and is neither `same-origin` nor `none`, unless the request is a top-level navigation (`Sec-Fetch-Mode: navigate` with `Sec-Fetch-Dest: document`). Clicked links and clients without these headers, such as `curl`, are unaffected. Added in L1 after review. Reads under `/-/` get the same refusal, pages from L3 on: whether `/-/links/<name>` is a page or a `404` tells which links exist. The health check and static files are exempt |
| Script injection through a name, description, or path | Escaping templates, the `innerHTML` ban, and the content security policy (NFR-11) |

`GET` requests never change state, apart from counting a use.

The write guards apply under `/-/` only, never to the resolver. In the L0 spike (check 4) a go link clicked on the Directory's own page arrived with `Sec-Fetch-Site: cross-site`, in headless Chromium and by hand on Chrome stable. In headless Chromium, a form on a plain-HTTP page of another site that posts to `http://go/…` arrived as a `POST` carrying that site's `Origin`; the same form on an HTTPS page did not arrive (Appendix B). The resolver refuses such a `POST` because it accepts `GET` and `HEAD` only (section 3.3).

### 3.9 Local runtime

| Flag | Environment | Default | Meaning |
|---|---|---|---|
| `--addr` | `OGRE_ADDR` | `localhost:4655`, both loopback addresses | Where to listen |
| `--db` | `OGRE_DB` | `ogre.db` in the data directory | Database file |
| `--allow-host` | `OGRE_ALLOW_HOSTS` | none | Extra `Host` values to accept |
| `--log-level` | `OGRE_LOG_LEVEL` | `info` | |
| `--dev` | — | off | Serve templates and static files from the source checkout, read on every request. Refuses to start when no checkout is found |
| `--allow-unauthenticated-network` | — | off | Allow listening beyond loopback although there is no sign-in (section 3.8) |

The data directory is `~/Library/Application Support/ogre` on macOS, `$XDG_DATA_HOME/ogre` (or `~/.local/share/ogre`) on Linux, and `%LocalAppData%\ogre` on Windows. It holds `ogre.db`, `backups/`, and `logs/`. The database lives there and not in the repository checkout, so pulling or deleting the source never touches a user's links.

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
| Extension logic | URL validation and rule building, the meta check, the service worker's handling of install, start-up, and a changed URL | `node --test` | `make check` |
| Web UI modules | The debounce, cancelling, and ordering of calls made while typing; the URL check of `lib/dom.js` | `node --test` over `e2e/unit` | `make check` |
| Type check | All browser and extension code | `tsc --noEmit` | `make check` |
| End to end | The redirect through the real extension, with path, query, and encoding intact; the settings and first-run pages; PRD scenarios 1–8; the unreachable page. In Firefox and WebKit, PRD scenarios 1–7 in the web UI, without the extension. A content security policy violation, console error, or uncaught error on a Directory page or an extension page fails the test | Playwright with its bundled Chromium; its Firefox and WebKit for the web UI only (`make e2e-browsers`) | `make check` |
| Accessibility | No serious or critical issue on the main pages and the extension's pages, in light and dark, and nothing wider than 375 pixels (NFR-21) | axe, run by Playwright; the web UI's pages in all three engines | `make check` |
| Performance | NFR-1, NFR-2, NFR-3, NFR-5 with 100,000 links | Go benchmarks (`make bench`); `make perf` (`cmd/ogre-perf`), recorded in `docs/perf.md` | Before each tag |
| Manual | Address-bar behavior, permission prompts, a link clicked on a public page, Chrome restarts, the real Firefox and Safari, the service after a reboot | `docs/manual-tests.md` | Before each tag |

Two things automated tests cannot cover, which is why the manual list exists: Playwright cannot type into Chrome's address bar, and Chrome-branded builds no longer accept the `--load-extension` flag that test tools rely on (Chromium still does).

A task is done when its code and tests land together, `make check` passes, any behavior a user sees is described in the README or on the setup page, and the commit names the PRD requirements it advances.

`make check` is the gate before every commit. It pins `staticcheck` to a version and needs the network the first time; `STATICCHECK_OPTIONAL=1` skips that step visibly. Firefox and WebKit are a separate download (`npx playwright install firefox webkit`); without them the gate fails and says so, and `E2E_BROWSERS_OPTIONAL=1` skips them visibly. The end-to-end tests run their own server, on a temporary database, at `localhost:4655`, the extension's default. When the developer's own Directory holds that port, `OGRE_E2E_PORT=46555 make check` runs them on another port and points each test browser there through the extension's settings. The one request that can still reach port 4655 then is the first-run page's read of the `meta` endpoint at install. The fuzz step retries only when Go's fuzz engine reports its own time limit as a failure with no failing input. The repository has a remote on GitHub; a workflow that runs the same command on Linux, with Chromium under a virtual display, is still to be written (section 9).

## 5. Local track

Each milestone lists what it **completes** (requirements fully met at its end) and, where relevant, what it **starts**.

### L0 — Spike

**Goal.** Confirm in a real browser the Chrome behaviors the design rests on, before any product code exists.

**Status, 2026-10-13: done.** All checks have a recorded result and none failed, so no fallback of Appendix B applies. Checks 1 to 6, 8 to 15, and 17 matched their expected outcome by hand on Chrome 155 stable, and check 16 was recorded there (Chrome's error page). Two were run in a narrower form than Appendix B words them: check 4 with host access to `go` and the Directory, and check 5's hand-off from another app as a command-line hand-off to the test profile. Check 7 rests on the automated run in headless Chromium 156, with a resolver switch standing in for a hosts entry. The stack (Go) and the address-bar keyword (`go/`) are confirmed. The working name the spike ran under, `godir`, has since been replaced: the product is called Ogre (Q8). Port 4655 was free in every run. Name matching (Q5) is decided: names ignore case only, and `-` and `_` are significant. Within check 5 and check 14, the go link opened with Chrome closed was reported as working by the owner but was not seen in the stub's log. Results are in [spike-chrome.md](spike-chrome.md).

- **L0.1 Throwaway extension and stub.** A minimal Manifest V3 extension with the rule of section 3.7, and a 30-line Go server on port 4655 that logs each request and answers `302` or `404`. Both lived in `spike/`, which was deleted when L1 started; the code is in the repository's history at the tag `l0-spike`.
- **L0.2 Run the checklist** of Appendix B on current Chrome stable on macOS, in a fresh profile. Record the Chrome version, the date, and what was observed for each check in `docs/spike-chrome.md`.
- **L0.3 Apply the fallbacks.** For each check that fails, take the "if it fails" action in Appendix B and update this plan and PRD Appendix B.
- **L0.4 Confirm section 2**: stack, name matching, working name, port, and the address-bar keyword for L6.8.

**Done when** every check has a recorded result, and every failed check has a workaround written into this plan.

### L1 — Walking skeleton

**Goal.** The thinnest end-to-end slice on the final architecture: persistence, migrations, routing, request guards, the redirect rule, and the test harness.

**You can now** create `roadmap` with `curl`, type `go/roadmap` in Chrome, and land on its destination — and it still works after a restart.

**Status, 2026-10-13: built and verified, with one check left for a person.** `make check` passes: formatting, vet, staticcheck, the Go tests with the race detector, three fuzz targets, the type check, 21 extension unit tests, and 12 end-to-end tests in headless Chromium with the extension loaded. An independent verification confirmed two of the three "done when" items (`make check` passes, and 25 of 25 saved links survived `kill -9`) and the nine requirements below, as far as headless Chromium can show them. The third item is the check left for a person: typing `go/roadmap` in real Chrome with `extension/` loaded. `docs/manual-tests.md` (L4.8) is not written yet. Three reviewers found no high-severity issue; their findings are fixed, and the one behavior they added is the probing guard in section 3.8.

**Completes:** RES-3, RES-6, EXT-1, AUTH-1, DEP-1, DEP-3, DEP-6, NFR-4, NFR-7. **Starts:** RES-1, API-1.

- **L1.1 Repository.** `git init`; the layout of section 3.1; `go.mod`; a `Makefile` with `run`, `test`, and `check`; `package.json` with `typescript`, `@types/chrome`, `@playwright/test`, and `@axe-core/playwright`; `.gitignore`; `gofmt`, `go vet`, and `staticcheck`.
- **L1.2 Configuration.** The flags and environment variables of section 3.9; the per-OS data directory; `ogre version`; the refusal to listen beyond loopback while sign-in is off.
- **L1.3 Store and migrations.** Open SQLite with the write-ahead log, foreign keys on, and full synchronous commits. A migration runner over embedded `NNNN_name.sql` files, each applied in a transaction and recorded in `schema_migrations`. Before migrating an existing database, copy the file aside. Apply `0001_init.sql` and insert the built-in local user.
- **L1.4 Link model, first cut.** Lowercasing of names (the only normalization, per Q5), literal-only patterns, and a destination that must be an absolute `http` or `https` URL. L2 completes the rules and extends the types: a variable segment kind, the variables of a route, and removal and iteration on the index snapshot.
- **L1.5 Index.** Load every link into a snapshot keyed by lowercase name at startup; swap in a new snapshot after each committed write.
- **L1.6 HTTP server.** Listeners on both loopback addresses (the IPv6 one is skipped only on a machine without IPv6; if `[::1]` is taken, the server refuses to start); the routing of section 3.3; the guards of section 3.8; security headers; graceful shutdown. When the port is taken, say whether another `ogre` holds it (by asking its `meta` endpoint) or some other program.
- **L1.7 Endpoints.** `GET /-/api/v1/meta`, `GET /-/healthz`, `POST` and `GET /-/api/v1/links`, the resolver answering `302` or a bare `404`, and a placeholder page at `/-/`.
- **L1.8 Extension skeleton.** `manifest.json` and a service worker that installs the redirect rule for the default Directory URL on install and on browser startup. No pages yet.
- **L1.9 Tests.** Store and migration tests on temporary databases. HTTP tests for the redirect's status and headers and for each request guard. One Playwright test that starts the server, loads the extension, opens `http://go/x`, and asserts the browser lands on a fixture destination.
- **L1.10 `make check`** as described in section 4.

**Done when** the "you can now" works by hand, `make check` passes, and killing the process and restarting it loses no saved link.

### L2 — Link model and resolver core

**Goal.** PRD section 5 implemented once, in `linkmodel`, and proven by the PRD's own examples.

**You can now** create links with typed variables and several routes through the API, and get the right redirect, miss kind, reasons, and suggestions for any path from `GET /-/api/v1/resolve`.

**Status, 2026-10-13: built and verified.** An independent verification confirmed every "done when" item: the PRD 5.7 fixtures pass through the Go API and through HTTP (request 18 stays pending RES-8), six fuzz targets ran clean for 5 minutes each, and at 100,000 links a hit takes 0.4 to 3.4 µs and an unknown-name miss 0.6 to 9 ms. The gate now has six fuzz targets and 16 end-to-end tests. Three reviewers raised 26 findings, two of them high (the Directory's own address was not recognized in every spelling, and a deliberately built regex link could take seconds per request); all high and medium ones are fixed. Decisions taken where the PRD was silent:

- A value checked by a regex is at most 256 bytes, and compiled expressions are bounded (1,000 instructions each, 4,000 per link); the costliest link that can be saved takes about 10 ms per request.
- `DELETE` is exempt from the JSON content-type requirement, since it has no body; the origin checks still apply.
- `PATCH` replaces routes and variable metadata as a whole.
- The resolve endpoint answers a malformed path with `200` and kind `bad_request`.
- The unused-variable warning appears only when the destination is otherwise valid.
- A variable's example that its type rejects is a warning for now; VAR-6 makes it an error in L5.8.
- A link saved before the credentials rule existed would still resolve; validation runs on save only.
- Known gap: a destination that spells the Directory's own host in fullwidth Unicode letters is not recognized as the Directory.

**Completes:** VAR-1, RES-1, RES-4, RES-7, NFR-8, NFR-9, NFR-10, NFR-17, NFR-22. **Starts:** DIR-2, RES-2, MISS-1, MISS-2.

- **L2.1 Fixtures first.** Transcribe PRD 5.7 into `spec.json`: the links, every request with its expected outcome, and every save attempt. Mark request 18 as pending RES-8. Write the test that replays them; it fails until L2.9.
- **L2.2 Pattern parser.** A tokenizer, not a split on `/`: inside `regex(` it follows nested parentheses, escapes, and character classes (PRD 5.2). It produces segments with source offsets, reports problems as a code, a message, and a start and end offset, and enforces the character rules, the literal first segment, unique variable names, and the limits. It also provides the canonical text form, the shape, and the lowercasing of names.
- **L2.3 Types.** A small interface — does it accept this value, how is it described to a user, is it restricted, how is it written — implemented for `string`, `int`, `number`, and `regex`. A regex is compiled by Go's `regexp`, wrapped as `^(?:…)$`, and limited to 200 characters. L6 adds types through the same interface.
- **L2.4 Destination templates.** Validate per PRD 5.4: absolute `http` or `https`, at most 2,048 characters, no credentials (a user name or password before the host, as in `https://example.com@evil.example/`), no placeholder in the scheme or authority (checked on the text, before URL parsing), every placeholder declared, unused variables as a warning, not a go link and not a URL on the Directory. Record whether each placeholder sits in the path, the query, or the fragment. Expand by percent-encoding everything except unreserved characters. After expanding, assert that the scheme and host equal the template's; a mismatch is a logged bug and a `500`, never a redirect. The credentials rule is new with this task. The L1 build still accepts such destinations (`ValidateDestination` in `internal/linkmodel` and its tests say so, and change here); the owner decided on 2026-10-13 to forbid them. Still to settle within this task: what happens to a link saved earlier with such a destination. Validation runs only when a link is saved, so a stored link is not checked again by itself.
- **L2.5 Request splitting.** PRD 5.5 step 1 on the raw path: drop the query and fragment, split on `/`, discard empty segments, decode each segment once. A malformed escape or invalid UTF-8 is a bad request.
- **L2.6 Matching.** PRD 5.5 steps 2–4: candidate routes, acceptance per segment, specificity compared from the left (literal, then restricted variable, then text variable, then `path`), then route order. A variable whose placeholder sits in the destination's path rejects `.` and `..` (NFR-10). Every route that doesn't match yields a reason — wrong segment count, literal mismatch, or value rejected — with the variable, the value, what the variable accepts, and a sentence ready to show.
- **L2.7 Save rules.** A name unique ignoring case; all routes of a link share one name; a unique shape within the link; at most 20 routes; the reserved names of section 3.3.
- **L2.8 Suggestions.** Damerau–Levenshtein distance (the optimal-string-alignment form), stopping early once the allowed distance is exceeded. Thresholds of 0, 1, and 2 edits by the length of the requested name. Scan all names; for each candidate, check whether its name plus the rest of the request resolves. Order by edits, then name; return at most five. Thresholds and the limit are configuration.
- **L2.9 `Resolve`.** One function from a snapshot and a raw path to a decision: home, hit, unknown-name miss, known-name miss, or bad request.
- **L2.10 Use cases and persistence.** Create, update, and delete with the full model. Optimistic concurrency on `links.version`. A revision snapshot on every change — history is captured from now on, though its screen arrives in L5. Snapshot swap after commit. Renaming changes the first segment of every route.
- **L2.11 API.** `POST /links/validate`, `GET /resolve`, and full create, read, update, delete on `/links`, with the shapes of section 3.5. Creating a link whose name exists answers `409` with the existing link and whether the caller may edit it (DIR-4).
- **L2.12 Resolver handler.** Renders the decision as `302`, `404` (a plain page in L2, replaced by the miss page of L3.8), or `400`. Request logs name the matched link, never the raw path (NFR-17).
- **L2.13 Robustness.** The fuzz targets of section 4. Benchmarks with 100,000 generated links for a hit, an unknown-name miss, and a known-name miss.

**Done when** every P0 fixture passes through the Go API and through HTTP; each fuzz target runs clean for 5 minutes; and the benchmarks show a hit under 1 ms and an unknown-name miss under 50 ms in process, leaving ample room inside NFR-1 and NFR-2.

### L3 — Web UI

**Goal.** Everything a person does in the browser works against the full Directory URL, with or without the extension.

**You can now** carry out PRD scenarios 1 to 7 at `http://localhost:4655`.

**Status, 2026-10-14: done.** `make check` passes from a clean state. Besides the steps of L2, it now runs 9 unit tests of the web UI's modules, the wider DOM-sink scan of section 3.6, and 47 end-to-end tests in headless Chromium. Those drive PRD scenarios 1 to 7 through the pages with the extension loaded, scan every page with axe in light and dark, and check a width of 375 pixels, the reading pages without JavaScript, and search by keyboard alone. Negative controls show that the policy watch and the scan fail when they should. An independent verification confirmed both "done when" items and every requirement below. Three reviewers raised 38 findings, none high and 16 medium; 15 of the medium ones are fixed, and the sixteenth is the conflict between PRD scenarios 2 and 5 noted below. Only Chromium has been exercised, so L3 claims only part of NFR-20: phone width and Chromium are verified, and Firefox, Safari, and Edge wait for a manual or CI pass in L4 (section 9). Typing `go/…` in real Chrome with the extension is still the check left for a person. Decisions taken where the PRD was silent:

- Scenario 5 is shown with the regex month of PRD 5.7. An `int` has no range until VAR-9 (L6.1), so the integer month of scenario 2 accepts `13`; the end-to-end test pins this.
- Pages send `Referrer-Policy: same-origin`; miss pages and the bad-request page send `no-referrer` (section 3.6).
- A page of another site cannot load a `/-/` page as an image, a frame, or a fetch (section 3.8).
- Routes prefilled by another site are left out of the edit form and flagged on the create form (L3.6).
- Search has a rank the PRD doesn't name, `name`, for a word found inside a name (section 3.5). A result without variables opens through the resolver (`/<name>`), so the server decides where it goes and can count the use in L5.
- Every write goes through the API. The reading pages call the same use cases directly; the API has no lookup by name, owner filter, or sort, which those pages would need to be built on it. These wait in the backlog.
- The gate's accessibility scan uses the WCAG 2.0 and 2.1 A and AA rules. A run that added the 2.2 AA rules was clean too, but they are not in the gate.

**Completes:** DIR-1, DIR-2, DIR-3, DIR-4, DIR-6, DIR-7, VAR-2, VAR-3, VAR-4, VAR-5, SRCH-1, SRCH-2, SRCH-3, SRCH-4, MISS-1, MISS-2, MISS-3, RES-2, RES-5, API-1, NFR-11, NFR-21. **Starts:** DIR-5, NFR-20.

- **L3.1 Foundation.** Layout template; stylesheet with color tokens for light and dark; static files under a content-hash prefix; `--dev` mode; the content security policy. Focus styles, labels, and landmarks from the first page on.
- **L3.2 Search.** The ranking of SRCH-2, computed in the index: exact name, name prefix, misspelled name (the distance of L2.8), then substring matches in names, descriptions, destinations, and owners. Unit tests pin the order.
- **L3.3 Home.** Search box focused on load. "Your links" and "Recently added" rendered by the server. The search component lists results with variables highlighted and opens links without variables directly. Pages ask for one result more than they show, and say "showing the best N" only when more matched.
- **L3.4 Link page.** Description, owner, routes with destinations, variables with types, buttons that copy `go/…` and the full URL, the launcher, and Edit and Delete.
- **L3.5 Launcher.** One field per variable of the chosen route. Each debounced keystroke asks `GET /resolve` and shows the resulting go link and final URL, or the server's reason. Open is enabled only on a hit.
- **L3.6 Create and edit form.** Route rows with add, remove, move up, and move down; a description. The validate endpoint drives the rest: token highlighting, an error marker at the reported character, messages beside the field concerned, name availability, and conflicts that link to the existing link. The variables table shows a type selector per variable and, for a regex, an expression field with the sample tester. Changing a row rewrites that variable in the pattern; editing the pattern refreshes the table. The launcher serves as the live preview. The form opens prefilled from `?pattern=` and `?destination=`. When the name belongs to a link the user may edit, it offers to add the route there. It reports a version conflict when the link changed in another tab. Editing the first route's name renames every route that starts with the same name. The type labels come from `linkmodel.TypeChoices`, where L6 adds its types. A prefill counts as another site's when `Sec-Fetch-Site` is present and is neither `same-origin` nor `none`. The edit form leaves such routes out and says so, because a route added to an existing link takes the requests it answers more specifically than the link's own routes (PRD 5.5). The create form keeps them, since that is how DIR-7 works, but warns and names each destination's host.
- **L3.7 Delete.** A confirmation that names the link. A user who may not delete the link gets the page with the reason and no button, as a `403`, like the edit form. From L5.6 it also shows when the link was last used.
- **L3.8 Miss page.** Rendered by the server from the decision, at the missed URL, with status `404`, working without JavaScript. For an unknown name: up to five suggestions with description and destination. For a known name: the link's description and each route with the reason it didn't fit. Both kinds offer "Create `go/<typed path>`" and a search box holding the name; on a known name the create form finds the name taken and offers to add the route (DIR-4). An address that cannot be a link at all is answered without repeating it.
- **L3.9 Setup page.** How to load the extension, how to check that it works, what to do when Chrome searches instead of navigating, that in Incognito, where the extension is not allowed by default, a typed go link ended on Chrome's error page (L0, check 16), and how to allow the extension there (that this makes go links work in Incognito was not tried in L0), and the extension-free alternative: a browser search shortcut pointing at `http://localhost:4655/%s`.
- **L3.10 Reserved paths** of section 3.3, answered directly and never shown as misses.
- **L3.11 Tests.** Page tests with hostile names, descriptions, and paths to prove escaping. Playwright flows for scenarios 1–7. The accessibility scan on home, form, link page, and miss page. A pass at a width of 375 pixels on the miss and link pages.

**Done when** scenarios 1–7 pass as Playwright tests with the content security policy enforced and no violation reported, and the accessibility scan finds no serious or critical issue.

### L4 — Extension and local MVP

**Goal.** Finish the extension's P0 pages, then prove the whole P0 set on a clean machine. This closes the PRD's M1, except for one clause of DIR-5 (section 1).

**Status, 2026-10-14: done, except the steps that need a person: the timed README run, the Chrome stable pass of `docs/manual-tests.md`, and the `v0.1.0` tag.** `make check` passes from a clean state (`rm -rf bin`). Besides the steps of L3, it now runs 71 extension unit tests, 74 end-to-end tests in headless Chromium with the extension loaded, and, as its last step (`make e2e-browsers`), 32 tests in Playwright's Firefox and WebKit. Those run PRD scenarios 1 to 7 in the web UI without the extension, the axe scan in light and dark, a width of 375 pixels, and the pattern highlighting. Edge is not run; Chromium stands in for it. Every gate run in L4 used `OGRE_E2E_PORT=46555`, because the owner's own Directory held port 4655, so the tests' default-port branches have not run yet. An independent verification confirmed EXT-1 to EXT-4, NFR-18, NFR-21 on the extension's pages, and the part of DEP-2 a machine can do: from a fresh clone the README's steps lead to a working go link, and a first `go run` with empty caches took 16.7 s. A logging proxy showed that the only request the extension makes itself is `GET /-/api/v1/meta`, to the addresses it was set to or asked to test. Three reviewers found one high-severity issue (nothing tested the perf tool's verdicts) and nine medium ones; all are fixed.

The performance run of L4.7 passes at 100,000 links ([perf.md](perf.md), three runs, medians): a hit takes 0.008 ms at the 95th percentile, miss pages 0.088 and 2.88 ms, search 18.9 and 19.6 ms, and 200 resolutions per second are served with no error, at a peak of 412 MiB against 512 MiB. The first three runs failed NFR-5 on memory, at 517 to 536 MiB. The fix: routes with the same regular expression share one compiled expression (`internal/linkmodel/regexcache.go`, a cache of weak pointers that never keeps an expression alive), and text buffers are sized before they are filled. Three caveats:

- Repeated before the tag on 2026-10-14 from a clean clone of ca53300, on mains power, with port 4655 free: `make check` on the default port, `make fuzz FUZZTIME=5m` and three `make perf` runs all passed; perf.md holds those numbers (hit p95 0.006 ms, misses 0.070 and 2.05 ms, search 13.5 and 13.7 ms, peak RSS 421 MiB).
- The 100 MiB of headroom depends on shared regexes: the generated links use three distinct expressions. A Directory where every regex route had its own would come close to the limit again (section 9).
- A laptop core is faster than a cloud vCPU. C2.9 is the real test of NFR-5.

L4.5 checked which `Sec-Fetch-Site` the Directory receives through the extension. A typed go link redirected by the rule, and a tab the extension opens with `chrome.tabs.create`, arrive as `none`. A link clicked on an extension page arrives as `cross-site`, like any other site's. So the forms' rule of L3.6 stays as it is: no prefill a user starts is lost. No extension page links to the Directory today; the popup of L6.7 must open its tabs with `chrome.tabs.create`. `e2e/fetch-metadata.spec.mjs` pins all three; that real typing gives `none` is manual step B7.

Still to do, by a person:

- The timed README run (L4.6, DEP-2), by someone new to the project, on a clean macOS account. An estimate with Chrome, Go, and git already installed is 5 to 7 minutes; the README now says five minutes holds only then.
- The Chrome stable pass of `docs/manual-tests.md`, with section H in the real Firefox and Safari. D2 decides whether the "Not tested yet" note on Incognito leaves the setup page and the README; F3 decides what the setup page says about the search shortcut (section 9).
- L4.9: decide the version, then tag.

Decisions taken where the PRD and this plan were silent:

- A Directory URL without a scheme (`localhost:4655`) is refused, with the corrected form in the message, not completed silently: a Directory elsewhere may need `https`. A port alone (`4771`) gets `http://localhost:4771` as its suggestion. The host `go` is refused on any port, since every go link would come back to it. IPv6 literals and international names are accepted; the latter are stored and shown as punycode, so a look-alike name is visible.
- An address the user does not grant host access to is not saved, though the redirect rule alone would not need the access.
- The first-run page checks the server when it opens, on "Check again", and when it regains focus or becomes visible; it never polls. It follows an address saved in another tab without a reload, tells "Not answering", "Wrong server", and "Wrong version" apart, and leaves the query out of the go link it shows in step 3.
- Routes another site prefilled now keep their warning when the create form hands them to the edit form through "Add this route" (`prefill=foreign`). Before, that hand-over skipped the check of L3.6, and one click on Save added another site's route to an existing link. A reviewer found this in L3 code.
- The perf tool also judges NFR-5 on the wait from each request's scheduled send, which includes time queued for the server's one CPU that server time cannot see, and it fails a phase that finished early.
- Headless Chromium never answers Chrome's permission prompt. The tests approve access through `chrome.developerPrivate.addHostPermission` on `chrome://extensions`; the extension's own `permissions.request` then resolves with no prompt. The tests wait for the worker's `installHandled` mark instead of sleeping, and then close the first-run tab opened at install, except in the first-run page's own tests.
- WebKit reports a content security policy violation for every `<select>`, from the engine's own inline style. It is harmless and cannot be fixed in our code, so the end-to-end watch lets exactly that report through in WebKit and nothing else (section 9).

**Completes:** EXT-2, EXT-3, EXT-4, DEP-2, NFR-1, NFR-2, NFR-3, NFR-5, NFR-18, NFR-20. **Starts:** NFR-19.

- **L4.1 Settings logic.** Validate a Directory URL — `http` or `https`, no credentials, no path — and reduce it to its origin; build the rule; store the URL in `chrome.storage.local`. Unit-tested with `node --test`.
- **L4.2 Settings page.** A URL field defaulting to `http://localhost:4655`. "Test connection" fetches the `meta` endpoint and checks the product identifier and API version. Save requests host access for the new origin, replaces the redirect rule in a single call, and releases access to the old origin. A malformed URL cannot be saved; a well-formed one that doesn't answer can, after a warning.
- **L4.3 First-run page.** Opens on install. It shows whether the Directory answers, and how to start it if not. It asks the user to type `go/` and press Enter, and confirms by itself when it sees the navigation (check 13 of Appendix B holds: on Chrome stable the extension saw typed go navigations). Then it has the user try a link without a trailing slash and explains the fallback. Two findings of L0 shape this page. It asks for `go/` only once the Directory answers: in headless Chromium, a visit to `http://go/` made with the "typed" transition while the Directory was down left no `go` entry in history, and `chrome://omnibox` still reported a search for `go/foo` (check 3, proxy evidence; not repeated by hand). It accepts `https://go/` as well as `http://go/` as the navigation it waits for: in headless Chromium with "Always use secure connections" at its strict level, the address was rewritten to `https://go/` before the extension saw it (check 6). By hand on Chrome stable the strict level was only shown to reach the Directory; the scheme the extension saw there was not recorded.
- **L4.4 Permission audit.** The manifest holds exactly the permissions of section 3.7. `extension/README.md` states the purpose of each. No content scripts, no remotely loaded code, no telemetry (NFR-18).
- **L4.5 Extension tests.** Playwright: path, query, and percent-encoding survive the redirect; changing the Directory URL re-points the rule, checked with two test servers; the first-run page renders. The permission prompt for a custom URL belongs to the manual list. So does start-up behavior: with `--load-extension`, Chromium fires `onInstalled` (reason `install`) on every launch and never `onStartup` (L0 spike, check 14), so in these tests the first-run page opens in every session and the `onStartup` path is never exercised. Check which `Sec-Fetch-Site` the Directory receives when the extension opens or redirects a tab to it: the forms treat a prefill as another site's unless it is `same-origin` or `none` (L3.6).
- **L4.6 README.** Prerequisites, the one command, loading the extension, the first link, troubleshooting. Timed on a clean macOS user account by someone new to the project: five minutes or less.
- **L4.7 Performance run.** Generate 100,000 links. Over real HTTP, measure server time at the 95th percentile for hits, miss pages, and search, plus memory use and startup time. Record the results in `docs/perf.md` against NFR-1, NFR-2, NFR-3, and NFR-5.
- **L4.8 Manual browser pass.** Write `docs/manual-tests.md` — the address-bar checks no automated test can perform — and run it on Chrome stable on macOS. Run PRD scenarios 1 to 7 in the web UI on current Firefox, Safari, and Edge, by hand or with Playwright's Firefox and WebKit in CI (NFR-20).
- **L4.9 Tag `v0.1.0`.** Open: which version the tagged code reports. `cmd/ogre/main.go` has `var version = "0.1.0-dev"` and the Makefile's `VERSION` defaults to the same, so `go run ./cmd/ogre`, `ogre version`, and the `meta` endpoint say `0.1.0-dev`; the extension's manifest says `0.1.0`. Either set `0.1.0` in the tagged commit, or accept `-dev` and say so in the tag's notes.

**Done when** every P0 requirement in Appendix A is met, DIR-5 apart from its last-use clause (section 1), and the PRD's M1 criteria hold: the P0 fixtures pass, and the timed README run succeeds.

### L5 — Daily driver

**Goal.** A person can depend on the local product every day without thinking about it.

**You can now** reboot and have `go/…` work without opening a terminal, see a helpful page when the Directory is down, restore yesterday's data, move links between machines, see what you use, and undo a bad edit.

**Completes:** DIR-5, VAR-6, MISS-4, EXT-5, INS-1, INS-2, INS-3, DEP-10, NFR-16.

- **L5.1 Background service.** `ogre service install`, `uninstall`, `status`, `restart`, and `logs`. On macOS, a LaunchAgent that runs at login and restarts the process if it exits. On Linux, a systemd user unit. On Windows, a documented Task Scheduler command. The service runs the binary that executed `install`, and refuses when that binary is a `go run` temporary file. Log files rotate at a size cap.
- **L5.2 Install path.** `make install`, and a README section "Use it every day": install, `ogre service install`, done.
- **L5.3 Unreachable page.** The service worker watches for failed top-level requests to the Directory's origin and replaces the tab with an extension page. The page names the configured URL, offers Retry and Settings, and — for a local URL — shows the command that starts the Directory. A failed Retry must not loop. From the L0 spike (check 12, headless Chromium only): when `onErrorOccurred` fired, `chrome.tabs.get` never gave a `pendingUrl`. It gave a `url` only when the tab was already showing the extension's own unreachable page (the failed Retry and the Back case); for a tab coming from a page the extension may not read it gave neither. So the handler cannot check that the tab still shows the failed address; the spike skipped `net::ERR_ABORTED` and non-active documents instead. Pressing Back on the unreachable page behaved differently in the two runs: headless Chromium returned to the failed address and landed on the unreachable page again, while Chrome 155 stable, by hand, returned to the page the tab showed before. Test Back on Chrome stable and keep that behavior.
- **L5.4 Export and import.** `GET /export` returns the document of section 3.5. `POST /import?dryRun=true` reports what would be created, skipped as conflicting, or rejected as invalid, with reasons; without `dryRun`, it applies the same plan in one transaction. Buttons on the settings page; `ogre export` and `ogre import` on the command line. A round-trip test: export, empty the database, import, compare.
- **L5.5 Backups.** A daily copy of the database made with `VACUUM INTO`, keeping the last seven. `ogre backup <file>` on demand. `ogre restore <file>` with the server stopped. Restore steps in the README.
- **L5.6 Usage counts.** Migration `0002`. Hits are counted in memory per route and written every 10 seconds and at shutdown. The link page shows totals and last use; the home page gains "Most used"; recent use breaks ties in search and orders suggestions; the delete confirmation shows last use. Nothing per user and nothing per value is stored (NFR-16, NFR-17).
- **L5.7 History and revert.** The link page lists revisions with what changed in each. Any revision can be viewed and restored; restoring writes a new revision and validates against the Directory as it is now.
- **L5.8 Variable descriptions and examples.** Editable in the variables table. An example must satisfy its variable's type. Both appear in the launcher, in search results, and in usage help, and examples prefill the form's preview.
- **L5.9 Keyboard-first miss page.** The first suggestion has focus, Enter follows it, and the digits 1–5 choose.
- **L5.10 Sample links.** `ogre seed` adds a handful of links to public sites that show each variable type, and only into an empty Directory.
- **L5.11 Diagnostics.** `ogre doctor` reports the address and whether the server answers, the database path and its integrity check, the schema version, the service's state, and the age of the last backup.
- **L5.12 Upgrade safety.** A `v0.1.0` database fixture migrates cleanly in tests. The `meta` endpoint reports the API version, and the extension warns when it doesn't support it.
- **L5.13 Tag `v0.2.0`.**

**Done when** a typed go link works after a reboot with no manual step; stopping the service produces the unreachable page; the export–import round trip is lossless; a revert brings back an earlier destination; and yesterday's backup exists and restores.

### L6 — Power features

**Goal.** The remaining features a single user would notice the absence of.

**Completes:** DIR-8, DIR-9, VAR-7, VAR-8, VAR-9, SRCH-7, RES-8, RES-10, MISS-6, MISS-7, EXT-7, EXT-8.

The tasks are independent of each other and can be done in any order.

- **L6.1 More types.** Integer ranges, `enum`, and `path` with `{+name}` expansion. `path` is allowed only as the last segment, ranks below a text variable, captures one or more segments, and rejects dot segments. The variables table gains bounds and value-list editors. New fixtures cover each type and the precedence between `path` and longer routes. The overlap warning of PRD 5.5 arrives with them, including a warning when a draft route would take requests that another route of the same link answers today (L3 review). Before the new editors, the form's type syntax (`{name:int}`, `{name:regex(…)}`), the only pattern syntax browser code writes, should come from the server.
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
| API-2, API-3 | Access tokens and an OpenAPI description. With them, the reads the pages make through the use cases directly: a link by name, an owner filter, a sort, and `limit` on the plain list (L3) |
| RES-9 | Serving hits while the database is down. Matters with Postgres |
| DEP-11 | Several instances serving one database |
| AUTH-8 | Sign-in through an identity header from a reverse proxy |
| DIR-10, VAR-10, VAR-11, EXT-9, EXT-10, EXT-11, EXT-12, AUTH-9, AUTH-10, INS-5 | The PRD's P3 list |
| — | Upkeep left by the L3 review: one source for the markup of a link in a list (now both a template and `search.js`), and JSDoc types for API answers (now `any`) |

## 9. Risks and open points

| Risk | Effect | Response |
|---|---|---|
| A Chrome behavior in Appendix B doesn't hold | A design change in the extension | L0 comes before any product code, and each check has a fallback |
| Chrome's Local Network Access rules tighten | A go link clicked on a public page may prompt or fail when the Directory is on loopback | Check 11; rerun the manual list, whose step B8 clicks a go link on a public HTTPS page, on each Chrome release during the local track; the fallback is to route clicked links through an extension page |
| Pattern highlighting misbehaves in some browser | L3.6 takes longer | The fallback of section 3.6 still meets VAR-2 |
| Browser code without a build step becomes hard to maintain | Slower UI work in L5 and L6 | Type-checked from day one; small components. Switching to TypeScript sources later is confined to `internal/web/static` and `extension/` |
| The pure-Go SQLite driver is slower than expected | NFR-3 or NFR-5 missed | The resolver never touches the database; L4.7 measures the rest; a cgo driver fits behind the same interface |
| Unpacked extensions need Chrome's developer mode | Friction for daily local use | Acceptable for the owner. If it grates, bring C2.7 forward — packaging doesn't depend on the rest of the cloud track |
| Autostart differs between launchd and systemd | The service doesn't come up after login on some machine | A reboot test per OS on the manual list; `ogre doctor` |
| L5 and L6 delay the cloud track | A later team release | L6 is the cut line |
| A Directory where most regex routes have an expression of their own | NFR-5's memory limit is missed: the 100 MiB of headroom measured in L4.7 comes mostly from routes sharing compiled expressions, and the generated links use only three. Without sharing, peak memory is estimated at about the limit | C2.9 measures the deployed form. If needed, lower the Go collector's heap target |
| `extension/shared/page.css` is a hand-made copy of the web UI's color tokens and components in `site.css` | The extension's pages drift from the web UI, or lose the contrast `site.css` was checked for | The tokens match today; only a comment in `page.css` asks to copy a change. A test that compares the tokens of both files would catch drift |
| From L5 the background service holds port 4655 | `make check` fails on the busy port | Stop the service for the gate, or run it with `OGRE_E2E_PORT` |

Open points from L3, settled in L4:

- NFR-20 beyond Chromium. Closed: `make check` runs PRD scenarios 1 to 7, the accessibility scan, 375 pixels, and the pattern highlighting in Playwright's Firefox and WebKit (`make e2e-browsers`). The WebKit report is an engine quirk, not our code: WebKit refuses an inline style of its own inside every `<select>`, even in a static page with no script or stylesheet, and the select draws the same with and without the policy. `e2e/webkit-select-csp.spec.mjs` pins it and fails the day WebKit stops; the end-to-end watch lets that one report through in WebKit only. Edge is covered by Chromium standing in for it. The real Firefox and Safari are section H of `docs/manual-tests.md`.
- Whether Chrome's search shortcut turns `/` into `%2F` in `%s`. Moved to manual step F3; still open until that step has run. If it does, the setup page must say the shortcut works only for links without variables.

Open points that don't block L0–L4:

- The CI workflow. The repository is on GitHub, so the workflow of section 4 can be wired now.
- How far to support Windows for the local server. This plan documents a Task Scheduler command and builds nothing more.

## Appendix A. Requirement traceability

Each PRD requirement appears once, under the milestone that completes it.

| Milestone | Requirements |
|---|---|
| L1 | RES-3, RES-6, EXT-1, AUTH-1, DEP-1, DEP-3, DEP-6, NFR-4, NFR-7 |
| L2 | VAR-1, RES-1, RES-4, RES-7, NFR-8, NFR-9, NFR-10, NFR-17, NFR-22 |
| L3 | DIR-1, DIR-2, DIR-3, DIR-4, DIR-6, DIR-7, VAR-2, VAR-3, VAR-4, VAR-5, SRCH-1, SRCH-2, SRCH-3, SRCH-4, MISS-1, MISS-2, MISS-3, RES-2, RES-5, API-1, NFR-11, NFR-21 |
| L4 | EXT-2, EXT-3, EXT-4, DEP-2, NFR-1, NFR-2, NFR-3, NFR-5, NFR-18, NFR-20 |
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
- NFR-20 holds at phone width and in Chromium from L3. From L4, `make check` also runs the web UI in Playwright's Firefox and WebKit; Edge is covered by Chromium standing in for it, and the real Firefox and Safari by the manual list.
- DIR-5 is P0 but listed under L5: the confirmation exists from L3.7, and the last use it shows needs INS-1 (L5.6). M1 is complete without that clause (section 1).
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
