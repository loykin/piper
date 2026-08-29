# Log/Metric Storage Backend

> Status: implemented, live-verified (this document records the as-built
> architecture, not a proposal).
>
> Live-verified: 2026-08-29, against Elasticsearch 8.15.0, ClickHouse 24.8,
> and InfluxDB 2.7.12 running as local Docker containers.

## 1. Summary and goals

Piper records two kinds of high-volume, append-only telemetry for every
pipeline run: **step logs** (stdout/stderr lines) and **run metrics**
(`PIPER_METRIC key=value` markers and structured step metrics). By default
both are written to the same relational database (SQLite or Postgres) that
holds Piper's operational state — projects, credentials, runs, schedules.

That default is fine for light use, but it does not scale with training
workloads: an ML training step that logs per-epoch progress can produce
hundreds of log lines and dozens of metric points per run, and a busy
installation runs many such pipelines concurrently. Every one of those rows
lands in the same `logs`/`run_metrics` tables and the same connection pool
that `projects`, `credentials`, and `schedules` depend on for correctness —
there is no way to apply downsampling, compaction, or index rollover to that
data without touching the OLTP tables it shares a database with. Systems
like Kubeflow and SageMaker separate metrics/log storage from their control
plane for exactly this reason; Piper had no equivalent until
`pkg/statsstore` was added.

`pkg/statsstore` decouples log/metric storage from the OLTP database: an
installation can point `stats.logs.url` and `stats.metrics.url` at a
purpose-built store (Elasticsearch, ClickHouse, or — metrics only —
InfluxDB) while every other part of Piper keeps using SQLite/Postgres
unchanged. Selecting an external backend is entirely optional and
per-Member; an installation that never sets these URLs keeps using the
primary database exactly as before.

Goals:

- Let log/metric volume grow independently of the OLTP database's size and
  connection budget.
- Give each backend's native retention/rollover/compaction mechanism (ILM,
  TTL, bucket retention) ownership of aging out old rows, instead of a
  hand-rolled `DELETE ... WHERE ts < ?` sweep against a relational table.
- Keep writes durable across a backend outage (network blip, backend
  restart, misconfiguration) without blocking pipeline execution or losing
  data.
- Change nothing about how the rest of Piper reads/writes logs and metrics —
  the switch is behind one interface pair, not threaded through the
  scheduler, queue, or REST handlers.

Non-goals: this is not a general observability/APM pipeline. It stores
exactly the two record shapes Piper already had (`LogLine`, `MetricPoint`),
scoped by `project_id`/`run_id`/`step_name`; it is not a place to route
arbitrary structured events.

## 2. Package boundary and architecture

`pkg/statsstore` is structurally parallel to `pkg/storage` (Piper's artifact
store abstraction): a small set of backend-neutral interfaces, plus a
URL-scheme-based `Open()` factory that picks a concrete adapter.

```
pkg/statsstore/
  statsstore.go        LogBackend, MetricBackend, Purger, Capabilities, Store
  open.go               Open(config, fallback) — URL-scheme factory, mirrors storage.Open(url, token)
  elasticsearch.go      elasticsearch[+https]:// adapter (logs + metrics)
  clickhouse.go         clickhouse[+https]://    adapter (logs + metrics)
  influxdb.go           influxdb[+https]://      adapter (metrics only)
  http_common.go        shared HTTP client, auth header construction, credential lookup
  spool.go              disk-backed durability queue (diskSpool)
  spooled_backend.go     wraps any LogBackend/MetricBackend with spool-and-retry
  cursor.go             opaque pagination cursors, filter-fingerprinted
  ids.go                event ID generation (uuid)
```

### 2.1 Interfaces

```go
type LogBackend interface {
    AppendLogs(ctx context.Context, lines []LogLine) error
    QueryLogs(ctx context.Context, query LogQuery) (LogPage, error)
}

type MetricBackend interface {
    AppendMetrics(ctx context.Context, points []MetricPoint) error
    QueryMetrics(ctx context.Context, query MetricQuery) (MetricPage, error)
}

type Purger interface {
    PurgeProject(ctx context.Context, projectID string) error
    PurgeRun(ctx context.Context, projectID, runID string) error
}
```

