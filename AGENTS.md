# Piper — Agent Guide Index

Piper is a Go pipeline-orchestration server (master) with a React frontend
embedded in the same binary, plus workers that connect to the master over a
single outbound tunnel.

This file only indexes role-specific guides. Do not duplicate their content
here — read the relevant guide when you start work in that area.

- **Frontend work** (anything under `frontend/`): read
  `docs/frontend/develop.md` before making changes.
- **Backend work** (Go code, server, worker, scheduler, etc.): read
  `docs/backend/develop.md` before making changes.
- **Before signing off** any feature or fix as verified: follow
  `docs/qa/adversarial-qa-playbook.md`. Unit/e2e tests and a single-worker
  manual smoke test do not satisfy this — the playbook specifically requires
  registering multiple worker infrastructure types (`baremetal`, `docker`,
  `k8s`) at once, submitting through the real rendered UI forms rather than
  hand-built API payloads, and cross-checking server logs rather than trusting
  API success responses alone.

## Cross-Role Integration Review

Before reporting a change that touches more than one role's area as done:

1. Re-check every touched path against the owning role's guide
   (`docs/frontend/develop.md` and/or `docs/backend/develop.md`) for
   compliance — don't assume it still matches after edits.
2. If the change is user-visible, verify it with
   `docs/qa/adversarial-qa-playbook.md`.
3. If either step surfaces a violation or gap, write it up as an audit
   finding and fix it before reporting completion — do not report "done"
   with known drift outstanding.
