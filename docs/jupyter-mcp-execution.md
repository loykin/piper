# Jupyter Execution + MCP Integration Design

> 상태: 제안(Proposed) — 구현 전 리뷰 필요
>
> 작성일: 2026-08-28
>
> 대상: Piper의 기존 Notebook 기능을 사람이 Jupyter UI로 사용하면서,
> 외부 AI 클라이언트도 MCP를 통해 같은 Notebook/Kernel을 안전하게 사용할 수
> 있도록 확장
>
> 관련 문서: [백엔드 개발 가이드](backend/develop.md),
> [프런트엔드 개발 가이드](frontend/develop.md),
> [API 규약](backend/api-conventions.md),
> [Federated Piper](federated-piper.md),
> [Adversarial QA](qa/adversarial-qa-playbook.md)

## 1. 요약

Piper는 이미 `baremetal`, `docker`, `k8s`에서 Jupyter 서버의 생성, 시작,
중지, 영구 볼륨 및 브라우저 프록시를 직접 관리한다. 현재 빠진 것은
**Jupyter 내부의 Kernel/Cell/Notebook 실행을 Piper가 관리하는 제어면**과
그 제어면을 외부 AI에 제공하는 MCP 인터페이스다.

이 설계는 다음 구조를 채택한다.

```text
사람 ── JupyterLab UI ── 기존 reverse proxy ─┐
                                             │
Piper UI / REST API ─────────────────────────┼─ NotebookExecutionService
                                             │          │
외부 AI ── MCP Streamable HTTP ──────────────┘          │
                                                        ▼
                                               NotebookGateway
                                                        │
                                         Jupyter REST + Kernel WebSocket
                                                        │
                              baremetal | docker | k8s의 기존 Jupyter 서버
```

핵심 결정은 다음과 같다.

1. Jupyter UI와 MCP는 대체 관계가 아니다. 사용자는 기존 JupyterLab을 그대로
   쓰고, AI는 동일한 서버와 볼륨을 Piper를 통해 사용한다.
2. MCP 도구가 Jupyter나 런타임을 직접 조작하지 않는다. REST와 MCP 모두
   `NotebookExecutionService`를 호출한다.
3. 기존 `notebook.Manager`는 서버/볼륨 생명주기 소유권을 유지한다. Kernel과
   Notebook 실행은 별도 서비스가 소유하여 두 상태 머신을 섞지 않는다.
4. 코드를 실행하는 MCP 호출은 장기 HTTP 호출로 유지하지 않는다. 실행 리소스를
   만들고 즉시 `execution_id`를 반환하며, 상태 조회와 취소를 별도 호출로 한다.
5. MCP 2025-11-25의 Tasks 기능은 실험적이므로 v1 필수 기능으로 사용하지 않는다.
   안정화 후 Piper 실행 리소스와 Tasks를 매핑할 수 있다.
6. AI가 실행한 코드는 반드시 Notebook 파일 또는 실행 결과 Notebook으로 남긴다.
   추적할 수 없는 일회성 `eval`은 v1에서 제공하지 않는다.
7. Jupyter 접속 토큰은 Piper 내부 비밀이다. REST/MCP/로그/감사 이벤트 어디에도
   반환하지 않는다.

## 2. 목표와 비목표

### 2.1 목표

- 사용자가 브라우저에서 JupyterLab을 직접 사용하는 현재 경험을 보존한다.
- 외부 AI가 프로젝트 범위 안에서 Notebook을 찾고, 읽고, 실행하고, 결과를
  조회할 수 있게 한다.
- 실행 결과를 `.ipynb`와 실행 이력으로 남겨 사람이 Jupyter에서 검토하고 이어서
  작업할 수 있게 한다.
- 세 런타임에서 같은 API와 상태 모델을 제공한다.
- 실행 요청자의 사용자, MCP 클라이언트, 대상 Notebook, 코드 해시, 결과를
  감사할 수 있게 한다.
- 동시 편집, 중단, 재시작, 서버 장애 및 Piper 재시작 시 데이터 손상을 피한다.
- 프로젝트 RBAC와 Home/Member 실행 소유권을 유지한다.

### 2.2 비목표

- JupyterLab UI를 Piper UI로 재구현하지 않는다.
- 새로운 Kernel 프로토콜이나 Notebook 파일 형식을 만들지 않는다.
- MCP 서버 안에 별도 실행 엔진이나 별도 작업 큐를 만들지 않는다.
- 자연어를 코드로 변환하는 모델을 Piper 서버에 내장하지 않는다. 코드 생성은
  MCP 클라이언트의 책임이다.
- Python 소스 문자열을 검사하여 안전성을 판별하려 하지 않는다. 임의 코드 실행은
  문자열 필터가 아니라 런타임 격리, 권한, 승인, 자원 제한으로 통제한다.
- v1에서 협업 편집이나 CRDT를 구현하지 않는다.
- MCP 지원을 이유로 여러 실행 런타임을 한 Piper 인스턴스가 동시에 소유하도록
  바꾸지 않는다. 설치당 `runtime.type` 하나라는 규칙은 유지한다.

## 3. 현재 상태와 갭

현재 구현은 다음 기반을 이미 갖고 있다.

- `pkg/notebook/manager.go`: Notebook 서버와 볼륨의 비동기 생명주기
- `pkg/notebook/driver.go`: 세 런타임에 공통인 서버/볼륨 드라이버 경계
- `pkg/notebook/handler.go`: 프로젝트 범위 CRUD, 시작/중지, 파일 목록 및 프록시
- `pkg/notebook/workspace.go`: 로컬/K8s 볼륨에 대한 안전한 파일 읽기 경계
- `pkg/notebook/dispatch/localdriver`: baremetal/docker 실행
- `pkg/notebook/dispatch/localdriver/k8s`: StatefulSet/PVC/Service 실행
- `/projects/:project_id/notebooks/:name/proxy/*path`: Jupyter UI와 WebSocket 프록시
- Member 터널의 streamed project HTTP: HTTP, SSE 및 WebSocket 전달 기반

