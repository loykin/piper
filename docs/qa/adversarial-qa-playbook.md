# Adversarial QA Playbook

Read this before doing a pre-release QA pass on Piper. It is not a code
review checklist and it is not "run the test suite and confirm it's green."
It is a separate, distinct activity: **assume something is broken and try to
prove it**, working only from what a real user would see (the UI, the API,
the docs) — not from knowledge of how a feature was implemented.

## Why this exists

Code that has already gone through implementation, unit tests, e2e tests,
and separate-session review can still fail on the primary, first-button
flow of a major feature — including things like insecure defaults,
missing safety checks on destructive actions, or a launch form that fails
every time it's used. Confirmatory testing routinely misses this class of
bug, no matter how many times it's repeated. What catches it is a live,
adversarial, click-through-the-UI pass against a real deployment.

The reason prior testing misses it: unit tests and "e2e" tests are
written by whoever (human or AI) just implemented the feature, or by a
reviewer working from the same implementation-shaped mental model. Both
share the same blind spot — they confirm the case the author already knows
is correct, not the cases the author never considered. "e2e" describes
*execution realism* (does it hit real code, a real database, a real
container runtime), not *input-space coverage* — a test can be fully
"real" in that sense and still only ever try inputs the author already
believes are valid. That's why this has to be a genuinely different
activity, not "more of the same testing, done again."

If you're the one implementing a feature, or reviewing it for correctness,
do not run this playbook in the same sitting — you'll unconsciously test
what you already believe works. Come back to it later, or hand it to a
context that doesn't know the implementation details, working only from
the running system and its public interfaces.

## Before you start

- Get a **real, persistent, deployed instance** — not a fresh ephemeral CI
  fixture built from a clean, minimal, already-correct manifest. Ephemeral
  clean environments hide exactly the class of bug this playbook exists to
  find (missing manifests, stale build artifacts, a runtime driver that only
  works against the fixture's narrow input shape). Reuse or stand up a real
  deployment (`deploy/k8s/*.yaml` applied to a real cluster is a good target;
  a local `piper server` also works).
- Make sure you have **log access**, not just API access — `kubectl logs`
  on the server pod, or the server's stdout if running locally. A
  successful-looking API response does not prove the underlying operation
  actually completed (see step 4).
- Each Piper installation owns exactly one `runtime.type` (`baremetal`,
  `docker`, or `k8s`) — there is no multi-worker registration to set up
  anymore, but that also means a pass against only one runtime tells you
  nothing about the other two. **Run the full playbook once per
  `runtime.type`**, each against its own separately configured instance
  (three passes, not one) — a bug in the Docker or Kubernetes driver is
  invisible from a baremetal-only pass, and vice versa.

## The procedure

Work through every page reachable from the sidebar as a first-time user
would — not by inspecting the code first. For each page:

### 1. Actually use the UI, not the API

For every "create," "launch," or "deploy" form, fill it out and submit
through the rendered form itself — don't construct the equivalent API
payload by hand. Hand-built payloads carry your own assumptions about what
fields matter; the form only carries what the form's author remembered to
expose. If the form is missing a field the backend actually requires, only
submitting through the real form will show you that.

Leave every field that looks optional at its default. Don't pre-fill
things you assume are needed unless the form itself prompts for them.

### 2. Probe `placement.runtime` mismatches and manifest-vs-driver drift

There is no dispatch ambiguity to create anymore — an installation owns
exactly one runtime, so there is only ever one candidate. What replaces it:

- Submit a manifest with `driver.placement.runtime` set to a value that
  does **not** match the installation's configured `runtime.type` (e.g.
  submit `runtime: k8s` against a `docker` installation). Confirm it is
  rejected before dispatch, with a clear error — not silently ignored, and
  not accepted and then stuck.
- Submit a manifest with `driver.placement.worker` or `driver.placement.label`
  set to any non-empty value. Both must be rejected outright — there is no
  worker to route to anymore, so a manifest carrying either field is always
  wrong, never merely suboptimal.
- Submit a manifest whose driver sub-block doesn't match the active runtime
  (e.g. a `driver.k8s` block on a `docker`-runtime installation). Confirm
  the extra block is either rejected or cleanly ignored — not silently
  misinterpreted as if it were the active runtime's config.

### 3. Test every degenerate/zero state you can construct

For anything that can be listed, counted, or configured, deliberately try
the empty/missing/zero case:

- `runtime.type` left empty or set to a value the config loader doesn't
  recognize — confirm the server refuses to start with a clear error
  rather than falling back to some implicit default
- Zero projects (including deleting the last one that exists)
- Security-relevant configuration left unset (auth signing key, encryption
  key, credentials)
- Any field that behaves like an enum (`driver.placement.runtime`, driver
  type) submitted empty or with a value that doesn't match any case the
  code branches on

These are the states an implementer is least likely to have manually
tried, because by the time a feature is being built, the "happy path"
inputs are already the ones in front of them.

### 3b. If testing via an automated browser tool, verify polling separately

