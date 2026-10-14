# PRD: Go Links — Directory and Chrome Extension

| | |
|---|---|
| **Status** | Draft for review |
| **Date** | 2026-10-13 |
| **Source** | `idea.md`, the original idea note (no longer in the tree; it is in the repository's history) |

Requirements that go beyond `idea.md` are marked **Added** in sections 6 and 7 and summarized in Appendix A.

## 1. Summary

A go link is a short, memorable name that redirects to a long URL: typing `go/roadmap` in the address bar opens the roadmap. This product has two parts:

- **The Directory** — a database, web app, and API where people register, find, and edit go links. It also performs the redirect.
- **The Chrome extension** — makes `go/…` typed in the address bar reach the Directory, with no DNS or network setup.

What sets it apart from existing go-link tools is **named, typed variables in the link path**. A single link, `go/reports/{month}/{day}`, covers every daily report by redirecting to `https://reports.example.com/daily?month={month}&day={day}`, and `month` can be restricted to a number or a regular expression. A request that breaks a variable's type is not redirected; it counts as a miss.

A miss is never a dead end. An unknown link gets "Did you mean …?" suggestions based on edit distance, a known link used the wrong way gets an explanation of what it expects, and anything else can be registered on the spot.

The same build runs on a laptop with one command and no login, and as a shared, authenticated service for a team. Where the shared service will be hosted is still open (section 13).

## 2. Problem and goals

### 2.1 Problem

Useful internal pages have URLs nobody can remember or say out loud. People cope with bookmarks, pinned chat messages, and wiki pages full of links — all personal, all prone to going stale, none of them something you can dictate in a meeting.

Many of the most-used destinations are also parameterized: the report for a given day, a ticket by ID, a repository by name. A bookmark covers one instance. Existing go-link tools handle these with anonymous positional placeholders (Appendix C), which don't say what a value means and can't reject a wrong one.

### 2.2 Goals

| # | Goal |
|---|---|
| G1 | **One-step navigation.** A registered link typed as `go/<path>` in Chrome lands on its destination with no intermediate page. |
| G2 | **Fast registration.** Registering a link, including one with variables, takes under a minute and needs no documentation. |
| G3 | **First-class variables.** Variables are named, optionally typed, validated on every request, and explained wherever the link appears. |
| G4 | **No dead ends.** Every miss offers a next step: a suggestion, an explanation, or creating the link. |
| G5 | **One product, two setups.** Zero configuration on a laptop; shared and authenticated when deployed. |
| G6 | **A directory people trust.** Every link has an owner, a change history, and visible usage. |

### 2.3 Non-goals for v1

- A public URL shortener: random short codes, anonymous public traffic.
- Hosting several organizations in one deployment. One Directory serves one organization and one namespace.
- Private, personal, or team-only links. Every signed-in user can see and use every link (see Q7).
- Resolving links inside the extension, or offline.
- First-class support for browsers other than desktop Chrome. Links still work everywhere through their full Directory URL (RES-6).
- Variables that cover part of a segment (`v{version}`), optional variables, and value transformations such as zero-padding.
- A localized UI. English only.

### 2.4 Assumptions

- Users of a deployed Directory are colleagues in one organization; abuse is possible but not the common case.
- Scale stays within 100,000 links and a few hundred resolutions per second — small enough for an in-memory index on one modest server.
- Users can install a Chrome extension, by themselves or through company policy.

## 3. Users and scenarios

| Role | What they do | What they need |
|---|---|---|
| **Link user** | Types `go/…` many times a day | Speed, and forgiveness when they misremember a name or a value |
| **Link author** | Registers and maintains links for their team's tools | Variables that just work, confidence a link behaves as intended, a view of whether it is used |
| **Admin** (deployed only) | Keeps the Directory healthy | The ability to fix ownership, remove abuse, and manage access |
| **Solo user** (local only) | Runs the product on their own machine as a personal launcher | Setup in minutes, with no account and no infrastructure |

Scenarios the product must support:

1. **Register a simple link.** Ana opens the Directory, enters `roadmap` and a destination URL, and saves. `go/roadmap` works immediately.
2. **Register a link with typed variables.** Ana enters `reports/{month}/{day}`. The form lists `month` and `day`; she sets both to integers and watches a preview of the final URL update as she types sample values.
3. **Use a link.** Ben types `go/reports/10/13` and lands on the report for October 13.
4. **Recover from a typo.** Ben types `go/reprots/10/13`, sees "Did you mean `go/reports/10/13`?", and presses Enter.
5. **Recover from a bad value.** Ben types `go/reports/13/01`. Instead of a broken report page, he sees that `month` doesn't accept `13`, and what it does accept.
6. **Discover a link.** Chen doesn't know the name. She searches the Directory for "daily report", finds `go/reports`, reads what its variables mean, fills in two fields, and opens the report.
7. **Maintain a link.** The reports tool moves. Ana edits the destination once, and every `go/reports/…` lands on the new URL.
8. **Run it alone.** Dana clones the repository, starts the Directory with one command, loads the extension, and has a working `go/` on her laptop within five minutes.
9. **Move to a shared Directory.** Dana's team deploys the Directory. She exports her local links, imports them there, and changes one setting in the extension.

Scenarios 2 and 5 cannot both hold for one link with the P0 types: an integer has no range until VAR-9 (P2), so a `month` set to an integer accepts `13`. Until then, scenario 5 is shown with the regex `month` of 5.7 (implementation plan, L3).

## 4. How it works

```text
Ben types  go/reports/10/13  in Chrome's address bar
      │
      ▼
Chrome starts navigating to  http://go/reports/10/13
      │
      ▼
The extension rewrites the navigation to  {DIRECTORY_URL}/reports/10/13
(no request is ever sent to a host named "go")
      │
      ▼
The Directory resolves the path against the registered links
      ├── hit  ──▶  302 redirect to https://reports.example.com/daily?month=10&day=13
      └── miss ──▶  404 page: "Did you mean…?", what the link expects, or "create this link"
```

Vocabulary used in the rest of this document:

| Term | Meaning |
|---|---|
| **Link** | A name plus one or more routes, with an owner and a description. `go/reports` is a link. |
| **Name** | The first path segment of a link. Unique within a Directory. |
| **Route** | One pattern together with the destination template it redirects to. |
| **Pattern** | The path shape a route accepts: literals and variables separated by `/`. |
| **Variable** | A named placeholder that captures one path segment, optionally restricted by a type. |
| **Destination template** | A URL containing `{variable}` placeholders. |
| **Hit / miss** | A request that resolves to a redirect / one that doesn't. |
| **Directory URL** | Where a Directory is served: `http://localhost:<port>` locally, something like `https://go.example.com` when deployed. |

## 5. Link model

This section defines what a link is and how a request is resolved. Section 5.7 restates the rules as examples that double as acceptance tests.

### 5.1 Links and routes

A link owns its name and everything under it. It has one or more routes, each mapping a pattern to a destination:

```text
gh
  gh                        →  https://github.com/acme
  gh/new                    →  https://github.com/organizations/acme/repositories/new
  gh/{repo}                 →  https://github.com/acme/{repo}
  gh/{repo}/pr/{num:int}    →  https://github.com/acme/{repo}/pull/{num}
```

Most links have a single route, as in `idea.md`. Routes are an addition: one name often needs several shapes — a home page with no arguments, a detail page with one. Keeping every shape of a name inside one link means one owner decides how `go/gh/…` behaves, and matching never has to arbitrate between links that belong to different people.

### 5.2 Patterns

A pattern is what follows `go/`: segments separated by `/`, where each segment is either a literal or a variable.

- The first segment is a literal — the link's name. A pattern cannot begin with a variable, so no link can capture the whole namespace.
- A segment is entirely a literal or entirely one variable.
- Literals use letters, digits, `-`, `_`, and `.`, and begin with a letter or digit. They are saved in lowercase. ASCII only in v1.
- A variable is written `{name}` or `{name:type}`. Names use letters, digits, and `_`, begin with a letter, and are unique within the pattern.
- Inside `regex(…)`, the expression runs to the parenthesis that closes `regex(`, counting nested parentheses and skipping escapes and character classes. It may therefore contain `/`, `{`, `}`, and `)`; the parser must not split a pattern naively on `/`.
- Limits: 8 segments, 6 variables, and 200 characters per pattern; 20 routes per link.

### 5.3 Variable types

A variable without a type accepts any text. A type restricts what it accepts; a value outside the restriction makes the route not match.

| Type | Written as | Accepts | Priority |
|---|---|---|---|
| Text (default) | `{name}` or `{name:string}` | Any non-empty segment | P0 |
| Integer | `{name:int}` | Digits with an optional leading `-` | P0 |
| Number | `{name:number}` | An integer or a decimal such as `3.5` | P0 |
| Regular expression | `{name:regex(…)}` | A value the expression matches from start to end | P0 |
| Integer range | `{name:int(1..12)}` | An integer within the inclusive bounds; either bound may be omitted | P2 |
| One of | `{name:enum(prod,staging,dev)}` | One of the listed values, ignoring case | P2 |
| Rest of path | `{name:path}` | All remaining segments (at least one); allowed only as the last segment | P2 |
| Date, UUID | `{name:date}`, `{name:uuid}` | `YYYY-MM-DD`; a canonical UUID | P3 |

- Except for `path`, a variable captures exactly one segment. An empty value never matches.
- Values are checked after percent-decoding and passed on exactly as typed: no trimming, no case change, no reformatting. `03` stays `03`.
- Regular expressions use RE2 syntax (no backreferences or lookaround), must match the whole value whether or not they include `^` and `$`, are case-sensitive unless they opt out with `(?i)`, and are limited to 200 characters.

### 5.4 Destination templates

- A destination is an absolute `http` or `https` URL of at most 2,048 characters.
- A destination cannot carry credentials: a user name or password before the host, as in `https://user:secret@host/`. Without this rule, `https://example.com@evil.example/` would look like a link to `example.com` and open `evil.example`.
- `{name}` inserts the variable's value percent-encoded, leaving only letters, digits, `-`, `.`, `_`, and `~` unencoded. A value therefore cannot add a path segment, a query parameter, or a fragment.
- Placeholders are allowed in the path, query, and fragment — never in the scheme, host, or port. With credentials ruled out, nothing else stands before the path, so a request cannot change which site a link leads to.
- Every placeholder must name a variable declared in the route's pattern; otherwise the route cannot be saved. A declared variable that the destination doesn't use is allowed and produces a warning.
- A variable may be used more than once.
- A literal brace is written percent-encoded (`%7B`, `%7D`).
- A destination cannot be a go link or a URL on the Directory itself, which rules out redirect loops. Aliases (DIR-10) cover the legitimate case.
- With the `path` type (P2), `{+name}` inserts the captured segments with their `/` separators intact, encoding each segment individually.

### 5.5 Matching

**Resolving a request.** Given the path that follows `go/`:

1. **Split.** Ignore the query string and fragment. Strip leading and trailing slashes, split on `/`, discard empty segments, then percent-decode each segment. Decoding happens after splitting, so an encoded slash (`%2F`) stays inside its value.
2. **Find the link.** Lowercase the first segment and look up the link with that name. Case is the only thing ignored: `go/on-call` and `go/On-Call` are the same link, while `go/on-call`, `go/on_call`, and `go/oncall` are three different names. If there is no such link, the request is a miss (unknown name).
3. **Find matching routes.** A route matches when it has as many segments as the request (a trailing `path` variable absorbs any extra ones), every literal equals the request's segment at that position, ignoring case, and every variable accepts the value at its position. Values are never changed.
4. **Choose the most specific.** If several routes match, compare them segment by segment from the left. A literal beats a restricted variable (`int`, `number`, `regex`, `enum`), which beats a text variable, which beats `path`. The first difference decides. A remaining tie goes to the route listed first in the link; owners control that order.
5. **Expand and redirect.** Substitute the values into the destination template (5.4) and redirect. If no route matched, the request is a miss (known name).

**Rules enforced on save.**

- Names are unique ignoring case: `On-Call` cannot be created while `on-call` exists. `oncall` can; it is a different name.
- Within a link, two routes cannot have the same shape — the same pattern once variable names are ignored. `gh/{repo}` and `gh/{name}` collide. `gh/{repo}` and `gh/{id:int}` do not; step 4 sends numeric values to the second.
- P2: when two routes of a link could match the same request with equal specificity — two different regular expressions in the same position, for example — saving succeeds with a warning that says which route wins.

### 5.6 Misses

A request that doesn't resolve is a miss. The Directory never redirects on a guess: it answers `404` with a page that lets the user choose what to do next. There are two kinds of miss.

**Unknown name** — no link has the requested name.

- *Did you mean.* Suggest links whose name is within a small edit distance of the requested one, ignoring case. A missing or different separator counts as an edit like any other, so `go/oncall` suggests `go/on-call`. Distance is Damerau–Levenshtein: an insertion, a deletion, a substitution, or a swap of two adjacent characters each count as one edit. The allowed distance grows with the length of the requested name: 0 for 1–2 characters, 1 for 3–5, 2 for 6 or more.
- If the rest of the requested path resolves against a suggested link, the suggestion is the complete corrected request with the user's values carried over — `go/reprots/10/13` yields `go/reports/10/13` — and following it goes straight to the destination. Otherwise the suggestion opens that link's usage help.
- Order suggestions by fewest edits, then by recent usage (once INS-1 exists), then alphabetically. Show at most five.
- Always offer to create the link, prefilled with the requested path, next to a search box prefilled with the requested name.
- P2: also suggest links whose name starts with the requested name, when it is at least 3 characters long.

**Known name, no matching route** — the link exists, but the request fits none of its routes. A value that fails its variable's type lands here.

- *Usage help.* Show the link's description and routes, and for each route the reason this request didn't fit: the wrong number of segments, a literal that differs, or a variable that rejected its value — naming the variable, the value, and what the variable accepts.
- P2: include the link's launcher (VAR-5) prefilled with the values that were valid, so the user only fixes or adds what is missing.
- P2: if correcting a typo in a later literal segment would make the request resolve, offer the corrected request as "Did you mean".

The distance thresholds and the number of suggestions are configuration, so they can be tuned once rescue-rate data exists (section 10).

### 5.7 Worked examples

Given these links:

```text
reports
  reports/{month:regex(^(0?[1-9]|1[0-2])$)}/{day:int}  →  https://reports.example.com/daily?month={month}&day={day}

gh
  gh                        →  https://github.com/acme
  gh/new                    →  https://github.com/organizations/acme/repositories/new
  gh/{repo}                 →  https://github.com/acme/{repo}
  gh/{repo}/pr/{num:int}    →  https://github.com/acme/{repo}/pull/{num}

jira
  jira/{key:regex([A-Z]+-[0-9]+)}  →  https://acme.atlassian.net/browse/{key}

search
  search/{q}                →  https://search.example.com/?q={q}

on-call
  on-call                   →  https://pager.example.com/schedule
```

Requests resolve as follows:

| # | Request | Outcome | Rule it demonstrates |
|---|---|---|---|
| 1 | `go/reports/10/13` | Redirect to `https://reports.example.com/daily?month=10&day=13` | Both values pass their types |
| 2 | `go/Reports/10/13/` | Same as 1 | Literals ignore case; a trailing slash is ignored |
| 3 | `go/reports/13/01` | Miss, known name: `month` rejected `13` | A value that fails its type is a miss |
| 4 | `go/reports/10/x` | Miss, known name: `day` rejected `x` | Same |
| 5 | `go/reports/10` | Miss, known name: the route needs two values and got one | The number of segments must match |
| 6 | `go/reprots/10/13` | Miss, unknown name: "Did you mean `go/reports/10/13`?" | One edit (two swapped letters); values carried over |
| 7 | `go/gh` | Redirect to `https://github.com/acme` | A link can have a route without variables |
| 8 | `go/gh/api-server` | Redirect to `https://github.com/acme/api-server` | Text variable |
| 9 | `go/gh/new` | Redirect to the `gh/new` destination | A literal beats a variable |
| 10 | `go/gh/api-server/pr/42` | Redirect to `https://github.com/acme/api-server/pull/42` | Routes of different lengths coexist |
| 11 | `go/gh/api-server/pr/latest` | Miss, known name: `num` rejected `latest` | Type failure in a longer route |
| 12 | `go/jira/OPS-1234` | Redirect to `https://acme.atlassian.net/browse/OPS-1234` | A regex must match the whole value |
| 13 | `go/jira/ops-1234` | Miss, known name: `key` rejected `ops-1234` | Values are case-sensitive and never changed |
| 14 | `go/search/a%20b%26c` | Redirect to `https://search.example.com/?q=a%20b%26c` | The value is decoded to `a b&c` and re-encoded; it cannot add a parameter |
| 15 | `go/On-Call` | Redirect to `https://pager.example.com/schedule` | Names ignore case |
| 15a | `go/oncall` or `go/on_call` | Miss, unknown name: "Did you mean `go/on-call`?" | `-` and `_` are part of the name; one edit away |
| 16 | `go/xyzzy` | Miss, unknown name, no suggestions: offer to create `go/xyzzy` | Nothing is within the allowed distance |
| 17 | `go/` | Directory home page | Empty path |
| 18 | `go/gh/api-server?tab=readme` | Redirect to `https://github.com/acme/api-server?tab=readme` | Query passthrough (P2, RES-8) |

Saving behaves as follows:

| Attempt | Outcome |
|---|---|
| Add route `gh/{name}` to `gh` | Rejected: same shape as `gh/{repo}` |
| Add route `gh/{id:int}` to `gh` | Saved: numeric values now go to this route, all others still go to `gh/{repo}` |
| Create a link named `On-Call` | Rejected: the name is taken by `on-call` |
| Create a link named `oncall` | Saved: a different name from `on-call` |
| A user who cannot edit `gh` adds `gh/issues` | Rejected: the message names the owner and links to `go/gh` |
| Destination `https://{team}.example.com/` | Rejected: variable in the host |
| Destination `https://example.com/{year}` on pattern `reports/{month}` | Rejected: `{year}` is not declared |
| Destination `javascript:alert(1)` | Rejected: scheme not allowed |
| Destination `https://example.com@evil.example/` | Rejected: credentials are not allowed |

## 6. Functional requirements

Priorities map to the milestones in section 11:

| Priority | Meaning |
|---|---|
| **P0** | Local MVP — needed for the product to work end to end for one person |
| **P1** | Team release — needed before a team relies on a shared deployment |
| **P2** | Next — high-value follow-ups |
| **P3** | Later — worth doing, not scheduled |

**Origin** says where a requirement comes from: *Idea* is stated or directly implied in `idea.md`; *Added* is proposed by this document.

### 6.1 Link management

| ID | Requirement | Priority | Origin |
|---|---|---|---|
| DIR-1 | Create a link from one form: a pattern, a destination template, and an optional description. The link's name is the pattern's first segment; the creator becomes its owner. | P0 | Idea |
| DIR-2 | Validate while the user types: pattern syntax, name availability, destination rules (5.4), and route conflicts (5.5). Each problem is explained next to the field it concerns; a conflict links to the existing link. Errors block saving; warnings don't. | P0 | Added |
| DIR-3 | Edit a link: any route's pattern or destination, variable types, and the description. Renaming a link renames the first segment of all its routes. A saved change affects resolution within 5 seconds. | P0 | Idea |
| DIR-4 | Add, remove, and reorder a link's routes. Entering a pattern whose name belongs to an existing link adds a route to that link if the user may edit it; if not, the form says who owns it and links to it. | P0 | Added |
| DIR-5 | Delete a link, after a confirmation that shows how recently it was used. | P0 | Added |
| DIR-6 | Link page: name, description, owner, routes with their destinations, variables with their types, a launcher (VAR-5), and buttons that copy the short form (`go/…`) and the full Directory URL. | P0 | Added |
| DIR-7 | The creation form can open prefilled with a pattern (from a miss page) or a destination (from the extension popup). | P0 | Added |
| DIR-8 | When a new route's destination is already the destination of another link, show that link before saving. | P2 | Added |
| DIR-9 | A deleted link can be restored for 30 days by its owner or an admin; its name stays reserved during that time. | P2 | Added |
| DIR-10 | Aliases: additional names for a link. Renaming a link keeps the old name working as an alias. | P3 | Added |

### 6.2 Variables in the web UI

| ID | Requirement | Priority | Origin |
|---|---|---|---|
| VAR-1 | Routes support named variables with the P0 types of section 5.3, validated on every resolution. | P0 | Idea |
| VAR-2 | The pattern field shows variables as distinct tokens and marks a syntax error at the character where it occurs. | P0 | Idea |
| VAR-3 | A variables table below the pattern lists each variable with a type selector and the editor that fits the type — an expression field for a regex, bounds for a range, a value list for `enum`. Editing the table rewrites the pattern and the reverse, so nobody has to hand-write type syntax. | P0 | Idea |
| VAR-4 | The regex editor checks sample values as the author types, using the server's engine, and shows which samples pass. | P0 | Added |
| VAR-5 | Launcher: a form with one field per variable, validated against its type as the user types, showing the resulting `go/…` link and the final URL, with an Open button. It appears on the link page and as the live preview in the create and edit forms. | P0 | Added |
| VAR-6 | Each variable can have a description and an example value. The example must satisfy the variable's type. Both appear in the launcher, in search results, and in usage help. | P1 | Added |
| VAR-7 | The destination field offers the route's variables when the author types `{`. | P2 | Added |
| VAR-8 | Test box in the create and edit forms: enter any `go/…` path and see which route it resolves to and why, or why it misses. | P2 | Added |
| VAR-9 | Types: integer ranges, `enum`, and `path` with `{+name}` expansion. | P2 | Added |
| VAR-10 | Types: `date` and `uuid`. | P3 | Added |
| VAR-11 | Build a route from a real URL: paste a destination and click a part of it to turn that part into a variable. | P3 | Added |

### 6.3 Search and browse

| ID | Requirement | Priority | Origin |
|---|---|---|---|
| SRCH-1 | Search as you type across names, descriptions, destinations, and owners. | P0 | Idea |
| SRCH-2 | Ranking: exact name, then name prefix, then misspelled name (the distance rules of 5.6), then matches in other fields. Ties go to the more recently used link once INS-1 exists. | P0 | Added |
| SRCH-3 | Home page: the search box has focus on load, above lists of the user's own links and recently added links. A most-used list joins them with INS-1. | P0 | Added |
| SRCH-4 | A result shows the link's name, routes with variables highlighted, description, and owner. Enter opens the link page; a link without variables can be opened directly. | P0 | Added |
| SRCH-5 | Tags: free-form labels on links, searchable and clickable to browse. | P2 | Added |
| SRCH-6 | Filters: owner (including "mine"), tag, has variables, unused for N days. | P2 | Added |
| SRCH-7 | Keyboard shortcuts: `/` focuses search, arrow keys move through results, Enter opens, `c` starts a new link. | P2 | Added |

### 6.4 Resolver

| ID | Requirement | Priority | Origin |
|---|---|---|---|
| RES-1 | `GET {DIRECTORY_URL}/<path>` resolves `<path>` as defined in 5.5. A hit responds `302` with the expanded destination. | P0 | Idea |
| RES-2 | A miss — an unknown name, no matching route, or a value that fails its type — responds `404` with the miss page (6.5). The resolver never redirects to a guessed link. | P0 | Idea |
| RES-3 | Redirect responses are not cacheable, so a browser never reuses an old redirect after a link changes. | P0 | Added |
| RES-4 | Values are percent-decoded once, validated, and percent-encoded when inserted (5.4). | P0 | Added |
| RES-5 | `go/` with no path opens the Directory home page. | P0 | Added |
| RES-6 | Resolution works at `{DIRECTORY_URL}/<path>` without the extension — on phones, in other browsers, from scripts — and when a DNS or hosts entry points the hostname `go` at the Directory. The web UI and API therefore live under a reserved prefix (section 9). | P0 | Added |
| RES-7 | A JSON endpoint returns the resolver's decision for a path without redirecting: the matched route and destination, or the kind of miss with suggestions and per-route reasons. The launcher, the test box, and the extension use it, so the rules have one implementation. | P0 | Added |
| RES-8 | Query passthrough: parameters on the go link are appended to the destination. A parameter the template already sets is not overridden. | P2 | Added |
| RES-9 | If the database becomes unreachable, hits are still served from memory; edits fail with a clear message. | P2 | Added |
| RES-10 | Preview: a link name followed by `+` (`go/reports+`) opens the link page instead of redirecting, so a user can inspect a link before following it. | P2 | Added |

### 6.5 Miss page

| ID | Requirement | Priority | Origin |
|---|---|---|---|
| MISS-1 | Unknown name: show "Did you mean" suggestions per 5.6, each with the link's description and destination, carrying over the values the user typed. | P0 | Idea |
| MISS-2 | Known name with no matching route: show usage help per 5.6, stating in plain language why the request fit none of the routes. | P0 | Idea |
| MISS-3 | Offer "Create `go/<typed path>`", which opens the creation form prefilled, and a search box prefilled with what was typed. | P0 | Added |
| MISS-4 | Keyboard-first: the first suggestion has focus, Enter follows it, and the digits 1–5 pick a suggestion. | P1 | Added |
| MISS-5 | Count whether a miss ended with a followed suggestion, to measure the rescue rate (section 10). | P1 | Added |
| MISS-6 | Usage help includes the launcher, prefilled with the values that were valid. | P2 | Added |
| MISS-7 | Prefix suggestions and later-segment typo suggestions (5.6). | P2 | Added |

### 6.6 Chrome extension

| ID | Requirement | Priority | Origin |
|---|---|---|---|
| EXT-1 | Send every tab navigation aimed at `http://go/<path>` or `https://go/<path>` to `{DIRECTORY_URL}/<path>` instead, keeping the path and query. This covers links typed in the address bar, clicked on a page, opened from bookmarks, and opened from other apps, and it works although no host named `go` exists. | P0 | Idea |
| EXT-2 | Settings page for the Directory URL, defaulting to the local Directory's address. "Test connection" confirms the URL answers as a Directory and shows its version; an invalid URL cannot be saved. | P0 | Added |
| EXT-3 | First-run page that has the user type `go/` and press Enter. This proves the setup works, teaches Chrome that `go` is a site, and is where the trailing-slash fallback is explained (risk R1). | P0 | Added |
| EXT-4 | Minimum permissions: access to the `go` host and the configured Directory only. No access to other sites, no content scripts, no browsing history, no remotely loaded code. | P0 | Added |
| EXT-5 | When the Directory cannot be reached, show a page that names the configured URL and offers Retry and Settings, instead of the browser's connection error. | P1 | Added |
| EXT-6 | Warn in settings when the Directory URL is unencrypted `http://` on a host other than the local machine. | P1 | Added |
| EXT-7 | Address-bar suggestions: in the extension's keyword mode, list matching links from the Directory as the user types; Enter opens the selection. The keyword must not be plain `go` (Appendix B). | P2 | Added |
| EXT-8 | Toolbar popup: search the Directory, and "Create a go link for this page", which opens the creation form with the current tab's URL as the destination. | P2 | Added |
| EXT-9 | Central configuration: an administrator can preset and lock the Directory URL through Chrome's enterprise policy. | P3 | Added |
| EXT-10 | Additional or alternative short hosts such as `g/`, for networks where `go` is already taken. | P3 | Added |
| EXT-11 | More than one Directory, tried in order — a personal one first, then the team's. | P3 | Added |
| EXT-12 | Opt-in: turn plain-text `go/…` on chosen sites into clickable links. | P3 | Added |

### 6.7 Accounts, ownership, and permissions

| ID | Requirement | Priority | Origin |
|---|---|---|---|
| AUTH-1 | Every link has an owner, initially its creator. Local mode has one built-in user who owns every link and never signs in. | P0 | Idea |
| AUTH-2 | In deployed mode, users sign in through the organization's identity provider using OpenID Connect, limited to configured email domains. An account is created at first sign-in. | P1 | Added |
| AUTH-3 | Only a link's owner, its co-owners, and admins can edit or delete it. Every signed-in user can view, search, and use every link. | P1 | Idea |
| AUTH-4 | Two roles: user and admin. Admins can edit, delete, restore, and reassign any link, and change roles. The first admins are named in configuration. | P1 | Added |
| AUTH-5 | Signing in doesn't lose the request: an unauthenticated `go/reports/10/13` continues to its destination after sign-in. | P1 | Added |
| AUTH-6 | Whether resolving a link requires sign-in is a deployment setting, on by default (Q3). | P1 | Added |
| AUTH-7 | An owner can transfer a link to another user and add or remove co-owners. | P1 | Added |
| AUTH-8 | Alternative sign-in: trust an identity header set by an authenticating reverse proxy. | P2 | Added |
| AUTH-9 | Links whose owner has been deactivated are listed for admins and can be claimed by another user. | P3 | Added |
| AUTH-10 | Group ownership, mapped from identity-provider groups. | P3 | Added |

### 6.8 Usage, history, and upkeep

| ID | Requirement | Priority | Origin |
|---|---|---|---|
| INS-1 | Count uses per route per day. Each link shows total uses, uses in the last 30 days, and when it was last used. | P1 | Added |
| INS-2 | Keep a history of every change to a link — who, when, before, and after — visible on the link page, with the ability to revert to an earlier version. | P1 | Added |
| INS-3 | Export all links as JSON. Import with a dry run that reports what would be created, skipped as conflicting, or rejected as invalid. | P1 | Added |
| INS-4 | Admin reports: the most-missed names (demand for links that don't exist), links unused for 180 days, and links without an active owner. | P2 | Added |
| INS-5 | Periodically check the destinations of routes without variables and tell owners about those that no longer respond. | P3 | Added |

### 6.9 API

| ID | Requirement | Priority | Origin |
|---|---|---|---|
| API-1 | A versioned JSON API covers everything the web UI does; the web UI is built on it. | P0 | Added |
| API-2 | Personal access tokens for scripts: shown once, stored hashed, revocable, scoped read-only or read-write. | P2 | Added |
| API-3 | The Directory serves a machine-readable description of its API (OpenAPI). | P2 | Added |

## 7. Local and deployed modes

| | Local | Deployed |
|---|---|---|
| Purpose | One person's launcher; development and demos | A team's or company's shared directory |
| Start | One command; creates its database on first run | Container image configured through environment variables |
| Directory URL | `http://localhost:<port>` | An HTTPS URL such as `https://go.example.com` |
| Reachable from | The local machine only, by default | Wherever the host exposes it, behind TLS |
| Sign-in | None; one built-in user with admin rights | Organization single sign-on |
| Storage | SQLite file in a local data directory | SQLite on a persistent volume, or Postgres (Q1) |
| Extension | Loaded unpacked; default settings work | Packaged build; Directory URL set by the user or by policy (EXT-9) |

| ID | Requirement | Priority | Origin |
|---|---|---|---|
| DEP-1 | Local mode starts with one documented command and needs nothing installed beyond the language runtime or Docker. It creates its database on first run and listens on a fixed default port, which is also the extension's default. The port number is chosen at implementation and should avoid ports that development servers commonly use. | P0 | Idea |
| DEP-2 | Following the README, a new user gets from cloning the repository to a working `go/` link in 5 minutes, including loading the extension. | P0 | Added |
| DEP-3 | Local mode listens on the loopback interface only unless configured otherwise, because it has no sign-in. | P0 | Added |
| DEP-4 | Local and deployed modes are the same build. Configuration selects the behavior: public base URL, sign-in mode and provider settings, database location, admin emails. Missing or invalid configuration stops startup with a message that names the setting. | P1 | Idea |
| DEP-5 | The deployed build is a container image with no dependency on a particular cloud. It listens on a configurable port and expects TLS to be terminated in front of it. | P1 | Added |
| DEP-6 | Database schema changes are applied automatically at startup. | P1 | Added |
| DEP-7 | Health and readiness endpoints, structured request logs, and basic metrics: requests, hits, misses, latency. | P1 | Added |
| DEP-8 | A documented backup and restore procedure. Export and import (INS-3) serve as a portable backup and as the path from a local Directory to a deployed one. | P1 | Added |
| DEP-9 | A way to distribute the extension to the team that doesn't require loading it unpacked (Q6). | P1 | Added |
| DEP-10 | Optional sample links (`--seed`), so a new local install has something to try. | P2 | Added |
| DEP-11 | Several instances can serve the same database; an edit reaches every instance within 5 seconds. | P2 | Added |

## 8. Non-functional requirements

| ID | Area | Requirement |
|---|---|---|
| NFR-1 | Performance | A hit takes at most 20 ms of server time at the 95th percentile, measured with 100,000 links. |
| NFR-2 | Performance | A miss page, including its suggestions, is produced within 200 ms at the 95th percentile. |
| NFR-3 | Performance | Search results return within 150 ms at the 95th percentile. |
| NFR-4 | Performance | The extension's redirect is a declarative browser rule, not code that runs on each navigation. It adds no perceptible delay and doesn't depend on the extension's background process being awake. |
| NFR-5 | Capacity | One modest instance (1 vCPU, 512 MB) serves 100,000 links at 200 resolutions per second. |
| NFR-6 | Reliability | Deployed resolution is available 99.9% of each month. |
| NFR-7 | Reliability | A saved change survives a crash or restart; no acknowledged write is lost. |
| NFR-8 | Security | User-supplied regular expressions run only on a linear-time engine with RE2 semantics, so no expression can stall the resolver. |
| NFR-9 | Security | Destinations are limited to `http` and `https`. `javascript:`, `data:`, `file:`, and every other scheme are rejected. |
| NFR-10 | Security | A request can never change a destination's scheme or host (5.4). A value of `.` or `..` is rejected when its placeholder sits in the destination's path, so a request cannot climb out of the templated path. |
| NFR-11 | Security | Names, descriptions, and requested paths are untrusted input, escaped wherever they are rendered. A content security policy forbids inline script. |
| NFR-12 | Security | State-changing requests are protected against cross-site request forgery. Session cookies are `Secure`, `HttpOnly`, and `SameSite`. |
| NFR-13 | Security | Deployed mode rate-limits sign-in, link creation, and API writes. |
| NFR-14 | Security | Deployed mode serves the UI and API over HTTPS only. Secrets and tokens never appear in logs; tokens are stored hashed. |
| NFR-15 | Security | Every create, edit, delete, and ownership change is attributable to a user and a time. |
| NFR-16 | Privacy | Usage is stored as counts per route per day. Nothing records which user used which link; the only per-user usage data is a last-active date, used to count active users. |
| NFR-17 | Privacy | Variable values and full missed paths are not stored. A miss records only the lowercased requested name, and resolver logs record the matched link instead of the raw path. |
| NFR-18 | Privacy | The extension collects no telemetry and contacts no server other than the configured Directory. |
| NFR-19 | Compatibility | Extension: Manifest V3, on the current Chrome stable release and the two before it, on macOS, Windows, Linux, and ChromeOS. Other Chromium browsers on a best-effort basis. |
| NFR-20 | Compatibility | Web UI: current Chrome, Edge, Firefox, and Safari on desktop. Miss pages and link pages are usable at phone widths. |
| NFR-21 | Accessibility | The web UI and the extension's pages meet WCAG 2.2 AA and are fully operable by keyboard. |
| NFR-22 | Maintainability | The link model — parsing, validation, matching, expansion, suggestions — is one module with one implementation, covered by table-driven tests that include every example in section 5.7. Clients get validation and previews through the API (RES-7) instead of re-implementing the rules. |

## 9. Proposed architecture

This section is a recommendation, not a requirement. The stack was an open question (Q4) when it was written; it has since been decided (section 13), with type-checked JavaScript in place of TypeScript for the extension.

```text
┌──────────────────────┐                          ┌─────────────────────────────────────────┐
│ Chrome               │  go/reports/10/13        │ Directory (one process)                 │
│                      │  rewritten by the        │                                         │
│  Extension (MV3)     │  extension to            │  /<path>        resolver → 302 or 404   │
│   · redirect rule    │  {DIRECTORY_URL}/…       │  /-/…           web UI                  │
│   · settings page    │ ───────────────────────▶ │  /-/api/v1/…    JSON API                │
│   · first-run page   │                          │                                         │
│                      │ ◀─────────────────────── │  link model: parse · match · expand ·   │
└──────────────────────┘  302 to the destination  │              suggest (in-memory index)  │
                                                  │  store: SQLite or Postgres              │
                                                  └─────────────────────────────────────────┘
```

**Design decisions**

- **Resolve on the server.** The extension only reroutes navigations; all matching happens in the Directory. That gives one implementation of the rules, edits that take effect at once, links that work without the extension, and usage data without extra plumbing. The cost is that the Directory must be reachable for links to work (risk R8).
- **Resolver at the root, everything else under `/-/`.** A link name must begin with a letter or digit, so `/-/` can never collide with one. The Directory can then sit directly behind a host named `go`, and link URLs stay as short as possible.
- **One link-model module.** Parsing, validation, matching, expansion, and suggestions live in one library whose tests include every example in section 5.7. The web UI and the extension ask the API instead of carrying their own copy.
- **In-memory index.** Routes are compiled once, regular expressions included, and held in memory keyed by lowercase name. The index is rebuilt when a link changes.
- **Recommended stack.** Go for the Directory: its standard regular-expression engine is RE2, which satisfies NFR-8 with no extra dependency, and a single static binary with the web assets embedded makes "run locally" and "deploy anywhere" the same artifact. SQLite by default, Postgres as an option. TypeScript for the extension. The web UI can be server-rendered with light scripting; nothing in this document needs a heavy client framework.

**URL space**

| Method and path | Purpose |
|---|---|
| `GET /<path>` | Resolve and redirect, or render the miss page |
| `GET /-/` | Web UI home |
| `GET /-/api/v1/resolve?path=` | The resolver's decision as JSON (RES-7) |
| `GET /-/api/v1/links?q=` | Search and list |
| `POST /-/api/v1/links` | Create a link |
| `GET`, `PATCH`, `DELETE /-/api/v1/links/{id}` | Read, update (routes included), delete |
| `POST /-/api/v1/links/validate` | Validate a draft without saving (DIR-2) |
| `GET /-/api/v1/suggest?q=` | Short result list for the extension (EXT-7, EXT-8) |
| `GET /-/api/v1/export`, `POST /-/api/v1/import` | Export and import (INS-3) |
| `GET /-/api/v1/meta` | Product identifier and version, for the extension's connection test (EXT-2) |
| `GET /-/healthz` | Health check |

**Data model**

| Entity | Main fields | Notes |
|---|---|---|
| User | Email, display name, role, status | One built-in user in local mode |
| Link | Name (lowercase, unique), description, owner, co-owners, tags, created, updated, deleted | |
| Route | Link, position, pattern, shape (unique within the link), destination template | Position is the tie-break order of 5.5 |
| Variable | Route, name, type, restriction, description, example | |
| Revision | Link, editor, time, before and after | INS-2 |
| Usage | Route, day, hit count | No per-user or per-value data (NFR-16, NFR-17) |
| Miss | Lowercased requested name, day, count, rescued count | Feeds INS-4 and the rescue rate |
| Access token | User, hash, scope, last used | API-2 |

## 10. Success metrics

| Metric | Definition | Target |
|---|---|---|
| Time to first redirect | From opening the README to a working `go/` link, for a new user in local mode | 5 minutes or less |
| Registration time | Median time from opening the creation form to saving | 60 seconds or less |
| Hit latency | Server time for a redirect, 95th percentile | 20 ms or less |
| Miss rate | Misses ÷ all resolutions | Track; should fall as the Directory fills |
| Suggestion coverage | Unknown-name misses that showed at least one suggestion ÷ all unknown-name misses | Track |
| Rescue rate | Misses where the user followed a suggestion ÷ misses that showed one | 50% or more (starting hypothesis) |
| Variable adoption | Share of links with at least one variable, and share of resolutions that go through them | Track |
| Active use (deployed) | Users resolving at least one link in a week; resolutions per active user | Track |
| Directory health | Share of links used in the last 90 days | Track |

The rescue-rate target is a guess made without data. Reset it, and set targets for the tracked metrics, after the first four weeks of real use.

## 11. Release plan

| Milestone | Scope | Done when |
|---|---|---|
| **M0 — Feasibility spike** | Confirm the Chrome behaviors in Appendix B on current Chrome stable with a throwaway extension. Decide Q4 (stack) and Q5 (name normalization). | Each item in Appendix B is confirmed, or the design is adjusted |
| **M1 — Local MVP** | All P0 requirements | The P0 examples in section 5.7 pass as automated tests, and DEP-2 holds on a clean machine |
| **M2 — Team release** | All P1 requirements; Q1, Q2, Q3, and Q6 decided | A deployed Directory with single sign-on is in daily use by a first team, whose links were migrated by export and import |
| **M3 — Next** | P2 requirements, ordered by what M2 usage shows (miss reports, rescue rate) | — |
| **Later** | P3 requirements | — |

## 12. Risks

| # | Risk | Mitigation |
|---|---|---|
| R1 | **Chrome searches instead of navigating.** Chrome treats `go/foo` as ambiguous until it knows `go` as a site, so a first-time user may land on a search results page and conclude the product is broken (Appendix B). | The first-run step that has the user type `go/` (EXT-3); documentation that a trailing slash or `http://` always navigates; confirmation in M0. If that proves insufficient: an opt-in rule that sends a search for `go/…` at the default search engine to the Directory, at the cost of a broader permission. |
| R2 | **HTTPS upgrades.** Chrome may rewrite `http://go/…` to `https://go/…` before the redirect rule sees it. | The rule matches both schemes (EXT-1); tested in M0. |
| R3 | **`go` is already taken** on a company network by an existing go-link system. | The extension overrides it only for users who install it; EXT-10 allows a different short host. |
| R4 | **Extension distribution.** Store review takes time and scrutinizes host permissions; loading unpacked doesn't scale beyond developers. | Minimum permissions (EXT-4); choose the channel early (Q6); EXT-9 for managed fleets. |
| R5 | **Trusted short links as a phishing vector.** `go/payroll` pointing somewhere hostile is more convincing than a raw URL. | Sign-in to create, a visible owner, change history (INS-2), admin takedown (AUTH-4), scheme and host rules (5.4), preview with `+` (RES-10). Later: allow and deny lists for destination domains. |
| R6 | **Hostile or careless regular expressions** slow down every request. | Linear-time engine and a length limit (NFR-8). |
| R7 | **Authors can't predict which route wins.** | Deterministic precedence (5.5), shape conflicts rejected on save, overlap warnings, the test box (VAR-8). |
| R8 | **The Directory sits on the path of everyday navigation.** When it is down, every go link is down. | Availability target (NFR-6), hits served from memory when the database is down (RES-9), an unreachable page that says what is wrong (EXT-5). |
| R9 | **The Directory decays** into stale, duplicate, and ownerless links. | Usage shown on every link (INS-1), reports (INS-4), the duplicate-destination hint (DIR-8), ownership transfer and claiming (AUTH-7, AUTH-9). |
| R10 | **Encoding mistakes** break destinations or let a value alter a URL's structure. | One implementation (NFR-22), encoding by default (5.4), tests with hostile values. |
| R11 | **Matching rules are hard to change after launch.** A later change to name matching or precedence can make existing links collide or change meaning. | Q5 was settled in M0; treat section 5 as a versioned contract backed by tests. |

## 13. Open questions

| # | Question | Recommendation | Needed by |
|---|---|---|---|
| Q1 | Where is the deployed Directory hosted? This decides storage (a persistent disk for SQLite, or managed Postgres), TLS termination, and how the single sign-on callback is reached. | Any container host with a persistent volume; start on SQLite and add Postgres only if the host has no persistent disk. | M2 |
| Q2 | Which identity provider, and which email domains? | OpenID Connect covers Google Workspace, Microsoft Entra ID, and Okta; confirm which one to test against first. | M2 |
| Q3 | Does resolving a link require sign-in when deployed? | Yes if the Directory is reachable from the internet — otherwise link names and internal URLs leak and the host becomes an open redirector. It can be off on a private network. | M2 |
| Q4 | Which stack? | **Decided 2026-10-13: Go.** SQLite for storage; type-checked JavaScript for the extension (implementation plan, section 2). | M0 |
| Q5 | Should `-` and `_` be ignored in names, so that `on-call` and `oncall` are the same link? | **Decided 2026-10-13: no.** Names ignore case only. `on-call`, `on_call`, and `oncall` are different links, and "Did you mean" covers the near-misses. | M0 |
| Q6 | How does the extension reach users of a deployed Directory: a public or unlisted Chrome Web Store listing, or enterprise force-install? | An unlisted store listing for a first team; force-install with EXT-9 for a company-wide rollout. | M2 |
| Q7 | Is every link visible to every signed-in user? This document assumes so. Private or team-only links would touch search, resolution, and the miss page, which must not reveal hidden names. | Keep everything visible in v1. | M2 |
| Q8 | What is the product called? "GoLinks" is an existing vendor's registered trademark. | **Decided 2026-10-13: Ogre, for the whole product.** Its two parts are the Ogre server, which this document calls the Directory, and the Ogre extension. | M2 |

## Appendix A. Additions to the original idea

| Area | In `idea.md` | Added by this document | Why |
|---|---|---|---|
| Link model | One pattern redirects to one URL | Several routes per link; precedence, conflict, and name-matching rules; encoding rules for values | One name needs several shapes, and matching has to be deterministic and safe |
| Variable types | Optional types such as number and regex | `int` and `number` as separate types; ranges, `enum`, rest-of-path; descriptions and examples | Ranges and lists cover the common cases without writing a regex |
| Misses | "Did you mean" by edit distance; a type mismatch is a miss | Usage help that explains why a known link didn't match; carrying values into suggestions; creating a link from a miss; prefix suggestions | A miss is where users give up |
| Directory web | Register, search, edit; rich variable support | Live validation, launcher, regex tester, test box, link page, tags and filters, duplicate hint | Authors need to see what a link will do before saving it |
| Extension | `go/…` in the address bar | Settings for the Directory URL, first-run check, minimum permissions, unreachable page, address-bar suggestions, popup | Local and deployed setups need configuration, and Chrome's first-use behavior needs handling |
| Access | "Their links" | Sign-in for deployed mode, owners, co-owners, admins, transfer | Ownership needs identity |
| Trust and upkeep | — | Usage counts, change history, export and import, admin reports, link preview with `+` | A shared directory decays without them |
| Platform | Runs locally and deployed | Links that work without the extension, JSON API, health checks, backups, safety rules for regexes, schemes, and caching | Needed to run it as a service |

## Appendix B. Chrome behavior this design depends on

Checked against Chromium source and Chrome's extension documentation on 2026-10-13. Confirm on current Chrome stable in M0.

1. **Address-bar classification.** Chrome classifies `go/foo` — a host without a dot, no scheme, no trailing slash — as ambiguous, and may run a search instead of navigating. It prefers navigation once the exact URL has been visited or the host `go` has been typed before. `go/`, `go/foo/`, and `http://go/foo` are always treated as URLs. Hence EXT-3.
2. **Redirect mechanism.** Manifest V3's `declarativeNetRequest` API can redirect with a regular-expression rule, here `^https?://go/(.*)` to `{DIRECTORY_URL}/\1`. The rule must list `main_frame` explicitly, needs host permission for `*://go/*`, and silently does nothing if the substituted URL is invalid — the reason EXT-2 refuses to save an invalid Directory URL.
3. **HTTPS upgrades.** Chrome can upgrade an `http://` navigation to `https://` before loading it. In the M0 spike (headless Chromium 156) `http://go/` was upgraded only with "Always use secure connections" at its strict level, where the extension then sees `https://go/`; with default settings and at the public-sites level it was not upgraded. By hand on Chrome 155 stable, go links reached the Directory with no warning at both levels, and the setting is off in a fresh profile. The rule therefore matches both schemes.
4. **Keyword mode.** An extension keyword of plain `go` captures ordinary searches that begin with "go " ("go fund me"). The GoLinks extension hit this and moved its keyword to `go/`. EXT-7 must use a keyword nobody types by accident.

Not settled by documentation when this PRD was written. The M0 spike has since checked all five on macOS only, and none failed there. All but the second were confirmed by hand on Chrome 155 stable; the second rests on headless Chromium, with a resolver switch standing in for a real `go` host, and its "every desktop platform" is still open: Windows, Linux, and ChromeOS were not tried. In the first, the extension held access to `go` and to the Directory, not to `go` alone. Results are in [spike-chrome.md](spike-chrome.md):

- A `go/…` link clicked on a third-party page is redirected with host permission for `*://go/*` alone.
- The redirect fires before DNS resolution of `go` on every desktop platform, including on a network where `go` does resolve.
- A fragment (`go/foo#section`) survives the redirect chain.
- After the first-run step, `go/foo` typed without a trailing slash navigates instead of searching.
- A previously used `go/…` link is offered by the address bar's autocomplete (by hand: one link, typed minutes earlier; its position in the list was not recorded, and inline completion was not seen in the automated run).

## Appendix C. Prior art

| Tool | How it handles variable links |
|---|---|
| Trotto (open source) | Positional `%s` placeholders: `go/gh/%s` |
| GoLinks (commercial) | `{*}` placeholders, with optional sections and default values |
| Glean go links | `{*}` placeholders |
| Tailscale golink (open source) | Self-hosted with SQLite; only a link's owner can edit it, and ownership can be transferred; reachable as `http://go/` through the tailnet's DNS, with no extension |

As far as the documentation reviewed shows, variables in these tools are positional and untyped. Named variables with types that are validated on every request are this product's differentiator.

## Sources

- Chromium address-bar classification: [autocomplete_input.cc](https://github.com/chromium/chromium/blob/main/components/omnibox/browser/autocomplete_input.cc), [history_url_provider.cc](https://github.com/chromium/chromium/blob/main/components/omnibox/browser/history_url_provider.cc)
- Chrome extensions: [declarativeNetRequest reference](https://developer.chrome.com/docs/extensions/reference/api/declarativeNetRequest), [Replace blocking web request listeners](https://developer.chrome.com/docs/extensions/develop/migrate/blocking-web-requests)
- HTTPS upgrades and single-label hosts: [WHATWG fetch review thread, October 2023](https://lists.w3.org/Archives/Public/public-webapps-github/2023Oct/0390.html)
- GoLinks on the `go` keyword: [A Tale of Two Browsers](https://www.golinks.com/blog/go-links-contributes-to-open-source-a-tale-of-two-browsers/)
- Trotto: [Programmatic go links](https://docs.trot.to/use/programmatic-go-links)
- Glean: [Create variable go links](https://docs.glean.com/user-guide/knowledge/go-links/create-variable-go-links)
- Tailscale: [golink](https://github.com/tailscale/golink)