하지만 현재 Piper가 다루는 상태는 “Jupyter 서버가 실행 중인가”까지다. 다음은
관리하지 않는다.

- 어떤 Kernel 세션이 누구에 의해 생성됐는가
- 어떤 `.ipynb`를 어느 시점에 실행했는가
- 현재 몇 번째 Cell이 실행 중인가
- 실행 취소와 Kernel interrupt가 성공했는가
- 산출물과 Notebook 출력이 어디에 저장됐는가
- AI가 실행한 코드와 사람이 Jupyter에서 편집한 내용이 충돌했는가

### 3.1 선행 보안 수정

`pkg/notebook/model.go`의 `NotebookServer.Token`은 현재 `json:"token"`이므로
저장소 객체를 그대로 응답하면 Jupyter 토큰이 직렬화될 수 있다. OpenAPI의
`NotebookServer` 스키마에는 token이 없지만, 스키마 생략만으로 실제 응답이
보호되지는 않는다.

구현에 앞서 다음 중 하나를 반드시 적용한다.

- 권장: API 전용 `NotebookServerResponse` DTO를 만들고 허용 필드만 매핑한다.
- 최소 수정: 저장 모델의 token을 `json:"-"`로 바꾸고 모든 응답 테스트에서
  token 부재를 검증한다.

MCP 구현은 이 수정 전에는 활성화하지 않는다. `Endpoint`, `WorkDir`, `PID`도
외부 AI에 기본 반환할 필요가 없으므로 MCP 결과는 REST 저장 모델을 그대로
직렬화하지 않고 별도 공개 모델을 사용한다.

## 4. 제안 아키텍처

### 4.1 패키지 경계

```text
pkg/notebook/
  manager.go                     기존 서버/볼륨 생명주기
  execution/
    model.go                     KernelSession, NotebookExecution
    repository.go                실행/세션 저장소 인터페이스
    service.go                   권한 이후의 도메인 로직과 상태 전이
    scheduler.go                 노트북/커널별 직렬화와 동시성 제한
    recovery.go                  Piper 재시작 후 복구
    handler.go                   프로젝트 REST API
    gateway.go                   Jupyter 제어 추상화
    jupyter/
      client.go                  Contents/Sessions/Kernels REST
      channels.go                Kernel WebSocket 메시지 처리
      notebook.go                ipynb 실행 및 출력 반영
  mcp/
    server.go                    MCP protocol/transport 구성
    tools.go                     도구 입력 검증 및 service 호출
    resources.go                 읽기 전용 resource templates
    public.go                    외부 공개 DTO와 결과 크기 제한
```

실제 디렉터리 이름은 구현 시 조정할 수 있지만 다음 의존 방향은 지켜야 한다.

```text
REST handler ─┐
              ├─> execution.Service ─> execution.Repository
MCP tools  ───┘              │
                             ├─> notebook.Repository / VolumeRepository
                             └─> NotebookGateway ─> Jupyter server
```

`execution.Service`는 Gin이나 MCP 타입을 import하지 않는다. MCP 도구에서 기존
REST handler를 프로세스 내부 HTTP로 호출하는 방식도 사용하지 않는다.

### 4.2 NotebookGateway

런타임 차이를 실행 서비스 밖으로 숨기기 위해 다음과 같은 인터페이스를 둔다.

```go
type NotebookGateway interface {
    ListContents(ctx context.Context, server *notebook.NotebookServer, path string) ([]ContentEntry, error)
    ReadNotebook(ctx context.Context, server *notebook.NotebookServer, path string) (*Document, error)
    SaveNotebook(ctx context.Context, server *notebook.NotebookServer, path string, doc *Document) error

    CreateKernelSession(ctx context.Context, server *notebook.NotebookServer, req CreateSessionRequest) (*KernelSessionInfo, error)
    GetKernelSession(ctx context.Context, server *notebook.NotebookServer, id string) (*KernelSessionInfo, error)
    DeleteKernelSession(ctx context.Context, server *notebook.NotebookServer, id string) error
    InterruptKernel(ctx context.Context, server *notebook.NotebookServer, kernelID string) error
    RestartKernel(ctx context.Context, server *notebook.NotebookServer, kernelID string) error

    Execute(ctx context.Context, server *notebook.NotebookServer, session KernelSessionInfo, req ExecuteRequest, sink OutputSink) error
}
```

기본 구현은 모든 런타임에서 기존 `NotebookServer.Endpoint`, Jupyter에 설정한
`/projects/{project_id}/notebooks/{name}/proxy/` base URL, 내부 token을 조합해
Jupyter Server REST API와 Kernel channels WebSocket에 연결한다. `Endpoint`만
기준으로 `/api/...`를 붙이면 Jupyter의 base URL을 건너뛰어 404가 되므로 공통 URL
builder를 한 곳에 두고 REST와 WebSocket이 함께 사용한다. Piper 서버가 이미
Jupyter browser proxy를 제공하려면 해당 endpoint에 도달할 수 있어야 하므로,
새 worker나 새 tunnel은 필요하지 않다.

Jupyter token은 `NotebookGateway`에만 전달한다. URL query가 로그에 남지 않도록
가능하면 `Authorization` 헤더를 사용하고, 에러에는 endpoint query와 token을
포함하지 않는다.