Automated browser tabs (e.g. an MCP-driven browser pane) frequently report
`document.visibilityState: "hidden"` and `document.hasFocus(): false` even
when they're the only/active tab — because they never receive real OS/browser
focus. React Query's `refetchInterval` pauses by default while the document
is hidden (`refetchIntervalInBackground` defaults to `false`), so a page that
polls correctly for a real user can look completely frozen in this kind of
automated session. Before filing "no live update" as a bug, check the source
for `backgroundPolling()`/`refetchInterval` on the relevant hook — if it's
present, this is very likely a test-environment artifact, not a real defect.
Confirm with `document.hidden`/`document.hasFocus()` via the browser tool's
JS-eval action before concluding either way.

### 3c. A click on a Base UI Select/Tabs/etc. that "does nothing": retry, then try keyboard

This app's interactive primitives (`Select`, `Tabs`, and others from
`@loykin/designkit`, wrapping `@base-ui/react`) rely on pointer-capture-based
interaction handling. Both this browser tool's click action and a raw JS
`dispatchEvent` of `pointerdown`/`mousedown`/`pointerup`/`mouseup`/`click`
intermittently fail to register against them — a Select option can appear to
render and be "clicked" with no effect, or a `TabsTrigger` click can silently
no-op on the first attempt and work on an identical second attempt. Two live
examples from a real pass: a Select's `onValueChange` never fired despite three
different click strategies, but `ArrowDown`+`Enter` (keyboard) worked first try;
a `TabsTrigger` click did nothing on attempt 1 and switched tabs correctly on
an immediate, identical attempt 2.

Before filing a "click does nothing" bug against one of these components:
1. Retry the exact same click once — a silent first-attempt failure followed by
   a working retry means the interaction itself is fine.
2. For a Select specifically, if retry doesn't resolve it, drive it with real
   keyboard input instead (`ArrowDown`/`ArrowUp` then `Enter`) through the same
   tool's key-press action. Keyboard succeeding means this is very likely the
   automation gap, not an app defect — note it as such rather than as a
   confirmed finding, and recommend one manual real-mouse click-through to
   fully rule it out.

### 4. Don't trust a success response — check the log too

A `200`/`201` or "success" API response only tells you the request was
*accepted*, not that the underlying operation *completed* or *failed
cleanly*. Cross-check server-side logs for the expected terminal event
(e.g., a run reaching `failed` or `completed`, not stuck at `running`
forever). A dispatch or provisioning step can fail deep inside a runtime
driver or inside internal error-handling and never propagate that failure
back to the record the user sees — which looks identical to "still
working" from the API alone.

### 5. Go past the list view into every detail/comparison view

List pages tend to render fine even when something is badly wrong,
because they often show minimal derived data. Click into every detail,
comparison, or drill-down view a list page links to — several real
crashes only appeared once real data reached a more complex rendering
path that a list view never exercises.

### 6. Check that the frontend and backend actually agree

For any API response the frontend consumes, compare the *raw* network
response (not what you assume it looks like) against what the frontend's
type declarations expect. Do this even if nothing visibly crashes yet —
a shape mismatch that happens to not crash today can still be silently
wrong (missing data, wrong values shown) and will definitely crash later
once the data that exposes the mismatch actually shows up.

### 7. Confirm you're actually testing what would ship

Before concluding a feature works: confirm the artifact you're testing
against is not stale relative to source. Check that a built frontend
bundle's last commit isn't meaningfully older than the frontend source
it's supposed to reflect, and that any deployed image digest actually
matches the current commit's published image. A feature can be completely
correct in source and still not exist in what a user actually receives.

## Recording findings

Write findings to a dated file under `docs/` (e.g.
`docs/adversarial-qa-YYYY-MM-DD.md`). Put a scannable, severity-ranked
backlog table at the top — that's the part anyone will actually reference
later; the narrative detail below it is for whoever fixes the bug. For
each finding, include:

- What you did and what you expected vs. what happened
- The actual evidence (log lines, network responses) — not just a
  description of the symptom
- Whether the same pattern is likely to recur elsewhere in the codebase
  (grep for the same code shape in sibling features — copy-pasted
  patterns tend to carry the same gap into every copy)

## Closing the loop

For any finding that reveals a *repeatable class* of bug rather than a
one-off — a missing `default` case in a switch that exists in multiple
files, an ignored error return, a manifest that's never applied by CI —
don't stop at documenting it. Propose (or add) a mechanical, permanent
check that makes the same class of bug impossible to reintroduce silently:
a linter rule (e.g., Go's `exhaustive` for unhandled switch cases,
`errcheck` for discarded errors), a CI job that exercises the real
deployment artifacts, or a structural refactor (e.g., a single registry
for driver-specific validation instead of the same hand-written logic
copied across features) that removes the place the bug could hide in the
first place.

The point of this playbook is to find what testing has missed *so far* —
not to become the only thing standing between a bug and a release, forever,
by hand, every time. Anything found once should never need to be found
this way again.
