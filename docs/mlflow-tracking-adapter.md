# MLflow Tracking Adapter Design

> 상태: 제안(Proposed) — 구현 전 리뷰 필요
>
> 작성일: 2026-08-28
>
> 대상: Piper의 Pipeline run과 Piper가 관리하는 Notebook execution을 외부
> MLflow Tracking Server에 안전하게 내보내고, 향후 Model Registry 연동으로
> 확장할 수 있는 선택형 어댑터
>
> 관련 문서: [Jupyter Execution + MCP](jupyter-mcp-execution.md),
> [통계 저장소](stats-history-store.md),
> [Federated Piper](federated-piper.md),
> [백엔드 개발 가이드](backend/develop.md),
> [프런트엔드 개발 가이드](frontend/develop.md),
> [API 규약](backend/api-conventions.md),
> [Adversarial QA](qa/adversarial-qa-playbook.md)

## 1. 요약

Piper는 이미 다음 데이터를 소유한다.

- Pipeline run의 프로젝트, pipeline, experiment, parameter, 상태와 시간
- step별 numeric metric과 로그
- `{runID}/{step}/{artifact}/...` 구조의 durable artifact
- artifact를 소비하는 ModelService와 배포 이력
- 향후 `NotebookExecution`의 Notebook path, 실행 상태, 결과와 요청자

따라서 MLflow를 Piper의 새로운 실행 엔진이나 원본 데이터베이스로 넣을 필요가
없다. 이 설계는 **Piper가 원본이고 MLflow는 선택형 projection**이라는 방향을
채택한다.

```text
Pipeline / Notebook execution
              │
              ▼
      Piper authoritative state
      ├─ run / step repositories
      ├─ statsstore metrics
      ├─ artifact store
      └─ serving / notebook execution history
              │
              ▼
      Durable Integration Outbox
              │
              ▼
        MLflow Adapter Worker
              │
              ▼
      MLflow Tracking Server
      ├─ experiments
      ├─ runs
      ├─ params / metrics / tags
      └─ small Piper artifact manifest
```

핵심 결정은 다음과 같다.

1. Piper run 상태가 권위 있는 원본이다. MLflow 장애나 불일치가 Piper run의 성공,
   실패, 취소 상태를 바꾸지 않는다.
2. 연동은 서버 측 비동기 export다. v1에서 workload에 MLflow credential을 자동
   주입하지 않는다.
3. `internal/event.Bus`는 느린 subscriber에게 보낼 event를 drop하는 in-memory 알림이므로
   동기화의 신뢰 경계로 사용하지 않는다. durable outbox와 reconciliation을 둔다.
4. Piper ID와 MLflow ID는 다르므로 명시적인 mapping table을 둔다. MLflow ID를
   Piper run ID로 가장하거나 문자열 규칙으로 역산하지 않는다.
5. v1 artifact 모드는 `reference`다. Piper artifact를 MLflow에 전부 복제하지 않고,
   MLflow run에 Piper artifact manifest와 링크를 남긴다.
6. Model Registry는 v1 자동 동기화 범위에서 제외한다. 명시적인 모델 등록과
   선택적 artifact mirror를 후속 단계로 설계한다.
7. 사람이 JupyterLab에서 임의로 실행한 cell은 Piper가 관찰하지 못하므로 자동
   tracking하지 않는다. Piper REST/MCP를 통해 생성된 `NotebookExecution`만
   자동 export할 수 있다.

## 2. 목표와 비목표

### 2.1 목표

- 프로젝트별로 하나 이상의 MLflow Tracking Server 연결을 설정할 수 있게 한다.
- Piper Pipeline run을 MLflow experiment/run으로 일관되게 매핑한다.
- parameter, metric history, 상태, pipeline/step metadata를 비동기로 내보낸다.
- Piper가 관리하는 Notebook execution도 선택적으로 MLflow run으로 내보낸다.
- Piper UI에서 MLflow run으로 이동할 수 있는 외부 링크와 sync 상태를 제공한다.
- 일시적인 MLflow 장애, rate limit, timeout 뒤에도 재시도하고 재조정할 수 있게 한다.
- 중복 전송, 순서 역전, Piper 재시작에 안전한 at-least-once export를 제공한다.
- SQLite/Postgres, 세 runtime type 및 federated Home/Member 소유권 모델을 유지한다.
- credential, endpoint 및 원격 error body가 로그/API에 노출되지 않게 한다.

### 2.2 비목표

- MLflow를 Piper의 scheduler, queue, runtime 또는 status source로 사용하지 않는다.
- Piper의 `statsstore`를 MLflow로 교체하지 않는다. MLflow는 metric query backend가
  아니라 외부 tracking projection이다.
- Piper artifact retention을 MLflow retention에 위임하지 않는다.
- v1에서 모든 artifact를 MLflow artifact store로 복사하지 않는다.
- v1에서 MLflow Model Registry와 Piper ModelService를 양방향 동기화하지 않는다.
- v1에서 MLflow Projects, MLflow Serving 또는 MLflow Pipelines 호환 계층을 만들지
  않는다.
- MLflow UI를 Piper 안에 iframe으로 삽입하지 않는다.
- MLflow에서 수정한 run status, param 또는 tag를 Piper로 역동기화하지 않는다.
- 외부 MLflow run을 Piper run으로 자동 import하지 않는다.

## 3. 현재 Piper 모델과 MLflow 모델의 대응

### 3.1 현재 Piper 데이터