현재 Jupyter 프로세스는 browser proxy를 전제로 `allow_origin=*`로 시작한다.
MCP 도입으로 Jupyter Service/host port를 외부에 직접 공개하지 않으며, 외부 Origin
검사는 Piper의 proxy/MCP 경계에서 수행한다. Jupyter 자체의 origin 설정을 더
좁힐 수 있는지는 별도 hardening 항목으로 검증한다.

### 4.3 실행 서비스와 Notebook 서버 생명주기

- 실행은 대상 `NotebookServer.Status == running`일 때만 생성할 수 있다.
- 서버가 `stopping`, `stopped`, `failed`로 전환되면 진행 중 실행은
  `failed(server_unavailable)` 또는 명시적 중지의 경우 `cancelled`로 종료한다.
- MCP 실행 요청이 Notebook 서버를 암묵적으로 생성하지 않는다.
- 편의를 위한 `start_notebook_server` 도구는 기존 `notebook.Manager.Restart`를
  호출할 수 있지만, start와 execute는 별도 호출이다. 클라이언트는 running을
  확인한 뒤 실행한다.
- Notebook 서버 삭제는 활성 실행이 있으면 `409 Conflict`를 반환한다. 강제 삭제는
  v1에 넣지 않으며 먼저 실행 취소가 필요하다.

## 5. 도메인 모델과 상태 머신

### 5.1 KernelSession

MCP protocol session과 혼동하지 않도록 API와 코드에서는 항상
`kernel_session`이라는 이름을 사용한다.

```go
type KernelSession struct {
    ID             string
    ProjectID      string
    NotebookName   string
    NotebookPath   string
    JupyterSessionID string // API 응답에는 노출하지 않아도 됨
    KernelID       string   // API 응답에는 노출하지 않아도 됨
    KernelName     string
    Status         string   // starting | idle | busy | restarting | closed | failed
    CreatedBy      string
    ClientID       string
    LastActivityAt time.Time
    CreatedAt      time.Time
    ClosedAt       *time.Time
}
```

- Piper ID와 Jupyter ID를 분리한다. 외부 호출자는 Piper ID만 사용한다.
- 한 Kernel에는 동시에 하나의 execute 요청만 전달한다.
- 유휴 TTL이 지나면 Kernel을 종료하고 상태를 `closed`로 만든다.
- 사용자가 Jupyter UI에서 만든 세션은 v1 관리 대상이 아니다. Piper가 만든
  세션만 소유하고 종료한다.

### 5.2 NotebookExecution

```go
type NotebookExecution struct {
    ID              string
    ProjectID       string
    NotebookName    string
    NotebookPath    string
    ResultPath      string
    KernelSessionID string
    Kind            string // notebook | cell
    Status          string
    RequestedBy     string
    ClientID        string
    IdempotencyKey  string
    SourceSHA256    string
    BaseContentHash string
    CurrentCell     int
    TotalCells      int
    ErrorCode       string
    ErrorMessage    string
    OutputSummary   []byte // 제한된 구조화 JSON
    QueuedAt        time.Time
    StartedAt       *time.Time
    FinishedAt      *time.Time
    UpdatedAt       time.Time
}
```

상태 전이는 다음과 같다.

```text
awaiting_approval ── approve ─┐
                             ▼
queued ──> running ──> succeeded
  │          │  │  └─> conflicted
  │          │  ├────> failed
  │          │  ├────> timed_out
  │          │  └────> cancelling ──> cancelled
  │          └────────> cancelled
  └───────────────────> cancelled

awaiting_approval ── deny/expire ──> cancelled
```

터미널 상태는 `succeeded`, `conflicted`, `failed`, `timed_out`,
`cancelled`다. `conflicted`는 코드 실행 자체는 끝났지만 사람이 수정한 원본
Notebook에 결과를 안전하게 반영하지 못한 경우다. 이때 `ResultPath`에는 복구
가능한 실행 결과 Notebook이 남아야 한다.

### 5.3 저장 정책

- 실행 메타데이터와 상태는 SQLite/Postgres에 저장한다.
- 코드 원문과 rich output 전체를 DB에 복제하지 않는다.
- Notebook 실행 결과는 대상 `.ipynb`에 반영한다.
- 대상이 충돌했거나 실행이 중간 실패한 경우에도
  `.piper/executions/{execution_id}/result.ipynb`에 복구본을 남긴다.
- `cell` 실행의 입력 코드도 대상 Notebook에 새 cell로 추가하거나 지정한
  `cell_id`를 교체한 뒤 실행한다. DB 감사 레코드에는 원문 대신 SHA-256을 남긴다.
- `OutputSummary`는 상태 표시용으로 제한하며 기본 최대 64 KiB다.
- 바이너리/HTML/이미지 출력은 MCP 응답에 통째로 넣지 않고 Notebook 또는 파일
  resource URI를 반환한다.

DB migration은 SQLite와 Postgres에 같은 순서로 추가하고 repository conformance
suite에 두 backend를 모두 포함한다.

## 6. 실행 의미론

### 6.1 전체 Notebook 실행

1. 프로젝트 권한, 서버 상태, 경로와 확장자를 검증한다.
2. `Idempotency-Key`로 중복 요청을 확인한다.
3. 원본 Notebook을 읽고 canonical content SHA-256을 계산한다.
4. 실행 레코드를 `queued` 또는 `awaiting_approval`로 저장한다.
5. scheduler가 Notebook/Kernel 동시성 슬롯을 확보한다.
6. Kernel session을 생성하거나 요청된 Piper kernel session을 확인한다.
7. code cell을 문서 순서대로 실행한다.
8. 각 cell의 `stream`, `display_data`, `execute_result`, `error` 메시지를 ipynb
   output으로 변환하고 진행률을 갱신한다.