`Store` bundles a `LogBackend`, a `MetricBackend`, `Capabilities`, and a
shared `Close()`/`Health()` — logs and metrics are independently
configurable (different backend, different URL, different credential), but
when they resolve to the *same* backend (same URL + credential), `Open()`
opens one physical client and uses it for both, so a combined
Elasticsearch/ClickHouse deployment does not pay for two connections.

### 2.2 `Purger` is deliberately not part of `LogBackend`/`MetricBackend`

`Purger` is a separate, optional interface. Backends implement it (all three
adapters do), but nothing in the retention-sweep path is allowed to call it.
The only callers are explicit, operator- or user-initiated actions: project
deletion and run deletion (`PurgeProjectStats`/run cleanup in
`memberclient_local.go`). This separation exists so that a future retention
mechanism can never accidentally short-circuit into "delete this project's
data" — the type signature makes that impossible without deliberately
type-asserting to `Purger`, which only the two real delete flows do.

### 2.3 `Open()` — URL-scheme factory

```go
func Open(config Config, fallback Fallback) (*Store, error)
```

mirrors `pkg/storage`'s `storage.Open(url, token)`: given a
`stats.logs.url`/`stats.metrics.url`, it parses the scheme
(`elasticsearch[+https]`, `clickhouse[+https]`, `influxdb[+https]`) and
constructs the matching adapter. When both URLs are empty it returns the
supplied `Fallback` untouched — no spool, no extra goroutine, just the
relational backend Piper already had. When either URL is set, `Open()`
requires `SpoolDir` and wraps the resolved backend(s) in a `spooledBackend`
(§6) before returning.

`ValidateBackendURL(kind, rawURL)` runs before any credential resolution or
adapter construction:

- Rejects userinfo in the URL (`elasticsearch://user:pass@host/...`) —
  credentials must go through `credential_ref`, never the URL string, so a
  URL can be logged or displayed without redaction logic.
- Rejects query parameters whose key contains `token`, `password`, `secret`,
  or `api_key` — same reasoning, applied to query strings.
- Rejects unsupported schemes, including `loki://`, which is intentionally
  unsupported (see §10, "next steps").

## 3. The append-only design

Every backend interaction in normal operation is a write (`AppendLogs`/
`AppendMetrics`) or a read (`QueryLogs`/`QueryMetrics`) — nothing in the hot
path ever deletes a row. Two things are allowed to remove data, and both are
explicit:

1. **`Purger.PurgeProject`/`PurgeRun`** — invoked only when a human or the
   API deletes a project or a run outright. `internal/logstore.Backend` (the
   relational fallback) and all three external adapters implement this by
   issuing a real delete (`_delete_by_query` for Elasticsearch, `ALTER TABLE
   ... DELETE` for ClickHouse, a predicate delete for InfluxDB).
2. **Each backend's own retention mechanism** (§7) — Elasticsearch ILM,
   ClickHouse `TTL`, InfluxDB bucket retention rules — configured once at
   `Open()` time when `ManageRetention` is set, then left to the backend's
   own background process. Piper does not poll and issue row-level deletes
   against these backends; that would defeat the entire purpose of choosing
   a backend with native TTL/rollover support in the first place. (The
   relational fallback is the one exception: `SQLiteLogStore`/`PgStore`
   implement `SweepLogs`/`SweepMetrics`, a real polling sweep, because a
   relational table has no native TTL primitive to delegate to. `piper.go`'s
   `cleanupStats` calls this only when the active `p.logs`/`p.metrics` is
   the relational store — the moment an external backend is configured, the
   sweep's type assertion to `logstore.LogRetention` fails harmlessly and
   the external backend's own native mechanism is the only thing removing
   old rows.)