| Piper | 현재 위치 | 특성 |
|---|---|---|
| Project | `pkg/project` | 권한과 federation routing 경계 |
| Experiment | `run.Run.Experiment` | sweep/group 이름; 빈 값 가능 |
| Pipeline run | `pkg/pipeline/run.Run` | Piper가 소유하는 상태 원본 |
| Parameters | `Run.ParamsJSON` | JSON object; 값 타입이 string에 한정되지 않음 |
| Step | `run.Step` | 문자열 step 이름, attempts와 상태 |
| Metrics | `statsstore.MetricPoint` | project/run/step/key/value/timestamp |
| Artifacts | `storage.Store` | durable key `{run}/{step}/{artifact}/...` |
| Model deployment | `serving.Service` | Piper run/artifact와 연결 가능 |
| Notebook execution | 제안된 `NotebookExecution` | Piper REST/MCP 실행만 관찰 가능 |

### 3.2 MLflow 대응

| Piper | MLflow | 비고 |
|---|---|---|
| Project + Piper experiment | Experiment | 이름 template과 mapping table 사용 |
| Pipeline run | Run | 1:1 mapping |
| Notebook execution | Run | 별도 source tag 사용; 선택 가능 |
| ParamsJSON | Params | deterministic flatten/string encoding |
| MetricPoint | Metric | step name을 key namespace에 포함 |
| Step 상태/attempt | Tags | v1에서 nested MLflow run을 만들지 않음 |
| Piper artifacts | Tags + manifest artifact | v1 reference mode |
| ModelService deploy | Run tags/link | Registry 등록과 별개 |

Pipeline step마다 nested MLflow run을 만들지 않는다. Piper step은 scheduler 상태와
retry attempt를 가진 실행 단위지만, MLflow child run을 자동 생성하면 step 수만큼
run이 폭증하고 retry와 parent lifecycle의 의미가 복잡해진다. v1은 Pipeline run
하나를 MLflow run 하나로 만들고 step 이름을 metric key/tag namespace에 포함한다.

### 3.3 `statsstore.MetricBackend`로 구현하지 않는 이유

MLflow 연동은 `pkg/statsstore.MetricBackend` 구현이 아니다.

- `MetricBackend`는 Piper가 metric을 append하고 다시 query하는 원본 저장 경계다.
- MLflow adapter는 experiment/run 생성, parameter/tag, status, artifact manifest까지
  함께 다뤄야 하므로 metric 저장소 인터페이스보다 범위가 넓다.
- MLflow retention이나 수동 삭제가 Piper Experiments/Alerting query를 깨뜨리면
  안 된다.
- adapter가 느리거나 실패해도 Piper metric append는 성공해야 한다.

따라서 기존 stats backend에 정상 저장된 metric snapshot을 durable outbox가
MLflow로 내보내는 단방향 구조를 사용한다.

## 4. 권위와 일관성 규칙

### 4.1 Piper가 원본인 항목

- run/step의 상태와 시작/종료 시간
- run parameter의 원본 JSON
- metric의 원본과 retention
- artifact key, storage backend 및 retention
- ModelService가 실제로 배포한 artifact
- Notebook execution의 승인, 상태와 결과

MLflow 데이터가 삭제되거나 수동 수정되어도 Piper 원본은 변경하지 않는다.

### 4.2 MLflow에만 있는 항목

- MLflow experiment ID와 run ID
- MLflow UI link
- MLflow 측 user-created tag
- 향후 MLflow model name/version/alias

Piper가 소유하는 `piper.*` tag만 adapter가 reconcile한다. 사용자가 MLflow UI에서
만든 다른 tag는 삭제하거나 덮어쓰지 않는다.

### 4.3 장애 격리

- MLflow API 호출은 Pipeline/Notebook의 synchronous lifecycle path에 두지 않는다.
- MLflow create/log/update 실패는 Piper run을 실패시키지 않는다.
- endpoint 장애는 outbox backlog와 integration health에 나타내고 재시도한다.
- backlog limit을 넘으면 새 Piper 실행을 거부하지 않고 adapter를 degraded로 만든다.
  단, disk/DB 전체를 채우지 않도록 admission limit과 운영 경고를 둔다.

## 5. 연결 리소스와 credential

### 5.1 MLflowIntegration

연결은 프로젝트 범위 DB 리소스다. 서버 YAML을 수정하고 재시작해야만 연결을
추가하는 방식은 사용하지 않는다.

```go
type MLflowIntegration struct {
    ID                     string
    ProjectID              string
    Name                   string
    TrackingURI            string
    CredentialRef          string
    Enabled                bool
    Default                bool
    ExportPipelines        bool
    ExportNotebookExecutions bool
    ExperimentTemplate     string
    ArtifactMode           string // reference; future: mirror_selected, mirror_all
    CreatedBy              string
    CreatedAt              time.Time
    UpdatedAt              time.Time
}
```

v1은 프로젝트당 `Default=true` 연결을 최대 하나 허용한다. 여러 연결로 동시에
fan-out하는 것은 outbox cardinality와 운영 복잡도를 크게 늘리므로 후속 범위다.

기본 experiment template:

```text
piper/{project_id}/{experiment_or_pipeline}
```

- `Run.Experiment`가 있으면 이를 마지막 segment로 사용한다.
- 없으면 pipeline name을 사용한다.
- 외부 이름이 바뀌어도 기존 run mapping은 변경하지 않는다.
- template 결과는 MLflow 허용 길이와 문자를 검증한다.
- 충돌 방지를 위해 project display name이 아니라 안정적인 project ID를 기본으로
  포함한다.

### 5.2 Credential

`Credential.Kind`에 `mlflow`를 추가하거나 generic credential의 명확한 schema를
사용한다. UI와 연결 테스트를 고려하면 전용 kind를 권장한다.

지원 필드:

| 필드 | 설명 |
|---|---|
| `token` | Bearer token; username/password와 상호 배타적 |
| `username` | HTTP Basic username |
| `password` | HTTP Basic password |
| `ca_cert` | 선택적 custom CA; 저장/표시 정책 별도 검증 |

임의 header map은 v1에서 지원하지 않는다. Host, Content-Length, forwarding header
등을 주입하게 두면 SSRF와 credential confusion 위험이 커진다.

credential은 Piper adapter worker만 읽는다. Pipeline container, Notebook pod,
MCP client 또는 브라우저에 전달하지 않는다.

### 5.3 Endpoint와 SSRF 방어

Project admin이 설정한 `TrackingURI`는 Piper 서버가 요청하는 대상이므로 SSRF
경계다.

- scheme은 production에서 `https`만 허용하고 development에서만 명시적으로
  `http`를 허용한다.
- URL userinfo와 query에 credential을 넣지 못하게 한다.
- system config의 host/CIDR allowlist를 통과해야 한다.
- redirect는 기본 거부하거나 같은 origin의 제한된 redirect만 허용한다.
- DNS resolve 후 private/link-local/metadata address 정책을 적용하고, redirect마다
  다시 검사한다. **단, host가 `allowed_hosts`에 정확히 있거나 resolve된 주소가
  `allowed_cidrs`의 범위 안에 있으면 이 private/link-local 거부를 건너뛴다** —
  self-hosted MLflow(공식 문서가 권장하는 배포 형태)는 대부분 사내망/VPC/
  Kubernetes 클러스터 내부 주소로 뜨므로, allowlist는 "이미 public인 주소를 그중
  일부로 더 좁히는" 용도가 아니라 "관리자가 명시적으로 신뢰하는 사설 엔드포인트를
  예외적으로 허용하는" 용도다. allowlist가 비어 있으면 private/loopback/
  link-local 거부는 무조건 적용된다.
- response body와 duration에 제한을 둔다.
- log에는 canonical host만 남기고 전체 credential-bearing URL을 남기지 않는다.

연결 테스트는 experiment search를 1건 제한으로 호출하고, 성공 여부와 정제된
message만 저장한다. 테스트를 위해 experiment나 run을 생성하지 않는다.

## 6. ID 매핑과 저장 모델

### 6.1 Experiment mapping

```go
type MLflowExperimentLink struct {
    IntegrationID     string
    ProjectID         string
    PiperGroupKey     string // experiment:<name> 또는 pipeline:<name>
    MLflowExperimentID string
    MLflowName        string
    CreatedAt         time.Time
    UpdatedAt         time.Time
}
```

unique key:

```text
(integration_id, project_id, piper_group_key)
```

adapter는 먼저 mapping을 조회한다. 없으면 MLflow의 get-by-name을 호출하고, 없을
때만 create한다. 동시에 두 worker가 create하더라도 name conflict 응답 뒤
get-by-name으로 수렴해야 한다.

### 6.2 Run mapping

```go
type MLflowRunLink struct {
    IntegrationID     string
    ProjectID         string
    SourceType        string // pipeline | notebook_execution
    SourceID          string // Piper run ID 또는 NotebookExecution ID
    MLflowExperimentID string
    MLflowRunID       string
    MLflowRunURL      string
    SyncStatus        string // pending | syncing | synced | degraded | disabled
    LastSequence      int64
    LastErrorCode     string
    LastErrorMessage  string
    LastSyncedAt      *time.Time
    CreatedAt         time.Time
    UpdatedAt         time.Time
}
```

unique key:

```text
(integration_id, project_id, source_type, source_id)
```

MLflow run name은 사람이 식별하기 쉽게 만들되 identity로 사용하지 않는다.

```text
{pipeline_name}-{short_piper_run_id}
{notebook_name}-{short_execution_id}
```

### 6.3 Durable outbox

```go
type IntegrationOutboxEvent struct {
    ID             string
    IntegrationID  string
    ProjectID      string
    AggregateType  string // pipeline_run | notebook_execution
    AggregateID    string
    Sequence       int64
    EventType      string
    PayloadJSON    []byte
    Status         string // pending | delivering | delivered | dead
    Attempts       int
    NextAttemptAt  time.Time
    LeaseOwner     string
    LeaseExpiresAt *time.Time
    LastErrorCode  string
    LastError      string
    CreatedAt      time.Time
    DeliveredAt    *time.Time
}
```

unique key:

```text
(integration_id, aggregate_type, aggregate_id, sequence, event_type)
```

outbox payload에는 export에 필요한 bounded snapshot을 넣는다. metric은 원본
backend retention 뒤에도 재시도할 수 있도록 key/value/timestamp/step name을
payload에 포함한다. artifact binary나 code 원문은 넣지 않는다.

SQLite와 Postgres 모두 다음을 지원해야 한다.

- pending event의 ordered claim
- lease 만료 후 재claim
- delivered/dead 처리
- aggregate별 sequence ordering
- backlog count와 oldest age
- project/integration purge

SQLite에서는 `SKIP LOCKED`를 흉내 내려고 복잡한 동시 worker를 만들지 않고 기본
dispatcher concurrency를 1로 둔다. Postgres는 bounded concurrency로 늘릴 수 있다.

## 7. Export lifecycle

### 7.1 Pipeline run 시작

Piper `StartRun`이 run row를 만든 뒤 다음 snapshot을 outbox에 enqueue한다.

```text
pipeline_run.created
```

payload:

- Piper project/run ID
- pipeline name/version
- Piper experiment
- params snapshot
- creator ID
- runtime type
- start/scheduled time
- Piper run URL을 만들 수 있는 public base URL

adapter worker는 다음 순서로 처리한다.