9. 매 cell timeout과 전체 실행 timeout을 모두 적용한다.
10. 실행 결과 복구본을 고유한 `ResultPath`에 먼저 저장한다.
11. 원본 content hash가 바뀌지 않았을 때만 대상 Notebook에 결과를 반영한다.
12. 상태와 요약을 터미널 상태로 갱신하고 event를 발행한다.

Jupyter UI는 Piper의 잠금을 모르므로 hash 검사는 완전한 분산 lock이 아니다.
따라서 Piper는 원본을 먼저 덮어쓰지 않고 항상 고유 결과 복구본을 만든다. UI와
동시 편집이 감지되면 원본을 보존하고 `conflicted`로 종료하는 것을 우선한다.

### 6.2 Cell 실행

v1의 cell 실행은 추적 가능한 두 모드만 허용한다.

- `append`: 코드 cell을 Notebook 끝에 추가하고 실행
- `replace`: 명시한 안정적 `cell_id`의 source를 교체하고 실행

cell index만으로 replace하지 않는다. 사람이 cell을 삽입하면 index 의미가 바뀌기
때문이다. 대상 Notebook이 없을 때는 `create_if_missing=true`와 `.ipynb` 경로가
함께 주어진 경우에만 새 Notebook을 만든다.

### 6.3 취소와 timeout

- queued/awaiting 실행 취소는 Kernel에 접근하지 않고 상태만 바꾼다.
- running 실행 취소는 먼저 Jupyter interrupt를 호출한다.
- grace period 안에 idle 상태가 오지 않으면 Piper가 소유한 Kernel만 restart한다.
- 다른 실행이나 Jupyter UI가 만든 Kernel은 취소 과정에서 종료하지 않는다.
- 클라이언트의 MCP/HTTP 연결 종료는 실행 취소로 간주하지 않는다. 반드시 cancel
  API/tool을 호출해야 한다.

## 7. REST API

모든 리소스는 기존 규약대로 `/api/projects/{project_id}` 아래에 둔다. 구현 시
`docs/openapi.yaml`을 코드와 같은 변경에 포함한다.

### 7.1 Contents

| Method | Path | 최소 역할 | 설명 |
|---|---|---:|---|
| `GET` | `/notebooks/{name}/contents?path=` | viewer | 디렉터리 또는 파일 메타데이터 |
| `GET` | `/notebooks/{name}/documents?path=x.ipynb` | viewer | 제한된 크기의 Notebook 문서 읽기 |
| `PUT` | `/notebooks/{name}/documents?path=x.ipynb` | member | Notebook 생성/교체, `base_hash` 충돌 검사 |

경로는 반드시 `CleanWorkspacePath`를 통과하고, Notebook 문서 API는 기본적으로
`.ipynb`만 허용한다. 일반 파일 읽기는 별도 allowlist와 크기 제한을 둔다.

### 7.2 Kernel sessions

| Method | Path | 최소 역할 | 결과 |
|---|---|---:|---|
| `POST` | `/notebooks/{name}/kernel-sessions` | member | `201 KernelSession` |
| `GET` | `/notebooks/{name}/kernel-sessions` | member | 호출자가 소유한 세션 목록; admin은 전체 |
| `GET` | `/notebooks/{name}/kernel-sessions/{id}` | member | 공개 세션 상태 |
| `POST` | `/notebooks/{name}/kernel-sessions/{id}/interrupt` | member | `204` |
| `POST` | `/notebooks/{name}/kernel-sessions/{id}/restart` | member | `204` |
| `DELETE` | `/notebooks/{name}/kernel-sessions/{id}` | member | `204` |

### 7.3 Executions

| Method | Path | 최소 역할 | 결과 |
|---|---|---:|---|
| `POST` | `/notebooks/{name}/executions` | member | `201 NotebookExecution` |
| `GET` | `/notebooks/{name}/executions?limit=&offset=` | viewer | 실행 이력 + `X-Total-Count` |
| `GET` | `/notebooks/{name}/executions/{id}` | viewer | 상태/진행률/요약 |
| `POST` | `/notebooks/{name}/executions/{id}/cancel` | owner/admin | `204` |
| `POST` | `/notebooks/{name}/executions/{id}/approve` | admin | `204` |
| `POST` | `/notebooks/{name}/executions/{id}/deny` | admin | `204` |

생성 요청 예시:

```json
{
  "kind": "notebook",
  "path": "experiments/train.ipynb",
  "kernel_session_id": "optional-piper-session-id",
  "timeout_seconds": 1800
}
```

cell 요청 예시:

```json
{
  "kind": "cell",
  "path": "experiments/ai-analysis.ipynb",
  "edit": {
    "mode": "append",
    "code": "df.describe()"
  },
  "create_if_missing": true,
  "timeout_seconds": 300
}
```

모든 execution 생성은 최대 128자의 `Idempotency-Key`를 지원한다. 같은 프로젝트,
actor, target, key와 같은 payload는 기존 실행을 반환하고, payload가 다르면
`409 Conflict`다.

## 8. MCP 서버 설계

### 8.1 Transport와 endpoint

원격 AI 클라이언트를 위해 MCP의 Streamable HTTP transport를 제공한다.

```text
POST/GET /api/projects/{project_id}/mcp
```

프로젝트 ID를 MCP tool argument로 받지 않고 endpoint 경로와 인증 컨텍스트에
고정한다. 이렇게 하면 한 연결의 tool 호출이 다른 프로젝트를 실수로 조작하는
것을 막고, 기존 project routing/RBAC/federation 경계를 재사용할 수 있다.

MCP 2025-11-25 기준으로 구현하며 다음을 지킨다.

- 하나의 endpoint가 POST와 선택적 GET/SSE를 처리한다.
- `Origin`과 `Host` allowlist를 검증하여 DNS rebinding을 막는다.
- `MCP-Protocol-Version` 협상과 지원 버전을 검증한다.
- 서버가 session ID를 발급하면 사용자, 프로젝트, client ID에 바인딩하고 TTL을
  둔다. 단, 도메인 실행 상태는 MCP session 메모리에 저장하지 않는다.
