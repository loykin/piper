# Runtime and Credential Policy

This document records the current policy decisions for worker runtime controls
and credential deletion.

## Worker Runtime Policy

Piper follows a progressive restriction model:

- If a worker has no policy, workloads are allowed.
- If a policy exists, it is applied as a baseline/default.
- Workload manifests win over baseline defaults unless a future constraint mode
  explicitly says otherwise.

This keeps local development and early deployments usable while still allowing
operators to add controls incrementally. A future hardening mode can add
`require_worker_policy` or a constraint/enforce mode that rejects workloads
which violate policy.

Kubernetes worker pod policy remains a baseline pod template. It is not a
security constraint system yet.

## Docker Runtime Policy

Docker workers currently use Docker's default execution model plus Piper's
existing worker-local controls:

- Notebook Docker volumes are selected from the worker's configured allowlist.
- Notebook Docker containers drop all capabilities by default and set
  `no-new-privileges`.
- Pipeline and serving Docker drivers apply workload `cpus`, `mem_limit`,
  `shm_size`, and GPU device reservations.

Dangerous runtime options should be introduced progressively:

- first as warning/audit signals,
- then as opt-in deny rules,
- then as enforce mode for hardened multi-tenant deployments.

Do not store Docker runtime policy in `worker_pod_policies`. If Docker runtime
policy becomes admin-managed, use a separate kinded schema such as
`worker_runtime_policies(worker_id, kind, policy_json)`.

Worker network invariants still apply: dispatch-time policy is handled through
the existing master-to-worker tunnel, and workers must not expose a new
control-plane endpoint.

## Credential Delete Policy

Credential delete is hard delete by default.

Deleting a credential removes the credential metadata row and relies on the
`credential_values` foreign key cascade to remove encrypted value history. This
keeps secret material out of the active database after deletion.

Audit requirements should be handled by separate events that record who deleted
which credential and when. Audit events must not contain credential values.