1. integration이 아직 enabled인지 확인
2. experiment mapping resolve/create
3. 기존 run link 조회
4. 없으면 MLflow run create
5. params와 `piper.*` tags log
6. link를 `synced`로 저장

MLflow run create 응답이 timeout으로 불명확하면 바로 다시 create하지 않는다.
먼저 MLflow experiment에서 `tags.piper.run_id`와
`tags.piper.integration_id`로 search하여 이미 생성된 run을 찾아 mapping한다.

### 7.2 Parameters

MLflow parameter는 한 run에서 같은 key를 다른 값으로 다시 기록할 수 없으므로,
Piper run 생성 시 snapshot 한 번만 export한다.

encoding 규칙:

- string/number/bool/null: JSON scalar의 안정적인 string 표현
- object/array: canonical JSON string
- nested flatten은 기본으로 하지 않는다. 사용자 key와 collision할 수 있기 때문이다.
- 길이 제한을 넘는 값은 조용히 자르지 않고 hash와 정제된 preview를 tag로 남기며
  integration warning을 기록한다.
- secret redaction은 `Run.Redact()`와 동일하거나 더 강한 정책을 적용한다.

권장 tag:

```text
piper.project_id
piper.run_id
piper.pipeline.name
piper.pipeline.version
piper.experiment
piper.runtime
piper.created_by
piper.source = pipeline
piper.url
piper.integration_id
```

### 7.3 Metrics

Piper metric은 step name을 별도 필드로 갖지만 MLflow metric은 run 안의 key로
구분된다. 기본 key mapping은 다음과 같다.

```text
{escaped_step_name}/{escaped_metric_key}
```

- `/`, `%`, 제어 문자는 percent-encoding하여 역변환 가능하게 한다.
- 최종 key가 MLflow 제한을 넘으면 readable prefix + SHA-256 suffix를 사용하고
  mapping tag/manifest를 남긴다.
- value와 millisecond timestamp를 보존한다.
- `(run, step, key)`별 증가 sequence를 MLflow metric `step`으로 사용한다.
- `.metrics.json`의 final metric도 같은 stream의 다음 sequence로 export한다.
- NaN/Inf 정책은 Piper 수집 단계와 MLflow 양쪽 제약을 확인하고 명시적으로
  reject 또는 encode한다. 조용한 변환은 하지 않는다.

near-real-time 경로:

```text
stats backend AppendMetrics 성공
        │
        ├─ metric.recorded in-memory event (UI/alerting용)
        └─ durable MLflow outbox enqueue (integration용)
```

stats backend와 SQL outbox가 다른 저장소일 수 있어 완전한 atomic transaction은
불가능하다. 따라서 run terminal reconciliation이 누락을 보완한다.

### 7.4 Run 종료

Queue의 `finalizeRunLocked`가 DB의 terminal status CAS에 성공한 경우에만
`pipeline_run.finished`를 enqueue한다.

상태 매핑:

| Piper | MLflow |
|---|---|
| `running` | `RUNNING` |
| `success` | `FINISHED` |
| `failed` | `FAILED` |
| `canceled` | `KILLED` |

종료 event 처리 전에 adapter는 metric reconciliation과 artifact manifest 생성을
수행한다. transient 오류가 있으면 MLflow run을 먼저 terminal로 닫지 않고 event를
재시도한다. 최대 지연을 넘기면 available data까지 export하고 run을 terminal로
닫은 뒤 link를 `degraded`로 표시할 수 있다.

### 7.5 Notebook execution

[Jupyter Execution + MCP 설계](jupyter-mcp-execution.md)의 실행 리소스가 구현된
뒤 다음 조건에서 export한다.

- integration의 `ExportNotebookExecutions=true`
- execution이 승인되어 `queued`에 진입
- 대상 Notebook server와 project가 integration 범위에 있음

Notebook execution 하나를 MLflow run 하나로 매핑한다.

tags:

```text
piper.source = notebook_execution
piper.notebook.server
piper.notebook.path
piper.notebook.execution_id
piper.notebook.source_sha256
piper.created_by
piper.mcp.client_id      # MCP 요청일 때만, secret 아님
piper.url
```

Notebook code 원문과 cell output 전체를 MLflow param/tag에 넣지 않는다. 결과
Notebook과 생성 파일은 v1 artifact reference manifest에만 표시한다.

사람이 JupyterLab UI에서 직접 실행한 cell은 Piper 실행 리소스가 아니므로 자동
export하지 않는다. 이를 추적하려면 사용자가 Notebook에서 MLflow SDK를 직접
사용하는 후속 “native workload access” 기능이 필요하다.

## 8. Artifact 정책

### 8.1 v1: reference mode

Piper artifact store가 원본이다. MLflow에는 작은 JSON manifest만 업로드한다.

```json
{
  "schema_version": 1,
  "piper_project_id": "project-id",
  "piper_run_id": "run-id",
  "artifacts": [
    {
      "step": "train",
      "name": "model",
      "piper_key": "run-id/train/model",
      "piper_url": "https://piper.example/api/projects/.../artifacts/...",
      "storage_uri": "s3://redacted-or-policy-allowed-uri",
      "size": 12345
    }
  ]
}
```

manifest path:

```text
piper/artifacts.json
```

규칙:

- presigned URL, storage token 및 workload token은 manifest에 넣지 않는다.
- storage URI 노출은 프로젝트 정책으로 허용될 때만 포함한다.
- 기본은 인증된 Piper artifact URL이다.
- artifact binary는 MLflow로 복제하지 않는다.
- Piper artifact TTL이 지나면 MLflow manifest 링크가 404가 될 수 있음을 UI에
  명시한다.
- MLflow artifact retention이 Piper retention을 연장하지 않는다.

### 8.2 선택 이유