No code path issues a `DELETE FROM logs WHERE run_id = ?` for the common
"a run finished, keep or discard its logs" question — that's exactly the
`RunTTL`/`ArtifactTTL` retention the rest of Piper has for run rows and
artifacts, and it is deliberately decoupled from log/metric retention (see
`piper.go`'s `cleanupStats` doc comment: "It never consults run existence,
RunTTL, or schedule max_runs").

## 4. Backend selection and configuration

```yaml
stats:
  spool:
    dir: ./piper-data/stats-spool
    max_bytes: 1073741824
  logs:
    url: "elasticsearch://es-host:9200/piper"
    credential_ref: "stats-elasticsearch"
    retention: 720h
    manage_retention: true
  metrics:
    url: "clickhouse://ch-host:8123/piper"
    credential_ref: "stats-clickhouse"
    retention: 720h
    manage_retention: true
```

- `stats.logs` and `stats.metrics` are independent `StatsBackendConfig`
  values — logs can go to Elasticsearch while metrics go to ClickHouse (or
  InfluxDB), or both can point at the same backend, or either/both can stay
  empty and fall back to the primary database. This is not tied to
  `server.db.driver` in any way.
- `credential_ref` names a **system-project** generic credential (created
  via `POST /api/system/credentials`, resolved through
  `credentialStore.Resolve(ctx, project.SystemID, ref)` in `piper.go`) whose
  key/value pairs become the adapter's auth material: `token` (bearer/API
  key), `username`/`password` (basic auth), or backend-specific keys like
  InfluxDB's `org`/`org_id`. Because the credential must already exist by
  the time `piper.New` runs `statsstore.Open`, at a brand-new installation
  the operator creates the credential first (server running with no
  `stats.*.url` set yet, or with `manage_retention`/`url` deferred), then
  sets `stats.*.url`/`credential_ref` and restarts.
- Retention (`retention`, `manage_retention`) is per-backend, not global —
  see §7.
- When both `stats.logs.url` and `stats.metrics.url` are empty (the
  out-of-the-box default), `Open()` returns the SQLite/Postgres-backed
  fallback unchanged; nothing about this feature is opt-out, it's opt-in.

## 5. The three adapters

All three speak plain HTTP through `http_common.go`'s shared `httpBackend`
(15s client timeout, `Authorization` header built from the resolved
credential, `ApiKey`/`Bearer`/`Token`/basic-auth as appropriate). None of the
adapters use a backend-specific client SDK — this keeps the dependency
footprint small and makes the wire behavior directly inspectable with
`curl`, which is also how this document's live verification was cross-checked
(§8).

| | Elasticsearch | ClickHouse | InfluxDB |
|---|---|---|---|
| Logs | yes | yes | no |
| Metrics | yes | yes | yes |
| Full-text log search | yes (`match` query on `line`) | yes (`positionCaseInsensitive`) | n/a |
| Metric key filter | yes | yes | yes |
| Write path | `_bulk` NDJSON, `refresh=wait_for` | `INSERT ... FORMAT JSONEachRow` | line protocol, `/api/v2/write` |
| Read path | `_search` with a `bool.filter` term/range query | `SELECT ... FINAL ... FORMAT JSON` | Flux query via `/api/v2/query` |
| Native retention | ILM policy + index template (§7.1) | `TTL` clause (§7.2) | bucket retention rule (§7.3) |
| Idempotent replay | `_id` = `event_id` (upsert on retry) | `ReplacingMergeTree` keyed on `(project_id,run_id,step_name,id,event_id)` + `FINAL` reads | `id`/`event_id` carried as fields, deduplicated client-side by the spool layer |

Elasticsearch and ClickHouse can serve **both** logs and metrics from one
`Open()` call when `stats.logs.url == stats.metrics.url` (same credential
too) — they use two indices/tables (`<base>-logs`/`<base>-metrics`,
`piper_logs`/`piper_run_metrics` by default, both overridable via
`?logs_table=`/`?metrics_table=` on the ClickHouse URL). InfluxDB is
metrics-only; pointing `stats.logs.url` at an `influxdb://` scheme is
rejected by `ValidateBackendURL`.

## 6. Spool / durability behavior

`spooled_backend.go`'s `spooledBackend` wraps whichever real backend(s)
`Open()` resolved, whenever an external URL is configured (never for the
relational fallback — no double-buffering a database that's already
transactional). Every `AppendLogs`/`AppendMetrics` call:

1. Assigns each record a monotonically increasing ID and an `event_id`
   (`diskSpool.assignLogs`/`assignMetrics`), persisting the sequence to a
   `sequence` file so IDs survive a restart.
2. Writes the batch as one atomically-renamed JSON file under
   `stats.spool.dir` (`diskSpool.put` — write to a `.tmp` file, `fsync`,
   `rename`, `fsync` the directory).
3. Signals a background goroutine (`spooledBackend.loop`, also polled every
   2s) to flush.