- HTTP 연결 또는 MCP session이 사라져도 execution은 DB에 남고 계속 진행한다.
- 구형 HTTP+SSE transport는 v1에서 제공하지 않는다.

참고 사양:
[MCP transports](https://modelcontextprotocol.io/specification/2025-11-25/basic/transports),
[MCP authorization](https://modelcontextprotocol.io/specification/2025-11-25/basic/authorization),
[MCP tasks](https://modelcontextprotocol.io/specification/2025-11-25/basic/utilities/tasks).

2026-07 stateless MCP 사양은 작성 시점에 release candidate다. 구현 시작 시 안정
사양을 다시 확인하되, 이 설계의 도메인 실행 리소스는 transport session과
분리되어 있으므로 stateless 전환에도 유지된다.

### 8.2 Tools

도구 이름은 짧고 Piper namespace가 드러나게 한다.

| Tool | 역할 | 변경 여부 | 반환 |
|---|---:|---:|---|
| `piper_list_notebook_servers` | viewer | 읽기 | 공개 서버 상태 목록 |
| `piper_get_notebook_server` | viewer | 읽기 | 공개 서버 상태 |
| `piper_list_notebook_files` | viewer | 읽기 | 경로/크기/수정 시각 |
| `piper_read_notebook` | viewer | 읽기 | 축약된 ipynb 또는 resource URI |
| `piper_start_notebook_server` | member | 변경 | 서버 상태; 실행은 별도 |
| `piper_create_kernel_session` | member | 변경 | Piper kernel session ID |
| `piper_execute_notebook` | member | 변경/코드 실행 | execution ID |
| `piper_execute_cell` | member | 변경/코드 실행 | execution ID |
| `piper_get_execution` | viewer | 읽기 | 상태/진행률/결과 URI |
| `piper_list_executions` | viewer | 읽기 | 페이지된 실행 이력 |
| `piper_cancel_execution` | owner/admin | 변경 | 최종/취소 중 상태 |
| `piper_close_kernel_session` | owner/admin | 변경 | 종료 확인 |

초기 릴리스에서는 서버 삭제, volume purge, 패키지 설치 전용 도구를 제공하지
않는다. 패키지 설치가 필요하면 격리된 이미지/prepare spec으로 환경을 만들거나,
승인된 실행 코드가 Kernel 권한 안에서 수행한다.

각 tool은 JSON Schema input/output을 선언하고 structured output을 반환한다.
annotation은 다음 원칙으로 정확히 표시한다.

- 조회 tool: `readOnlyHint=true`, `openWorldHint=false`
- 실행 tool: `readOnlyHint=false`, `destructiveHint=true`,
  `idempotentHint=false`, `openWorldHint`는 런타임 네트워크 정책에 맞춤
- cancel/close: `destructiveHint=true`; 재호출 안전성이 보장될 때만
  `idempotentHint=true`

annotation은 클라이언트 UI 힌트일 뿐이므로 Piper의 서버 측 권한/승인 검사를
대체하지 않는다.

### 8.3 Resources

큰 결과와 읽기 가능한 문서는 MCP resource template으로 제공한다.

```text
piper://projects/{project_id}/notebooks/{name}/documents/{path}
piper://projects/{project_id}/notebook-executions/{execution_id}
piper://projects/{project_id}/notebook-executions/{execution_id}/result
piper://projects/{project_id}/notebooks/{name}/files/{path}
```

- URI의 project는 현재 endpoint project와 반드시 일치해야 한다.
- text 파일과 Notebook JSON은 설정된 byte limit까지 inline으로 반환한다.
- 이미지 등 binary는 지원 MIME type과 크기 제한을 적용한다.
- symlink/`..`/absolute path escape는 `WorkspaceReader`와 같은 규칙으로 거부한다.
- resource response에도 token, host path, 내부 endpoint를 포함하지 않는다.

Prompts는 핵심 실행 기능이 아니므로 v1 범위에서 제외한다.

### 8.4 MCP Tasks와 Piper execution의 관계

MCP 2025-11-25 Tasks는 장기 작업에 잘 맞지만 실험적이다. v1은 모든 실행 tool이
일반 structured result로 `execution_id`, `status`, `poll_after_ms`를 반환하게 한다.
클라이언트는 `piper_get_execution`을 호출한다.

향후 Tasks가 안정되면 tool의 `taskSupport`를 추가하고 아래처럼 1:1 매핑한다.

```text
MCP task ID <── mapping ──> Piper NotebookExecution.ID
tasks/get                 -> execution.Service.Get
tasks/cancel              -> execution.Service.Cancel
tasks/result              -> execution summary + result resource link
```

Piper 실행 리소스는 MCP가 아닌 UI/REST 사용자에게도 필요하므로 Tasks로 대체하지
않는다.

## 9. 인증, 권한 및 승인

### 9.1 프로젝트 RBAC

| 작업 | viewer | member | admin |
|---|:---:|:---:|:---:|
| 서버/파일/실행 이력 조회 | O | O | O |
| Kernel 생성/자신의 Kernel 종료 |  | O | O |
| Notebook/cell 실행 |  | O | O |
| 자신의 실행 취소 |  | O | O |
| 타인의 실행/Kernel 취소 |  |  | O |
| MCP 정책과 무인 실행 권한 관리 |  |  | O |

Notebook 출력에는 원본 데이터가 포함될 수 있으므로 현재 프로젝트 viewer가
Jupyter/Notebook 콘텐츠를 읽을 수 있다는 제품 정책을 명시적으로 재확인해야
한다. 더 좁은 권한이 필요하면 `notebook:read` scope를 프로젝트 role과 별도로
추가하는 후속 설계가 필요하다.

### 9.2 인증 단계

Piper의 현재 access JWT는 브라우저 로그인용이며 표준 OAuth authorization server,
Protected Resource Metadata, audience-bound MCP token을 제공하지 않는다. 이를
그대로 “MCP OAuth 지원”이라고 부르면 안 된다.

단계별 지원은 다음과 같다.

1. **개발/사내 v1:** Piper bearer token 또는 프로젝트 범위의 해시 저장 access
   token을 사용한다. HTTPS와 짧은 만료를 강제하고 UI에는 token을 다시 표시하지
   않는다. 이 모드는 MCP OAuth 상호운용 모드가 아님을 문서화한다.
2. **원격 운영:** MCP endpoint를 OAuth 2.1 resource server로 제공한다. Protected
   Resource Metadata, authorization server discovery, PKCE 호환 client flow,
   `resource` indicator 및 audience 검증을 지원한다. Piper가 완전한 OAuth
   authorization server를 새로 구현하기보다는 OIDC/OAuth provider와 연동하는
   것을 우선한다.
3. MCP inbound token을 Jupyter로 전달하지 않는다. Piper가 보관한 별도 Jupyter
   token으로 내부 요청을 만들며, token passthrough를 금지한다.

### 9.3 실행 승인 정책

AI 코드 실행은 일반 CRUD보다 위험하다. 프로젝트별 정책을 둔다.

```yaml
notebook_execution:
  mcp_policy: approval_required # disabled | approval_required | allowed
```

- `disabled`: MCP read tools만 제공
- `approval_required`: 실행 레코드를 `awaiting_approval`로 만들고 admin이 UI/API에서
  승인해야 queue 진입
- `allowed`: member가 즉시 실행 가능; 격리된 자동화 프로젝트에 적합

기본값은 `approval_required`다. `baremetal`에서는 호스트 권한과 파일/네트워크
경계가 약하므로 명시적 운영자 설정 없이는 `allowed`를 허용하지 않는다.

## 10. 런타임별 보안과 자원 제한

AI가 Kernel에서 실행하는 코드는 해당 Jupyter 서버 사용자와 동일한 권한을 가진다.
“금지 함수 목록”으로 이를 안전하게 만들 수 없다.

### 10.1 공통

- Notebook manifest의 CPU/memory/GPU 제한을 그대로 적용한다.
- 실행 전체 timeout, cell timeout, 최대 queued 실행 수, 최대 Kernel 수를 둔다.
- MCP 결과 byte limit과 파일 read limit을 둔다.
- secret/token을 환경 변수나 output에 자동 주입하지 않는다.
- outbound network 허용 여부는 런타임 정책으로 결정하고 tool metadata에 반영한다.
- code 원문을 일반 애플리케이션 로그에 남기지 않는다.

### 10.2 baremetal

- Jupyter 프로세스가 Piper 호스트와 강한 격리를 공유하지 않는 현재 한계를 UI와
  설정에 표시한다.
- 기본 MCP 정책은 `disabled` 또는 `approval_required`다.
- unattended 실행을 활성화하려면 명시적인 위험 승인 설정이 필요하다.
- 향후 OS sandbox가 추가되기 전에는 다중 테넌트 보안 경계로 간주하지 않는다.

### 10.3 docker

- 기존 Notebook container의 CPU/memory/device 제한을 검증한다.
- Docker socket과 Piper 데이터 루트 전체를 mount하지 않는다.
- Notebook volume만 쓰기 가능하게 하고 불필요한 capability를 제거한다.
- 운영 환경에서는 egress 정책을 별도 네트워크 구성으로 제한한다.

### 10.4 k8s

- 기존 namespace allowlist를 벗어나지 않는다.
- Notebook pod service account에 불필요한 Kubernetes API 권한을 주지 않는다.
- ResourceQuota/LimitRange/NetworkPolicy 적용을 권장한다.
- Kernel 실행을 위해 새로운 privileged sidecar나 worker tunnel을 추가하지 않는다.

## 11. 동시성, 복구 및 오류 처리

### 11.1 동시성

기본 제한값:

```yaml
notebook_execution:
  max_running_per_notebook: 1
  max_kernels_per_notebook: 2
  max_queued_per_project: 20
  kernel_idle_ttl: 30m
  cell_timeout: 5m
  execution_timeout: 1h
  inline_output_bytes: 65536
  file_read_bytes: 1048576
```

- 같은 Kernel의 실행은 항상 직렬화한다.
- 같은 Notebook path에 결과를 쓰는 실행도 직렬화한다.
- 서로 다른 Notebook path와 서로 다른 Kernel은 설정 범위에서 병렬 실행할 수 있다.
- 제한 초과는 무한 대기 대신 `429 Too Many Requests` 또는 bounded queue로 처리한다.

### 11.2 Piper 재시작

시작 시 `queued`, `running`, `cancelling` 실행을 스캔한다.

- queued: 정책과 서버 상태가 유효하면 재queue
- running: Jupyter session/kernel 존재 여부와 execution marker를 확인
- Kernel이 idle이고 결과 복구본이 있으면 결과를 검증해 완료 처리
- 진행 여부를 증명할 수 없으면 같은 cell을 자동 재실행하지 않고
  `failed(recovery_uncertain)` 처리
- cancelling: interrupt를 재시도하고 terminal 상태로 수렴

임의 코드의 중복 실행은 외부 부작용을 만들 수 있으므로 “모르면 재실행”하지
않는다. 사용자가 새 Idempotency-Key로 명시적으로 다시 실행해야 한다.

### 11.3 오류 코드

외부 API/MCP 결과는 안정적인 code와 사람용 message를 분리한다.

- `notebook_not_running`
- `kernel_unavailable`
- `kernel_died`
- `execution_timeout`
- `execution_cancelled`
- `content_conflict`
- `path_invalid`
- `output_too_large`
- `approval_required`
- `approval_denied`
- `runtime_unavailable`
- `recovery_uncertain`

Jupyter 내부 응답, token, host path는 message에 그대로 포함하지 않는다.

## 12. Federation

Notebook 실행의 실제 소유자는 프로젝트를 소유한 Member다. Home은 실행 상태를
복제하거나 Kernel에 직접 연결하지 않는다.

- `/api/projects/{project_id}/mcp`도 다른 project API와 같은 fail-closed routing을
  사용한다.
- Home은 인증한 actor ID/role을 delegation에 넣고 Member가 다시 검증한다.
- Streamable HTTP POST/GET/SSE는 기존 streamed project HTTP 경로를 사용한다.
- MCP session ID는 Member가 발급하고 Home은 opaque header로 전달한다.
- 연결 종료가 Member의 실행 취소로 변환되지 않도록 한다.
- 큰 output은 터널 frame에 반복 복제하지 않고 resource read에 byte limit을 둔다.
- 각 runtime type을 가진 독립 Member에서 별도로 QA한다. Home에서 성공 응답만
  보는 것으로 실행 성공을 판정하지 않고 Member/Piper/Jupyter 로그를 대조한다.

구현 시 다음 회귀 테스트가 필요하다.

- MCP initialize와 tool call이 Home→Member를 통과
- SSE flush와 reconnect
- 잘못된 project delegation 거부
- Member disconnect 중 execution 지속 및 reconnect 후 조회
- remote project가 Home의 local notebook repository로 fall through하지 않음

## 13. 이벤트, 감사 및 관측성

### 13.1 이벤트

기존 `internal/event.Bus`에 다음 이벤트를 발행한다.

- `notebook.execution.queued`
- `notebook.execution.started`
- `notebook.execution.progress`
- `notebook.execution.succeeded`
- `notebook.execution.failed`
- `notebook.execution.cancelled`
- `notebook.execution.conflicted`
- `notebook.kernel.created`
- `notebook.kernel.closed`

progress event는 cell마다 무제한 발행하지 않고 변경 coalescing 또는 최소 간격을
둔다. 이벤트 payload에는 code 원문과 output 전체를 넣지 않는다.

### 13.2 감사 필드

각 변경 작업에 다음을 남긴다.

- actor ID와 프로젝트 role
- MCP client ID 또는 REST/UI source
- tool/action 이름
- project, notebook server, notebook path, execution ID
- source SHA-256
- 승인자와 승인 시각
- 시작/종료/취소 및 결과 code
- remote IP는 신뢰 가능한 proxy chain 설정이 있을 때만 기록

Jupyter token, inbound bearer token, code 원문, rich output 전체는 감사 테이블에
저장하지 않는다.

### 13.3 Metrics와 logs

권장 metrics:

- `piper_notebook_executions_total{runtime,status,source}`
- `piper_notebook_execution_duration_seconds{runtime,status}`
- `piper_notebook_execution_queue_depth{project}`는 project label cardinality 검토 후
  제한하거나 aggregate
- `piper_notebook_kernels{runtime,status}`
- `piper_mcp_requests_total{method,status}`
- `piper_mcp_tool_calls_total{tool,status}`
- `piper_mcp_active_sessions`

로그는 `project_id`, `notebook`, `execution_id`, `actor_id`, `client_id`를 구조화
필드로 사용하고 code/output/token은 제외한다.

## 14. UI 변경

JupyterLab을 대체하지 않고 관리와 검토만 추가한다.

### 14.1 Notebook detail

기존 `NotebookDetailPanel`에 다음을 추가한다.

- 최근 실행 상태와 실행 이력 route 링크
- running일 때 “Open Jupyter” 유지
- 실행 중인 Piper kernel/session 수
- MCP 실행 정책 표시
- 내부 endpoint, token, host work dir는 일반 사용자에게 표시하지 않음

### 14.2 Execution history

프로젝트 route에 Notebook execution history를 추가한다.

- `DataBodyTemplate.Resource` + server-side pagination
- 검색은 현재 page의 notebook/path/id를 대상으로 하는 기존 패턴 준수
- row click은 `SidePanelProvider`의 detail panel을 연다.
- detail은 cell 진행률, 요청자, source, 결과 경로, error code, 승인 정보를 표시한다.
- `awaiting_approval` 상태는 admin에게 approve/deny action을 제공한다.
- Notebook 내용 수정과 코드 작성은 JupyterLab에서 수행하며, Piper에 별도 코드
  editor를 만들지 않는다.

### 14.3 MCP 연결 안내

프로젝트 admin 화면에 다음만 제공한다.

- project-bound MCP endpoint
- 현재 read/execute/approval 정책
- 인증 연결 생성/폐기
- 마지막 사용 시각과 client 이름

token은 생성 직후 한 번만 보여주고 이후에는 hash만 저장한다. create form은 전용
route, 연결 목록은 DataGrid, 상세는 SidePanel이라는 프런트엔드 가이드를 따른다.

## 15. 설정

제안 설정 예시:

```yaml
mcp:
  enabled: false
  public_url: "https://piper.example.com"
  allowed_origins:
    - "https://trusted-ai-client.example.com"
  auth_mode: piper_token # piper_token | oauth_resource_server
  protocol_versions:
    - "2025-11-25"
  session_ttl: 30m

notebook_execution:
  enabled: false
  mcp_policy: approval_required
  max_running_per_notebook: 1
  max_kernels_per_notebook: 2
  max_queued_per_project: 20
  kernel_idle_ttl: 30m
  cell_timeout: 5m
  execution_timeout: 1h
  inline_output_bytes: 65536
  file_read_bytes: 1048576
```

두 기능은 기본 비활성화한다. MCP read-only만 켜고 execution을 끄는 구성이
가능해야 한다. config validation은 다음을 거부한다.

- `mcp.enabled=true`인데 인증 방식이 없거나 production public URL이 HTTP
- 빈 Origin allowlist로 public bind
- 음수/0 timeout과 비현실적인 output limit
- baremetal에서 별도 위험 승인 없이 unattended `mcp_policy=allowed`
- 지원하지 않는 MCP protocol version

## 16. 구현 단계

### Phase 0 — 보안과 계약 정리

- `NotebookServer.Token` 직렬화 제거 및 회귀 테스트
- 공개 DTO에서 endpoint/work_dir/pid 노출 범위 결정
- OpenAPI의 현재 Notebook 응답과 실제 JSON 일치
- 프로젝트의 Notebook content viewer 정책 확정

### Phase 1 — 공통 Jupyter execution core

- `NotebookGateway`와 Jupyter client
- KernelSession/NotebookExecution repository와 양 DB migration
- async scheduler, timeout, interrupt, recovery
- REST API와 OpenAPI
- 실행 이력/승인 UI
- MCP 없이 세 런타임에서 먼저 검증

### Phase 2 — MCP read-only

- Streamable HTTP initialize, tools/list, resources/list/read
- project-bound routing, auth, Origin/Host 검증
- list/get/read tools
- Home/Member streamed HTTP 검증

### Phase 3 — MCP execution

- kernel/execute/get/cancel tools
- approval workflow
- idempotency와 output/resource limits
- 감사/metrics
- client interoperability tests

### Phase 4 — 운영 인증과 안정화

- OAuth resource server mode와 Protected Resource Metadata
- audience/resource validation
- token/client 관리 UI
- 안정된 MCP Tasks 사양이 있으면 optional mapping

각 phase는 독립적으로 feature flag 뒤에 배포할 수 있어야 한다.

## 17. 테스트 및 검증 계획

### 17.1 단위/계약 테스트

- ipynb parse/save와 Jupyter message→output 변환
- stream/display/error/clear_output/display_id update 처리
- path traversal, symlink escape, oversized file/output 거부
- token/endpoint/work_dir가 public DTO에 없는지 확인
- 상태 머신의 모든 허용/거부 전이
- idempotency replay와 payload conflict
- actor ownership/RBAC/approval
- cancel/timeout/restart escalation
- SQLite/Postgres repository conformance
- MCP JSON Schema와 structured output
- Origin/Host/protocol version/auth rejection

### 17.2 Jupyter 통합 테스트

- 실제 Jupyter Server에 session 생성
- code cell 순차 실행과 execution count/output 저장
- stdout/stderr/error/rich image 결과
- interrupt 가능한 long-running cell
- dead kernel와 restart
- Jupyter UI 동시 수정으로 `conflicted` 및 복구본 생성
- Piper 재시작 후 queued/running/cancelling 복구

### 17.3 런타임별 adversarial QA

`docs/qa/adversarial-qa-playbook.md`에 따라 `baremetal`, `docker`, `k8s`를 각각
별도 설정한 실제 Piper 인스턴스로 검증한다.

각 런타임에서:

1. 실제 UI form으로 Notebook 생성
2. JupyterLab에서 사람이 cell 작성/실행
3. REST execution으로 같은 volume의 Notebook 실행
4. MCP client로 read→execute→poll→result 수행
5. timeout, cancel, server stop, concurrent edit, oversized output 수행
6. UI 결과, DB 상태, `.ipynb`, Piper 로그, Jupyter/runtime 로그 대조
7. 재시작 후 실행/Kernel/volume 상태 검증

K8s는 namespace allowlist, RBAC, NetworkPolicy/egress 조건을 포함하고, Docker는
bind mount와 자원 제한, baremetal은 host isolation 경고와 approval 기본값을
확인한다.

### 17.4 완료 기준

- Jupyter UI 사용 흐름에 회귀가 없다.
- 동일한 `.ipynb`가 UI와 MCP 양쪽에서 보이며 실행 결과가 보존된다.
- MCP 연결 종료 후에도 execution을 ID로 재조회할 수 있다.
- 중복 요청과 Piper 재시작이 cell을 몰래 재실행하지 않는다.
- 사용자 동시 편집을 덮어쓰지 않고 복구본을 남긴다.
- REST/MCP/로그 어디에도 Jupyter token이 노출되지 않는다.
- viewer/member/admin 경계와 approval 정책이 서버에서 강제된다.
- 세 runtime과 federated Home→Member 경로의 실제 검증이 완료된다.

## 18. 결정이 필요한 항목

구현 시작 전에 다음을 확정해야 한다.

1. 프로젝트 viewer에게 Notebook 본문과 output 읽기를 허용할지, 별도 권한이
   필요한지
2. 개발/사내 v1 token을 기존 사용자 session과 분리된 project access token으로
   만들지
3. OAuth 운영 모드에서 사용할 IdP와 Piper user `(issuer, subject)` 매핑 방식
4. 실행 결과 Notebook의 보존 TTL과 volume purge 시 감사 메타데이터 처리
5. `approval_required` 승인 가능 역할을 admin만으로 제한할지, 별도 approver
   권한을 둘지
6. v1에서 `replace cell`을 포함할지, 더 안전한 append-only로 시작할지

기본 권고는 **viewer read 허용 여부를 먼저 제품 결정하고, project access token,
admin 승인, append-only cell 실행으로 시작**하는 것이다. OAuth와 replace-cell은
core 실행과 MCP read-only가 안정된 뒤 확장한다.