`mirror_all`을 기본으로 하면 다음 문제가 생긴다.

- 대용량 모델/데이터가 두 저장소에 중복됨
- Piper와 MLflow retention이 달라 어느 쪽이 원본인지 불명확해짐
- remote artifact를 Piper host로 download한 뒤 다시 upload하는 비용 발생
- K8s/Docker workload가 가진 storage credential을 또 배포해야 함
- 부분 실패와 checksum/retry 상태가 새로운 데이터 파이프라인이 됨

따라서 v1은 tracking metadata 연동에 집중한다.

### 8.3 후속: selected model mirror

Model Registry 등록을 위해 특정 artifact만 명시적으로 MLflow artifact store에
복사하는 `mirror_selected`를 후속으로 제공할 수 있다.

필수 조건:

- 사용자가 선택한 artifact만 대상
- source checksum, size, destination URI를 mapping table에 저장
- multipart/streaming copy와 bounded local staging
- 재시도 시 checksum 기반 idempotency
- 성공 전에는 Model Version을 만들지 않음
- Piper 원본 artifact가 삭제돼도 mirror 수명은 MLflow 정책을 따름을 명시

## 9. Model Registry와 Serving

### 9.1 v1 범위

- Pipeline/Notebook tracking만 자동 export
- ModelService 배포 시 MLflow run tag에 Piper service name/status/link를 추가할 수
  있음
- Registered Model/Model Version은 자동 생성하지 않음
- MLflow model alias/stage 변경이 Piper 배포를 자동 유발하지 않음

모든 `outputs:` artifact가 MLflow Model 형식인 것은 아니며, Piper ModelService는
명시적인 run command와 port를 필요로 한다. artifact 이름만 보고 자동 등록/배포할
수 없다.

### 9.2 후속 명시적 등록

후속 API 예시:

```text
POST /api/projects/{project_id}/runs/{run_id}/artifacts/{step}/{artifact}/mlflow-registrations
```

요청:

```json
{
  "integration_id": "...",
  "registered_model_name": "fraud-detector",
  "description": "..."
}
```

이 작업은 다음을 확인한다.

1. artifact가 MLflow Model layout 또는 명시된 supported packaging인지
2. MLflow에서 접근 가능한 source URI가 있는지
3. 필요하면 selected mirror가 성공했는지
4. 연결된 MLflow run ID가 있는지
5. 동일 source checksum의 기존 version이 있는지

Piper에는 별도 `MLflowModelVersionLink`를 저장한다. Registry version을 문자열
규칙으로 추론하지 않는다.

### 9.3 MLflow → Piper serving

향후 `ModelService.spec.model`에 다음과 같은 새 variant를 검토할 수 있다.

```yaml
model:
  from_mlflow:
    integration: production-mlflow
    name: fraud-detector
    alias: champion
```

하지만 alias가 가리키는 version이 바뀌었다고 자동 redeploy하지 않는다. 실제
배포는 명시적인 Piper action이며 resolved version, source URI, checksum을 service
history에 고정해야 한다.

## 10. Delivery, retry와 reconciliation

### 10.1 Delivery semantics

MLflow REST에는 Piper outbox event ID를 받는 범용 idempotency key가 없으므로
adapter는 at-least-once delivery다.

- experiment/run create: `piper.*` tag search로 중복 수렴
- tag update: 동일 key/value set은 idempotent하게 취급
- run status update: 현재 remote state 확인 후 적용
- parameter: ambiguous failure 시 remote run을 GET하여 동일 값 존재 여부 확인
- metric: 안정적인 key/timestamp/step/value로 재전송; 동일 point 중복은 의미상
  같은 관측값으로 취급
- artifact manifest: 고정 path에 같은 content hash로 overwrite

### 10.2 Retry

재시도 대상:

- network timeout/reset
- HTTP 408/425/429
- HTTP 5xx
- 일시적 MLflow resource pending 상태

재시도하지 않는 오류:

- 인증/권한 401/403
- 잘못된 endpoint/API schema
- credential 누락
- validation/length 오류

재시도는 exponential backoff + jitter를 사용하고 `Retry-After`가 있으면 존중한다.
401/403은 integration을 degraded로 만들고 credential이 갱신될 때까지 낮은 빈도로
probe한다.

### 10.3 Ordering

aggregate별 순서를 보장한다.

```text
created -> metrics/artifact snapshot -> finished
```

다른 Piper run끼리는 병렬 처리할 수 있다. 한 run의 `finished`가 앞선 metric보다
먼저 처리되지 않도록 `(aggregate_id, sequence)` gate를 둔다.

### 10.4 Reconciliation

reconciler는 주기적으로 다음을 비교한다.

- enabled integration의 Piper terminal run 중 link가 없는 run
- `LastSequence`보다 새로운 metric
- Piper terminal인데 MLflow가 RUNNING인 run
- MLflow run ID가 없어 create가 불명확한 link
- 오래된 pending/delivering lease
- artifact manifest hash 불일치

metric store가 외부 backend일 수 있으므로 cursor pagination으로 조회하고 bounded
batch로 outbox를 보충한다. 한 번에 프로젝트 전체 metric을 메모리에 올리지 않는다.

reconciliation은 remote MLflow 변경을 Piper로 가져오지 않는다. 오직 Piper가
소유하는 projection을 다시 맞춘다.

### 10.5 Dead letter와 수동 재동기화

validation처럼 자동 회복되지 않는 event는 `dead`로 옮긴다. 원문 credential이나
remote body 전체를 저장하지 않고 정제된 code/message만 보존한다.

Project admin은 run 단위 또는 integration 단위 sync job을 만들 수 있다.

```text
POST /api/projects/{project_id}/mlflow-sync-jobs
```