The flush loop calls the real backend's `AppendLogs`/`AppendMetrics` for
each still-pending spool file, in order, and only deletes (`ack`s) a file
once the real backend accepted it. A crash between steps 2 and the ack
reprocesses the same file on restart — delivery is at-least-once, and
`event_id`-based idempotency (upsert on Elasticsearch, `ReplacingMergeTree`
on ClickHouse) makes replay safe.

**Reads merge live backend state with pending spool records** — a query in
`QueryLogs`/`QueryMetrics` fetches from the real backend *and* scans
matching un-flushed spool files, deduplicates by `event_id`, and returns the
union. This is what makes a record visible through the REST API
immediately after `Append`, even before the background flush loop has run —
verified live in §8.3.

`Capabilities`/`Health` (exposed at `GET
/api/projects/{id}/stats/capabilities`) report:

- `Healthy`/`Degraded` — `Degraded` is true whenever the last flush attempt
  failed *or* there is unflushed spool data (`pending_bytes > 0`), even if
  the backend is currently reachable and the failure was transient.
- `PendingBytes` — the spool's on-disk size right now.
- `LastError` — a backend-agnostic string (`"statistics backend
  unavailable"` or `"statistics spool is full"`), never the raw backend
  error text (which could leak internal hostnames/credentials into a
  user-facing API response).

`ErrSpoolFull` is returned synchronously from `Append*` when
`stats.spool.max_bytes` would be exceeded — the write fails loudly rather
than growing the spool unbounded, and `Capabilities.Degraded` reflects it
the same way a backend outage does.

## 7. Retention

Retention is configured per-backend (`stats.logs.retention`/
`stats.metrics.retention`, each with its own `manage_retention` flag) and is
**decoupled from run/project deletion** — a run can be deleted from Piper's
own tables while its logs/metrics continue to exist in the external backend
until the backend's own retention window elapses, and vice versa.

`ManageRetention` controls one specific thing: whether Piper hands the
backend a retention *policy* (an ILM policy, a `TTL` clause, a bucket
retention rule) at `Open()` time. It does **not** control whether the
backend's schema (index template / database and tables) gets created —
schema setup always happens, regardless of `ManageRetention` (this was a
real bug; see §8.2).

### 7.1 Elasticsearch — ILM

When `manage_retention: true` and `retention > 0`, `openElasticsearch`
`PUT`s an ILM policy (`<index>-retention`) with a single `delete` phase at
`min_age: <retention>s`, and attaches it via `index.lifecycle.name` in the
index template. **This deletes the entire index once its age exceeds the
retention window — not individual documents by their own timestamp.**
Piper uses one fixed, non-rolling index name per backend
(`<base>-logs`/`<base>-metrics`, no time-based rollover alias), so once that
index is old enough, ILM deletes *all* logs/metrics in it together,
including ones written moments before deletion. This is standard
Elasticsearch ILM behavior for a non-rollover index and is confirmed live
(§8.4) — it is not a bug, but it means Elasticsearch retention here behaves
more like "keep at most one index's worth of history, bounded by index age"
than "delete rows individually past their own retention age." An operator
who wants true per-document aging on Elasticsearch should be aware of this
before relying on it for compliance-grade deletion; see §10 for the
rollover-alias improvement this implies.

### 7.2 ClickHouse — `TTL`

When `manage_retention: true` and `retention > 0`, the log/metric tables get
a `TTL toDateTime(ts) + INTERVAL <n> SECOND` clause (set at `CREATE TABLE`
time, and re-applied via `ALTER TABLE ... MODIFY TTL` so an existing table's
retention can be changed by restarting with a new value). This *is*
per-row: ClickHouse expires individual rows once their own `ts` ages past
the interval. The expiry is enforced during ClickHouse's background merge
process, not immediately — a row can persist past its nominal TTL until a
merge touches its part. `OPTIMIZE TABLE ... FINAL` forces this and was used
to confirm the behavior live (§8.4); production deployments rely on
ClickHouse's normal background merge cadence instead.

### 7.3 InfluxDB — bucket retention

When `manage_retention: true`, `openInfluxDB` sets (or patches, if the
bucket already exists) the bucket's `retentionRules` to a single `expire`
rule at `everySeconds: retention.Seconds()`. This was validated at the
package level against a live InfluxDB 2.7 container (§8.1) but not through
the full Piper server end-to-end pass in this round (§8.5).

## 8. What's live-verified (this round)

All of the following was exercised against real, locally-run Docker
containers — not mocks — as part of this pass:

- **Elasticsearch 8.15.0** (`docker.elastic.co/elasticsearch/elasticsearch:8.15.0`,
  single-node, security disabled)
- **ClickHouse 24.8** (`clickhouse/clickhouse-server:24.8`, with an
  authenticated non-default user — the official image disables network
  access for the `default` user unless `CLICKHOUSE_USER`/`CLICKHOUSE_PASSWORD`
  is set)
- **InfluxDB 2.7.12** (`influxdb:2.7`, package-level only)

### 8.1 Package-level integration tests

`go test ./pkg/statsstore/... -run Docker -v` — `TestDockerElasticsearchIntegration`,
`TestDockerClickHouseIntegration`, `TestDockerInfluxDBIntegration` — all pass
against the live containers above (append, query, cursor pagination,
full-text search, key-filtered metric query, `PurgeProject`). These had
never been run before this pass, per the task background, and surfaced two
of the three real bugs below.

### 8.2 Real bugs found and fixed

1. **ClickHouse TTL on a `DateTime64` column** — `openClickHouse`'s TTL
   clause was `TTL ts + INTERVAL <n> SECOND` where `ts` is
   `DateTime64(9,'UTC')`. ClickHouse rejects this outright: *"TTL expression
   result column should have DateTime or Date type, but has
   DateTime64(9, 'UTC')"* — so `CREATE TABLE`/`ALTER TABLE ... MODIFY TTL`
   failed whenever `manage_retention: true` was combined with `retention >
   0` — i.e. ClickHouse retention never actually worked against a real
   server, in the one configuration it exists to support. **Fix**: downcast
   with `toDateTime(ts)` before adding the interval.
2. **ClickHouse JSON output quoting 64-bit integers as strings** —
   ClickHouse's `FORMAT JSON` quotes `Int64`/`UInt64` values as JSON strings
   by default (to protect JavaScript float precision), so
   `json.Unmarshal` into `LogLine.ID`/`MetricPoint.ID` (`int64`) failed with
   *"cannot unmarshal string into Go struct field ...id of type int64"* on
   every real query — this was invisible to the existing unit test because
   its mock server hand-wrote unquoted JSON. **Fix**: pass
   `output_format_json_quote_64bit_integers=0` on every query request.
