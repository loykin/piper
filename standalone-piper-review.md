# Piper 실행 아키텍처 재검토 — abc.md 검증과 수정안

> 상태: 결론 남 — 이 문서의 수정안(RuntimeController + baremetal만 tunnel
> 유지)이 아니라 `abc.md`의 원안대로 **전부 삭제**가 채택되어 실행 완료됨
> (2026-08-12, `standalone-piper-review` 브랜치). "개발중이므로 §3.1이
> 지적한 실사용 케이스(원격 baremetal 다중 등록, README의 `master_url`
> 예시, AGENTS.md의 다중 워커 QA 요구)를 유지할 필요가 없다"는 것이
> 채택 근거였다 — 이 문서가 놓친 전제가 아니라, 검토 이후 바뀐 우선순위다.
> `internal/grpcagent`, `internal/agent`, 원격 Worker 구조체, worker CLI,
> `WorkerPodPolicy`가 전부 삭제되었고, `AGENTS.md`/
> `docs/qa/adversarial-qa-playbook.md`도 다중 워커 QA 요구를 제거하도록
> 갱신되었다. 이 문서는 그 판단에 이르기까지의 근거 기록으로 남긴다 —
> §3.2의 인프라별 분석(k8s/docker는 tunnel을 걷어낼 구조적 이유가 있고
> baremetal만 원격 실행 채널이 필요하다는 지적)은 여전히 유효한 관찰이며,
> baremetal 원격 실행이 다시 필요해지면 참고할 것.
>
> 이 문서는 `abc.md`("Standalone Piper 아키텍처")가 제안한 master-worker
> 완전 제거 방향을 실제 코드베이스(main 브랜치) 근거로 검증하고, 발견된
> 문제를 반영한 수정안을 제시한다. `abc.md`를 대체하지 않고, 그 위에 쌓는
> 검토 결과였다.

## 1. abc.md의 핵심 주장

Piper의 실행 모델에서 master-worker 구조(worker registry, tunnel,
placement, lease/heartbeat, remote dispatch)를 완전히 제거하고, 각 Piper
설치본이 설치 시 고른 런타임(process/docker/k8s) 하나만 로컬에서
실행하는 self-contained 시스템으로 전환하자는 제안. 근거는 §2에 나열된
분산 경계의 유지비(placement, ambiguity, lease, reconnect, remote
cancellation 등)가 단일 환경 사용 모델의 가치보다 크다는 것.

## 2. 검증: 맞는 부분

`abc.md` §2가 나열한 복잡도 목록은 과장이 아니다. `worker-state-ownership`
브랜치에서 진행한 리뷰 1~4라운드의 실제 수정 사항 중 다음 네 건은 전부
"여러 워커/여러 동시 dispatch 후보가 있다"는 전제에서만 존재할 수 있는
버그였다.

- 병렬 root step에서 binding 완료 전 dispatch 가능 (동시 dispatch 조율)
- master 재시작 복구 시 저장된 worker binding 미사용
- Dispatch waiter의 `ctx.Done()` 경로에서 router capacity 누수
- cancel과 SendRPC 사이의 race window

후보가 항상 하나였다면 `router.Reserve`의 capacity 예약,
`AmbiguousInfrastructureError`, 동시 root-step의 `bindingDone` 대기
조율 자체가 존재할 이유가 없고, 이 버그들도 원천적으로 발생할 수
없었다. "N개 후보 중 하나를 고른다"는 문제를 풀기 위한 코드가 이번
세션에서 고친 버그의 상당 부분을 만들어냈다는 것은 근거 있는 지적이다.

## 3. 검증: 놓친 부분

### 3.1 실사용 케이스와의 충돌

`abc.md` §1은 "설치 시 baremetal/docker/k8s 중 하나만 고른다"를
전제로 한다. 그런데 실제 문서/예제는 다음을 정식 사용법으로 제시한다.

- `README.md`(worker 설정 예시): `master_url: https://piper.example.com`
  (공인 원격 주소), `labels: { accelerator: cpu }` — label이 있다는
  것은 후보가 여럿이라는 전제가 실제로 있다는 뜻이다.
- `examples/bare-metal/worker/main.go`의 사용법 주석:
  `go run ./examples/bare-metal/worker --master=http://remote:8080` —
  master와 물리적으로 분리된 원격 호스트에서의 실행을 정식 예시로
  문서화하고 있다.
- `AGENTS.md`와 `docs/qa/adversarial-qa-playbook.md`는 baremetal/docker/k8s
  워커를 **동시에 여러 개 등록한 상태**를 QA 완료 조건으로 요구한다
  ("a single-worker environment cannot surface them at all").

즉 "설치본마다 런타임 하나"는 현재 실사용 패턴과 맞지 않는다. `abc.md`가
이 트레이드오프(실사용 케이스를 버린다는 사실 자체)를 언급하지 않고
넘어간 것이 가장 큰 공백이다.

### 3.2 인프라 타입별로 사정이 다르다