이 API는 비동기 `MLflowSyncJob` 리소스를 `201`로 반환한다. HTTP handler 안에서
전체 재동기화를 수행하지 않는다.

## 11. REST API

### 11.1 Integrations

| Method | Path | 최소 역할 | 설명 |
|---|---|---:|---|
| `GET` | `/mlflow-integrations` | viewer | credential을 제외한 연결 목록 |
| `POST` | `/mlflow-integrations` | admin | 연결 생성, `201` |
| `GET` | `/mlflow-integrations/{id}` | viewer | 연결/health/backlog |
| `PUT` | `/mlflow-integrations/{id}` | admin | 연결 설정 교체 |
| `DELETE` | `/mlflow-integrations/{id}` | admin | 연결 비활성/삭제, `204` |
| `POST` | `/mlflow-integrations/{id}/test` | admin | 정제된 connection test 결과 |

삭제 기본 동작은 다음과 같다.

- dispatcher 중지
- pending outbox를 `disabled` 상태로 보존
- Piper→MLflow mapping 보존
- MLflow experiment/run 삭제 안 함
- credential 자체는 별도 리소스이므로 삭제 안 함

완전한 purge는 별도 admin 작업으로 두고 명시적인 확인을 요구한다.

### 11.2 Run links

| Method | Path | 최소 역할 | 설명 |
|---|---|---:|---|
| `GET` | `/runs/{id}/mlflow-links` | viewer | sync 상태와 안전한 MLflow UI URL |
| `GET` | `/notebooks/{name}/executions/{id}/mlflow-links` | viewer | Notebook execution link |

MLflow API credential이나 raw artifact URI는 반환하지 않는다.

### 11.3 Sync jobs

| Method | Path | 최소 역할 | 설명 |
|---|---|---:|---|
| `POST` | `/mlflow-sync-jobs` | admin | integration/run 범위 재조정 job 생성 |
| `GET` | `/mlflow-sync-jobs?limit=&offset=` | admin | `X-Total-Count` 포함 이력 |
| `GET` | `/mlflow-sync-jobs/{id}` | admin | 진행률과 정제된 오류 |
| `POST` | `/mlflow-sync-jobs/{id}/cancel` | admin | `204` |

새 endpoint는 `docs/openapi.yaml`에 같은 변경으로 추가한다. JSON field와 query는
`snake_case`, 오류는 API 규약에 맞춘다.

## 12. Backend package 경계

제안 구조:

```text
pkg/integration/
  outbox/
    model.go
    repository.go
    dispatcher.go
  mlflow/
    model.go
    repository.go
    service.go
    client.go
    exporter.go
    reconcile.go
    handler.go
    public.go

internal/store/sqlite/
  mlflow.go
  integration_outbox.go

internal/store/postgres/
  mlflow.go
  integration_outbox.go
```

의존 방향:

```text
run / metric / notebook lifecycle
              │ snapshot enqueue
              ▼
      integration/outbox
              │
              ▼
      mlflow.Exporter
       ├─ MLflowClient
       ├─ run/metric/artifact readers
       └─ mapping repositories
```

`MLflowClient`는 공식 Tracking REST API의 필요한 최소 subset만 감싼다.

```go
type Client interface {
    GetExperimentByName(context.Context, string) (*Experiment, error)
    CreateExperiment(context.Context, CreateExperimentRequest) (*Experiment, error)
    CreateRun(context.Context, CreateRunRequest) (*Run, error)
    GetRun(context.Context, string) (*Run, error)
    SearchRuns(context.Context, SearchRunsRequest) (RunPage, error)
    LogBatch(context.Context, LogBatchRequest) error
    UpdateRun(context.Context, UpdateRunRequest) error
    UploadArtifact(context.Context, string, string, io.Reader, int64) error
}
```

MLflow SDK를 subprocess로 호출하거나 Python sidecar를 추가하지 않는다. Piper가
이미 Go 단일 binary인 구조를 유지하도록 HTTP client를 Go로 구현한다.