3. **Schema/mapping setup incorrectly gated behind `ManageRetention`** —
   both `openElasticsearch` and `openClickHouse` only created their index
   template / database+tables inside `if manage { ... }`. This conflated
   two unrelated concerns: "does Piper own this backend's retention policy"
   and "does this backend have the schema it needs to function at all."
   Two distinct, serious failure modes resulted, both reproduced live with
   `manage_retention: false` (a valid, documented configuration — an
   operator who wants to manage ES/ClickHouse retention themselves, or
   simply hasn't decided yet):
   - **Elasticsearch**: with no index template, Elasticsearch's dynamic
     mapping inferred `project_id`/`run_id`/`step_name`/`stream` as analyzed
     `text` fields (with a `.keyword` sub-field) instead of `keyword`. The
     standard analyzer tokenizes on `-`, so a hyphenated UUID `run_id` gets
     split into multiple tokens on write. Every `term`/`range` filter
     `QueryLogs`/`QueryMetrics` builds — including the exact-match `run_id`
     filter every single query uses — then silently stopped matching.
     Writes kept succeeding (`AppendLogs` returned no error); reads through
     the real REST API returned an empty page every time. Confirmed live:
     submitted a real pipeline run through `POST
     /api/projects/default/runs`, watched 8 log lines land in the real
     Elasticsearch index via a direct `curl` against `_search`, and got
     `[]` back from `GET .../steps/train/logs` — a genuinely broken feature
     that unit tests never caught because the mock server in
     `TestElasticsearchAdapterBulkSearchAndCredential` never exercised
     dynamic mapping.
   - **ClickHouse**: with no `CREATE DATABASE`/`CREATE TABLE`,
     `AppendMetrics` failed outright — `Database piper_e2e does not exist` —
     every single time, for the lifetime of the process (the spool layer
     retried indefinitely, correctly reporting `degraded: true`, but the
     retry could never succeed since nothing ever created the table).
     Confirmed live the same way: `GET .../runs/{id}/metrics` returned
     `{"code":"stats_backend_unavailable", ...}` and the server log showed
     `statistics delivery degraded reason="statistics backend unavailable"`
     the moment the pipeline's metrics tried to land.

   **Fix**: schema setup (index template with correct `keyword` mappings;
   `CREATE DATABASE`/`CREATE TABLE IF NOT EXISTS`) now runs unconditionally
   on every `Open()`. Only the retention *policy* itself (the ILM policy
   attached via `index.lifecycle.name`; the `TTL` clause and its `ALTER
   TABLE ... MODIFY TTL`) stays conditional on `manage_retention`. Note this
   only prevents the bug going forward — an index that was already created
   under the old dynamic mapping is not retroactively fixed (Elasticsearch
   does not support changing a field's type in place); an operator who hit
   this needs to reindex or delete and recreate the affected index.

   Regression coverage: `pkg/statsstore/adapters_test.go`'s
   `TestSchemaSetupIsUnconditionalOnManage` asserts both adapters create
   their schema with `manage=false` and skip the retention policy;
   `piper_test.go`'s existing
   `TestExternalStatsBackendReceivesRuntimeWritesThroughDurableIngress` was
   updated (its mock server now answers the now-unconditional index
   template request) rather than left encoding the old, buggy assumption.

### 8.3 End-to-end pass through a running Piper server

With the fixes above, a real `piper server` process (baremetal runtime,
trusted local-dev auth) was started with:

```yaml
stats:
  logs:    { url: "elasticsearch://localhost:19200/piper-e2e" }
  metrics: { url: "clickhouse://localhost:18123/piper_e2e", credential_ref: "stats-clickhouse" }
```

- Created the `stats-clickhouse` credential via the real
  `POST /api/system/credentials` endpoint (system-project generic
  credential, `username`/`password`) — exercising `credential_ref`
  resolution through the actual REST API, not a hand-built config.
- Submitted a real pipeline (`POST /api/projects/default/runs` with a
  `sh -c` step that echoes several log lines and three `PIPER_METRIC`
  markers) — the same request shape the frontend's run-submission flow
  sends.
- `GET /api/projects/default/runs/{id}/steps/train/logs` returned all 8 log
  lines (Piper's own "running command" log plus the step's stdout/stderr),
  matching a direct `curl` against Elasticsearch's `_search` API for the
  same index.
- `GET /api/projects/default/runs/{id}/metrics?step=train` returned all 3
  metric points, matching a direct `curl`/HTTP query against ClickHouse for
  the same table.
- `GET /api/projects/default/stats/capabilities` reported
  `healthy: true, degraded: false, pending_bytes: 0` in steady state.

### 8.4 Retention, live

- **ClickHouse**: inserted a row with `ts` one hour in the past against a
  table opened with `retention: 5s, manage_retention: true`, then issued
  `OPTIMIZE TABLE ... FINAL` (forcing the merge that applies TTL
  immediately instead of waiting for ClickHouse's own background
  schedule) — the row was gone afterward. TTL deletion works.
- **Elasticsearch**: opened a backend with `retention: 5s, manage_retention:
  true`, lowered the cluster's `indices.lifecycle.poll_interval` to `1s`
  (production default is 10 minutes) to make the test tractable, wrote one
  document, and polled `_ilm/explain` — the index moved from ILM phase
  `new` to phase `delete` at ~6s of age and the index itself (not just its
  documents) was gone by ~11s. ILM deletion works, confirming the
  whole-index-deletion behavior described in §7.1.

### 8.5 Spool durability — degrade and recover

With the same running server: `docker stop`ped the ClickHouse container
mid-flight, submitted another pipeline run. Observed:

- Server log: `statistics delivery degraded reason="statistics backend
  unavailable"`.
- `GET .../stats/capabilities`: `degraded: true, pending_bytes: 685,
  last_error: "statistics backend unavailable"`.
- `GET .../runs/{id}/metrics?step=train` **still returned all 3 metric
  points** — served from the spool via the merged read path (§6), through
  the same REST API a client would normally use, with the backend fully
  unreachable.

`docker start`ed ClickHouse back up:

- Within one flush-loop cycle: server log printed `statistics delivery
  recovered`; `GET .../stats/capabilities` returned to
  `healthy: true, degraded: false, pending_bytes: 0`.
- Confirmed the flush actually landed the data, not just cleared the spool
  file: `SELECT count() FROM piper_e2e.piper_run_metrics WHERE run_id =
  '...'` against the real ClickHouse container returned `3`.

The claimed durability property — writes survive a backend outage and
flush once it recovers, transparently to readers — is real, not
aspirational.

### 8.6 What remains unverified / aspirational

- **InfluxDB was not exercised through the full running-server end-to-end
  pass** (§8.3) — only at the package level against a live container
  (§8.1). The logs/metrics adapters used for the server pass were
  Elasticsearch and ClickHouse. InfluxDB's `Open()`-time bucket-retention
  configuration (§7.3) and its spool/degrade/recover behavior specifically
  through the REST API were not independently confirmed this round, though
  they share the same `spooledBackend` wrapper already verified for
  Elasticsearch/ClickHouse.
- **Elasticsearch's rollover-based per-document retention** was not built
  or tested — see §7.1 and §10.
- **Multi-Member federation** (a Home routing `GET .../logs` to a remote
  Member whose own stats backend is external) was not part of this pass;
  only a single standalone installation was exercised.
- **Concurrent high-volume load** (many runs writing simultaneously,
  spool behavior under sustained backpressure rather than a single blip)
  was not load-tested.

## 9. Known gaps

- **No frontend configuration UI.** `stats.logs`/`stats.metrics` are
  `config.yaml`-only. `settings.go` (the persisted-settings API the
  frontend's Settings pages read/write) has no `Stats` handling at all, and
  the only frontend code that touches statistics
  (`frontend/src/features/runs/api.ts`'s `getStatsCapabilities`) only reads
  `GET /stats/capabilities` for the log/metric viewer's degraded-state
  banner — there is no page to choose a backend, set a URL, or manage
  `credential_ref` for logs/metrics the way there is for artifact storage.
  This was confirmed unchanged during this pass (out of scope to build —
  see AGENTS.md/task constraints) and remains config-file-only.
- **Elasticsearch retention is index-scoped, not row-scoped** (§7.1,
  confirmed live in §8.4) — worth calling out again here because it's easy
  to assume "retention: 24h" means per-row aging the way it does for
  ClickHouse, and it does not for Elasticsearch as currently implemented.
- **An index/table created under the old (pre-fix) dynamic mapping is not
  retroactively repaired** by simply restarting with the fix in place — see
  the note at the end of §8.2.

## 10. Next steps

For whoever picks this up next:

1. **Frontend configuration page.** A Settings page (or extension of the
   existing Storage settings page's pattern) to view/set
   `stats.logs.url`/`stats.metrics.url`/`credential_ref`/`retention`/
   `manage_retention`, and to surface `GET .../stats/capabilities` more
   prominently (it already exists and is read by the log/metric viewer, but
   there's no dedicated "statistics backend health" view). `settings.go`
   would need a `Stats` section added to the persisted-settings model,
   following the same pattern `Storage` already uses there.
2. **Elasticsearch rollover for real per-document retention.** Move from a
   single fixed index to a rollover alias
   (`<base>-logs` → `<base>-logs-000001`, `-000002`, ...) with an ILM policy
   that has a `rollover` phase (by size or age) ahead of the `delete`
   phase. This is what makes ILM retention behave like "delete documents
   older than N" instead of "delete the whole index once it's N old" — a
   meaningful behavior change, not a bug fix, and should be scoped and
   reviewed as its own piece of work given the index-naming and query-path
   changes it implies (`elasticQuery`'s hardcoded `b.logsIndex`/
   `b.metricsIndex` would need to become an alias, not a concrete index
   name).
3. **Loki**, explicitly named as unsupported by `ValidateBackendURL`
   (`TestBackendURLValidationRejectsLokiAndSecrets`) — if there's demand for
   a log-only backend distinct from a metric store, Loki is the obvious
   next adapter, following the same `httpBackend` pattern as the three
   existing ones.
4. **InfluxDB end-to-end pass** through a running server (§8.6) — the
   package-level integration test already exists and passes against a live
   container; what's missing is exercising it the way §8.3–8.5 did for
   Elasticsearch/ClickHouse (a real pipeline run, spool degrade/recover
   against a live InfluxDB outage, retention against a live bucket).
5. **Federation-aware live QA** — a Home routing to a Member whose stats
   backend is external, per `docs/qa/adversarial-qa-playbook.md`'s standard
   of exercising each real deployment shape rather than a single
   standalone install.