"워커가 왜 필요한가"를 인프라 타입별로 나눠서 다시 보면 결론이 갈린다.

- **k8s**: `deploy/k8s/k8s-worker.yaml`의 "k8s worker"는 실제로는
  master와 **같은 클러스터** 안에서 k8s API로 Job을 만드는 프로세스다.
  master가 RBAC만 맞으면 그 k8s API에 직접 접근 가능한 거리에 있다 —
  tunnel을 거칠 구조적 이유가 없다.
- **docker**: Docker API 자체가 이미 TCP+TLS로 원격 접근을 지원해서
  tunnel 없이도 될 것처럼 보이지만, **현재 구현은 워커 자신의 바이너리를
  `os.Executable()`로 스텝 컨테이너에 bind-mount하는 방식**이다
  (`README.md` "A Docker worker runs each pipeline step by bind-mounting
  its own running binary"). bind mount는 호스트 경로 참조라서, 이 방식을
  유지하는 한 **master가 Docker daemon과 같은 호스트에 있어야** 한다는
  제약이 붙는다. "Docker API가 원격을 지원하니 tunnel이 필요 없다"는
  결론은 이 구현 세부사항을 먼저 바꾸지 않으면 성립하지 않는다.
- **baremetal**: §3.1의 근거대로, master와 분리된 원격 호스트 + label
  기반 다중 후보 선택이 실제 사용법으로 문서화돼 있다. OS 프로세스
  실행에는 k8s API/Docker API 같은 "원격 실행 native API"가 없으므로,
  이 경우만은 어떤 형태로든 원격 실행 채널(tunnel이든 다른 것이든)이
  구조적으로 필요하다.

### 3.3 결론

`abc.md`의 "tunnel 전부 제거"도, 현재 구조의 "N-워커 관리 계층(registry/
router/placement/ambiguity) 전부 유지"도 둘 다 과하다. 근거로 뒷받침되는
결론은 다음과 같다.

> k8s/docker는 `abc.md` §4가 제안한 `RuntimeController`(Start/Wait/Stop/
> Recover) 직접 호출로 전환한다. baremetal만 원격 연결이 필요하고, 이때도
> 지금의 registry/router 풀세트가 아니라 baremetal 전용의 더 좁은
> placement(레이블로 후보 몇 개 중 선택)면 충분할 가능성이 높다.

## 4. 수정된 목표 아키텍처

```text
Piper
  ├── API / UI
  ├── Pipeline / Notebook / Serving Controller
  ├── Scheduler / Queue
  ├── RuntimeController
  │    ├── Process (in-process, direct)
  │    ├── Docker  (Docker API 직접 호출 — daemon과 동일 호스트 전제)
  │    ├── Kubernetes (k8s API 직접 호출 — RBAC로 클러스터 내 접근)
  │    └── RemoteBaremetal (tunnel 기반, 원격 호스트 다중 등록 지원)
  ├── Storage Manager
  └── Repository (SQLite | PostgreSQL)
```

- Docker/k8s 워크로드는 `abc.md` §4.2(RuntimeController 추출)의 계획을
  그대로 따르되, 마지막에 tunnel을 완전히 걷어낸다.
- Docker의 "자기 바이너리 bind-mount" 방식은 이 전환의 선행 조건으로
  재검토가 필요하다 — master가 host-local Docker socket에 직접 접근하는
  전제가 성립하지 않는 배포(예: 원격 Docker daemon)가 실제로 있는지
  확인 필요.
- Baremetal은 tunnel을 유지하되, `internal/agent`의 Router/Registry를
  지금의 범용 placement 시스템 그대로 둘지, baremetal 전용으로 축소할지는
  별도 검토 대상이다 — 별도 문서 `tunnel-library.md` 참고(tunnel 자체를
  라이브러리로 분리하는 안).

## 5. 비목표 / 이 문서가 답하지 않는 것

- Docker daemon이 실제로 원격(master와 다른 호스트)인 배포가 존재하는지
  여부 — 확인 필요, 확인 전까지는 "docker도 native 전환 가능"을 확정
  짓지 않는다.
- baremetal 전용 placement의 구체적 설계(레이블 매칭, ambiguity 처리
  범위) — 별도 검토.
- 이행 순서와 마이그레이션 — `abc.md` §14의 단계별 계획을 준용하되, k8s/
  docker/baremetal을 동시에 다루지 않고 k8s→docker→baremetal 순으로
  좁혀서 검증하는 편이 리스크가 낮다(각 인프라별로 지금 사용 중인 실사용
  배포를 깨뜨리지 않는지 독립적으로 확인 가능).

## 6. 다음 단계

`abc.md` 방향과 이 수정안을 각각 main 브랜치에서 분기한 별도 브랜치에서
독립적으로 프로토타입한 뒤 비교한다. 이 문서는 그 비교의 기준점 역할만
한다 — 실제 구현 작업은 이 문서의 범위 밖이다.