공식 API 기준:
[MLflow REST API](https://mlflow.org/docs/latest/api_reference/rest-api.html),
[Tracking Server](https://mlflow.org/docs/latest/self-hosting/architecture/tracking-server/),
[Artifact Stores](https://mlflow.org/docs/latest/self-hosting/architecture/artifact-store/),
[Model Registry](https://mlflow.org/docs/latest/ml/model-registry/workflow/).

## 13. Configuration

프로젝트별 연결 값은 DB 리소스에 저장한다. 서버 config는 dispatcher의 운영 한도와
SSRF 정책만 가진다.

```yaml
integrations:
  mlflow:
    enabled: true
    dispatcher_concurrency: 2
    batch_size: 100
    request_timeout: 10s
    max_attempts_before_dead: 20
    max_backlog_events: 100000
    reconcile_interval: 10m
    lease_duration: 30s
    allow_insecure_http: false
    allowed_hosts:
      - "mlflow.example.com"
    allowed_cidrs: []
```

- `enabled=false`면 project integration CRUD는 가능하되 dispatcher는 동작하지 않고
  UI에 system-disabled 상태를 표시한다.
- `request_timeout`은 Pipeline run timeout과 무관하다.
- `batch_size`는 MLflow Log Batch 제한보다 작거나 같아야 한다.
- config validation은 빈 allowlist의 public endpoint, 음수 duration, 과도한 batch,
  잘못된 CIDR을 거부한다.

## 14. Federation

MLflow adapter는 Project와 run을 실제로 소유한 Member에서 동작한다.

- Home은 MLflow에 직접 export하지 않는다.
- MLflowIntegration, credential, mapping, outbox는 Member 소유 project data다.
- Home UI/API는 일반 project routing으로 Member에 relay한다.
- Home이 Member event를 받아 다시 export하면 중복 run이 생기므로 금지한다.
- 각 Member는 서로 다른 MLflow endpoint를 사용할 수 있다.
- Member가 offline이어도 Home은 stale sync 상태를 표시할 뿐 대신 export하지 않는다.
- Project 이동/이관 시 MLflow run을 자동 이동하거나 복제하지 않는다. 기존 link와
  provenance를 보존하는 별도 migration이 필요하다.

Federated test는 remote project가 Home의 local mapping/outbox repository로
fall-through하지 않는지 반드시 확인한다.

## 15. 보안과 개인정보

### 15.1 Secret redaction

다음은 MLflow로 export하지 않는다.

- credential 값과 resolved environment
- Pipeline YAML 전체
- Notebook code 원문
- Jupyter/MCP/storage/workload token
- raw command line에 포함된 secret
- 전체 error stack 또는 remote response body

parameter가 secret인지 명시되지 않은 경우에도 Piper의 기존 redaction 규칙을 먼저
적용한다. redaction 이후에도 project admin이 integration을 켤 때 “run parameter와
metric이 외부 MLflow로 전송된다”는 데이터 경계를 명확히 보여준다.

### 15.2 Remote response

- 응답 body 최대 크기 제한
- JSON decode 오류 시 앞부분을 그대로 log하지 않음
- MLflow error code와 정제된 message만 보존
- HTML proxy/error page에 credential이 반사될 수 있으므로 저장하지 않음

### 15.3 TLS와 network

- production은 HTTPS 검증 필수
- `insecure_skip_verify` 옵션은 제공하지 않음
- custom CA는 credential/secret store에서 읽음
- tracking server 접근은 Piper server에서만 허용하도록 network policy 권장
- MLflow UI link는 browser용이고 API credential을 query에 붙이지 않음

## 16. UI

### 16.1 Integrations 목록

프로젝트에 Integrations route를 추가하고 `DataBodyTemplate.Resource` + DataGrid를
사용한다.

표시 항목:

- name
- tracking host
- enabled/default
- pipeline/notebook export 범위
- health (`healthy`, `degraded`, `disabled`)
- pending/dead event 수
- oldest pending age
- last successful sync

create는 전용 route, row click은 SidePanel, 삭제는 AlertDialog를 사용한다.

### 16.2 Create/edit form

`DataBodyTemplate.Group layout="stacked"`, React Hook Form, Zod를 사용한다.

- name
- tracking URI
- credential reference
- experiment template
- pipeline export toggle
- Notebook execution export toggle
- artifact mode (`reference`만 enabled; future 값은 disabled 설명)
- enabled/default
- connection test

credential 값은 form에 다시 채우지 않는다.

### 16.3 Run/Notebook detail

기존 detail panel에 작은 External Tracking section을 추가한다.

- sync status
- MLflow experiment/run ID의 축약 표시
- “Open in MLflow” 외부 링크
- last synced time
- degraded error code와 admin용 reconcile action

MLflow 링크가 없어도 Piper run 상세의 기존 metrics/artifacts UI는 그대로 동작한다.

### 16.4 Sync jobs

재동기화 이력은 server-side pagination과 detail SidePanel을 사용한다. 대규모
reconcile을 버튼 클릭 뒤 브라우저 request에 매달아 두지 않는다.

## 17. Observability

권장 metrics:

- `piper_mlflow_exports_total{event_type,status}`
- `piper_mlflow_export_duration_seconds{operation}`
- `piper_mlflow_outbox_pending`
- `piper_mlflow_outbox_oldest_seconds`
- `piper_mlflow_outbox_dead`
- `piper_mlflow_reconcile_total{status}`
- `piper_mlflow_remote_requests_total{operation,status_class}`

project ID, run ID, MLflow run ID는 Prometheus label로 넣지 않는다. cardinality가
높으므로 structured log field로만 사용한다.

구조화 log fields:

- `integration_id`
- `project_id`
- `piper_run_id` 또는 `notebook_execution_id`
- `mlflow_experiment_id`
- `mlflow_run_id`
- `outbox_event_id`
- `operation`
- `attempt`
- `error_code`

token, password, full TrackingURI, params payload, artifact manifest 전체는 log하지
않는다.

## 18. Retention과 삭제

- Piper run TTL이 지나도 MLflow run을 자동 삭제하지 않는다.
- Piper artifact TTL은 MLflow metadata 수명을 연장하지 않는다.
- integration 삭제는 remote MLflow 데이터를 삭제하지 않는다.
- Project purge는 local mapping/outbox를 삭제하기 전에 remote delete 여부를
  명시적으로 선택하게 한다.
- remote delete는 별도 destructive job이며 dry-run, 대상 count와 확인이 필요하다.
- MLflow에서 사용자가 run을 삭제하면 reconciler는 기본적으로 복원하지 않고
  link를 `remote_deleted`로 표시한다. admin이 명시적으로 recreate해야 한다.

이 규칙은 외부 시스템에서 사용자가 의도적으로 수행한 삭제를 adapter가 몰래
되돌리지 않게 한다.

## 19. 구현 단계

### Phase 0 — 계약과 선행 수정

- metric 수집 경로와 알려진 git-source `.metrics.json` QA finding 해결
- parameter redaction/export 정책 확정
- project admin의 external data export 권한 확인
- TrackingURI allowlist/SSRF 정책 결정
- MLflow 최소 지원 버전과 REST API compatibility test matrix 결정

### Phase 1 — 연결과 Pipeline tracking

- `mlflow` credential kind와 connection test
- MLflowIntegration CRUD/UI
- experiment/run link repositories와 SQLite/Postgres migration
- durable outbox와 dispatcher
- Pipeline create/params/final status export
- run detail의 MLflow link

### Phase 2 — Metric와 artifact reference

- metric outbox enqueue와 key/step mapping
- terminal reconciliation
- Piper artifact manifest 생성/업로드
- backlog/dead-letter UI와 sync jobs
- external stats backend 조합 검증

### Phase 3 — Notebook execution tracking

- Jupyter Execution 설계 구현 이후 lifecycle event 연결
- NotebookExecution→MLflow run mapping
- MCP client metadata의 안전한 tag
- result Notebook/artifact reference manifest

### Phase 4 — Registry와 native SDK access

- selected model mirror
- 명시적 Registered Model/Model Version 생성
- `from_mlflow` artifact resolver 검토
- workload/Notebook에 MLflow SDK access를 제공하는 별도 credential policy
- MLflow alias→Piper explicit deploy workflow

각 phase는 feature flag로 독립 배포하며, 이전 phase의 Piper 원본 동작을 MLflow
가용성과 연결하지 않는다.

## 20. 테스트와 adversarial QA

### 20.1 단위 테스트

- experiment template과 이름 escaping
- parameter canonical encoding/redaction/length
- metric key escaping, collision hash와 sequence
- Piper→MLflow status mapping
- run create ambiguous timeout 후 tag search 수렴
- param ambiguous retry remote verification
- outbox unique/claim/lease/order/backoff/dead-letter
- integration disabled/delete 중 pending event 처리
- SSRF: userinfo, redirect, DNS rebinding, private/link-local address
- error response redaction
- public DTO에 credential 부재

### 20.2 Repository conformance

SQLite와 Postgres에서 동일하게 검증한다.

- integration CRUD/default uniqueness
- experiment/run link uniqueness
- aggregate sequence ordering
- worker lease expiry와 reclaim
- pagination/count
- project purge
- concurrent claim

### 20.3 MLflow integration test

실제 database-backed MLflow Tracking Server를 띄워 검증한다.

- experiment get/create conflict
- run create/search/get/update
- log-batch limit과 validation error
- repeated metric point
- artifact manifest upload/read
- auth failure와 credential rotation
- rate limit/5xx/timeout proxy
- MLflow restart 중 backlog 후 recovery
- MLflow UI URL이 실제 run을 가리키는지 확인

Model Registry phase에서는 database-backed backend가 필수인 실제 server로 별도
검증한다.

### 20.4 Piper runtime QA

`docs/qa/adversarial-qa-playbook.md`에 따라 `baremetal`, `docker`, `k8s`를 각각
별도 Piper 인스턴스로 구성한다.

각 runtime에서 실제 UI form으로:

1. MLflow credential과 integration 생성/연결 테스트
2. Pipeline 제출
3. streaming metric과 `.metrics.json` final metric 생성
4. output artifact 생성
5. success/failure/cancel run 각각 수행
6. Piper UI, DB/outbox, Piper logs, MLflow UI/API를 교차 확인
7. MLflow를 중지한 상태에서 run 수행 후 재시작하여 backlog drain 확인
8. Piper 재시작 중 lease/reconciliation 확인
9. artifact TTL 후 MLflow manifest의 예상된 stale-link 상태 확인

Notebook phase에서는 Jupyter UI 직접 cell 실행은 자동 export되지 않고,
REST/MCP `NotebookExecution`만 export되는지도 확인한다.

### 20.5 Federation QA

- Home UI에서 Member 소유 project integration 생성
- run은 Member에서 단 한 번만 MLflow로 export
- Home/Member 재연결 뒤 mapping/outbox 중복 없음
- Member별 서로 다른 MLflow endpoint
- remote project가 Home local repositories로 fall-through하지 않음
- Home은 MLflow credential을 보거나 보관하지 않음

### 20.6 완료 기준

- MLflow가 완전히 중단돼도 Piper Pipeline/Notebook 상태가 정상 수렴한다.
- 같은 Piper run에 MLflow run이 하나만 생성된다.
- params, metric history, terminal status와 링크가 일치한다.
- artifact binary가 의도치 않게 복제되지 않는다.
- credential과 secret parameter가 MLflow/API/log에 노출되지 않는다.
- outbox 재전송과 reconciliation이 누락/중복을 안정적으로 수렴시킨다.
- SQLite/Postgres와 세 runtime, Home→Member 경로가 실제로 검증된다.

## 21. 결정이 필요한 항목

구현 전 다음을 확정해야 한다.

1. 기본 experiment mapping을 `project/experiment-or-pipeline`로 할지, 프로젝트당
   experiment 하나 + tags 방식으로 할지
2. Project admin이 외부 endpoint를 직접 등록할 수 있는지, system admin 승인이나
   host allowlist 선택만 허용할지
3. parameter export allowlist/denylist와 secret 판정 정책
4. MLflow 최소 지원 버전과 self-hosted/vendor compatibility 범위
5. remote-deleted MLflow run의 recreate 정책
6. Piper artifact URL을 MLflow 사용자도 인증해 열 수 있는 연결 방식
7. Notebook execution export의 기본값을 off로 둘지
8. Model Registry phase에서 `mirror_selected`를 필수로 할지, 외부 S3 URI 직접
   reference를 허용할지

기본 권고는 다음과 같다.

- experiment는 `piper/{project_id}/{experiment_or_pipeline}`
- endpoint는 system allowlist 안에서 project admin이 선택
- parameter는 기본 export하되 기존 redaction + project denylist 적용
- artifact는 reference only
- Notebook execution export 기본 off
- remote delete 자동 복원 안 함
- Model Registry는 selected mirror가 성공한 artifact만 등록
