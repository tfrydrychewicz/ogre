spec.json - the worked examples of docs/PRD.md section 5.7, as data
====================================================================

The PRD ends its link model with a table of requests and a table of save
attempts that "double as acceptance tests". spec.json is that section
transcribed, row for row, so the same expectations can be replayed at three
levels without being retyped: against the link model (spec_test.go in this
directory's package), against the application (Resolve and the use cases) and
through HTTP (GET /-/api/v1/resolve and the /links endpoints). When the PRD
table changes, change this file and nothing else.

A test that replays the file must fail on a row it does not understand
(decode with unknown fields disallowed), so a new kind of row cannot be
skipped by accident.

Top level
---------
format, version   "ogre-spec", 1.
source            Where the rows come from.
links             The five links every row starts from.
requests          PRD table "Requests resolve as follows", rows 1 to 18.
saves             PRD table "Saving behaves as follows", nine rows.

links[]
-------
name              The link's name (the first segment of every pattern).
description       Not in the PRD; added so a suggestion has one to show.
routes[]          pattern, destination. The order is the route order.

Load them by saving each one through the code under test (ValidateDraft,
CreateLink, POST /links), not by writing to the store: creating the fixture
links is itself part of the test.

requests[]
----------
id                The PRD row number. Row 15a names two requests and is
                  split into "15a.1" and "15a.2".
request           The request as the PRD writes it, for people.
path              The same without "go/": the raw, still percent-encoded
                  path, exactly as the resolver receives it after the first
                  slash and as GET /resolve?path= takes it (URL-encode it
                  again to put it in that query string). May carry a
                  trailing slash or a query string; "" is the home page.
rule              The rule the row demonstrates, from the PRD.
expect            The outcome (see below).
pending           Present when the PRD outcome needs a requirement that is
                  not built yet; the value names it ("RES-8"). A test then
                  asserts untilThen instead of expect and says that it
                  skipped the row. When the requirement is built, delete
                  pending and untilThen.
untilThen         What the row answers until then, same form as expect.

expect.kind is one of:

  "home"          The path is empty.
  "hit"           link (name), route (canonical pattern of the route that
                  won), values (variable -> value as typed, after decoding;
                  {} for a route without variables), destination (the
                  Location to redirect to).
  "miss_known"    link (name), reasons: one entry per route of the link, in
                  route order, each with route (canonical pattern), code
                  ("segment_count", "literal_mismatch", "value_rejected",
                  "value_not_allowed") and, for the value codes, variable
                  and value. A key that is absent must be empty in the
                  result. The message's wording is not part of the fixture;
                  a test may check that it mentions the variable and the
                  value.
  "miss_unknown"  requestedName (lowercase), suggestions: the complete list
                  in order, each with name, path (the corrected request
                  with the user's values carried over; equal to name when
                  the rest does not resolve), edits, resolves, description
                  and, when resolves is true, destination. An empty list
                  means there must be no suggestion.

saves[]
-------
id                "S1" to "S9", in the PRD table's order.
attempt, outcome  The PRD's two columns, for people.
needs             The lowest level that can decide the row:
                  "linkmodel"  ValidateDraft alone decides it.
                  "app"        It needs stored links or a second user. At
                               link-model level only this much holds: the
                               draft passes ValidateDraft, and the link's
                               name is expect.name.
action            "create" (a new link) or "update" (replace the routes of
                  the existing link named by "link").
link              For "update": the name of the link being edited.
actor             "owner": the user who owns the fixture links.
                  "other": a user who is not allowed to edit them.
draft.routes[]    pattern, destination: the whole route list submitted. For
                  an update, give each route whose pattern equals a stored
                  route's pattern that route's id, as an editor would.
expect.saved      Whether the save succeeds.
expect.name       The link's name when the draft is valid.
expect.error      When not saved: "validation" (HTTP 422),
                  "link.name_taken" (409) or "link.forbidden" (403).
expect.problems   For "validation": every error, in order, each with route
                  (index into draft.routes), field ("pattern" or
                  "destination"), code, and optionally mentions (text the
                  message must contain). Warnings are not listed and must
                  not make a save fail.
expect.existing   For "link.name_taken": the name of the link that holds
                  the name.
expect.namesOwner, expect.linksTo
                  For "link.forbidden": the message must name the link's
                  owner and point to the link with this name.
expect.then[]     For a successful save: requests (path, expect) that must
                  resolve as given once it is saved.

Every save row starts from the five fixture links, not from the result of
the row before it. Validation comes first: a row that expects "validation"
expects it whether or not the name is free (S7 uses the name of an existing
link, as the PRD does).

Values in a hit are data a user typed. A test may compare them; nothing may
log them (NFR-17).
