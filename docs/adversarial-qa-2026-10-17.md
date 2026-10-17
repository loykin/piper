# Adversarial QA — 2026-10-17

**대상 커밋**: `79ac8d0` (HEAD). 작업 트리 소스로 새로 빌드 — `make ui` +
`go build -tags builtinassets`, docker 에이전트는 `bin/piper-arm64` 새로 빌드,
k8s 이미지는 `piper/piper:qa-1017`(같은 바이너리)을 kind 노드에 적재.

**시나리오**: 사용자가 실제로 밟는 핵심 경로 하나 —
*Jupyter 노트북에서 ML 모델 작성 → 그 노트북 볼륨으로 템플릿 생성 →
Run / Deploy(스케줄)로 파이프라인 실행* — 를 `runtime.type`별 독립 인스턴스
3개에서 렌더링된 UI 폼으로 끝까지 진행하고, 서버 로그/데몬/클러스터를 교차 확인했다.

| 인스턴스 | 구성 | 결과 |
|---|---|---|
| baremetal | 로컬 `piper server`, `localhost:18081` | 템플릿 v1 **실패**(sklearn 없음) → YAML 탭에서 `prepare` 추가한 v2를 Deploy(cron) → **success** |
| docker | 로컬 `piper server`, `docker.localhost:18082` | 노트북 기본값 **실패**(이미지 미pull) → 수동 pull 후 성공. 템플릿(YAML 탭 수정) Run → **success** |
| k8s | `deploy/k8s` 매니페스트를 `piper-qa` 네임스페이스로 클러스터 내부 배포, port-forward `k8s.localhost:18083` | 노트북 기본값 **실패**(네임스페이스). 템플릿 Run **실패 2종**(권한, 401). 둘 다 우회해야 **success** |

ML 노트북 자체(papermill `parameters` 셀, iris RandomForest, `model.pkl`/
`metrics.json` 출력)는 세 런타임의 노트북 서버에서 모두 accuracy 0.978로 정상 실행됐다.
JupyterLab UI/커널 WebSocket도 세 런타임 모두 Piper 프록시를 정상 통과했다.

## 백로그 (심각도순)

| ID | 심각도 | 런타임 | 요약 |
|---|---|---|---|
| **QA-1** 스냅샷 fetch에 스토리지 토큰 누락 | Critical — **수정, k8s 라이브 확인** | k8s (docker도 `workload_token` 설정 시) | `internal/srcfetch/s3.go`가 `storage.Open(f.cfg.StorageURL, "")`로 토큰을 버린다. 공식 `deploy/k8s`는 항상 `workload_token`을 설정하므로 **노트북 볼륨 기반 템플릿 실행이 전부 `httpstore list: status 401`로 실패**. |
| **QA-2** 비루트 이미지에서 task.json 읽기 불가 | Critical — **수정, k8s 라이브 확인** | k8s | 태스크 Secret을 `defaultMode: 0400`(root 소유)으로 마운트하고 파드에 `fsGroup`이 없음. `USER`가 root가 아닌 이미지(jupyter/*, 대부분의 ML 이미지)는 `open /piper-task/task.json: permission denied`로 즉시 실패. |
| **QA-3** 노트북 → 파이프라인 의존성 단절 | High — **해결(설계 변경), 3개 런타임 라이브 확인** | 전체 | 노트북 서버에서 설치한 패키지가 파이프라인 스텝에 전혀 이어지지 않는다(스텝마다 새 `.task-venv` + papermill/ipykernel만 설치). 그런데 Design 에디터에 `prepare`(의존성) 입력이 없어 **UI만으로 만든 템플릿은 서드파티 import가 있는 노트북이면 반드시 실패**. YAML 탭에서 손으로 `prepare`를 적어야만 동작. |
| **QA-4** baremetal Jupyter가 무인증 + `allow_origin=*` | High (보안) — **수정, 라이브 확인** | baremetal | `notebookdriver/process/driver.go:93,101`이 토큰을 `""`로 하드코딩. `127.0.0.1:<port>`에 Piper 인증 없이 직접 접근 가능(`/api/kernels` 200 확인)하고, `allow_origin=*`라 사용자의 브라우저에서 열린 임의 웹페이지가 커널에 접근 가능한 구조. docker/k8s 드라이버는 토큰을 쓴다(불일치). `pkg/notebook/execution/jupyter/client.go:112` 주석("baremetal/docker는 항상 빈 토큰")도 사실과 다름. |
| **QA-5** Run/Schedule DAG가 정규식 YAML 파싱 | High — **수정, 3개 런타임 라이브 확인** | 전체 | `RunDAG.tsx`의 `parseStepsFromYaml`이 `- name:`으로 시작하는 모든 항목을 스텝으로 취급. outputs/inputs/env 항목이 **영원히 ⌛인 가짜 스텝 노드**로 표시되고 연결선도 없다(성공한 run에서도). Run 상세, Schedule 상세 공통. |
| **QA-6** UI가 만든 템플릿을 UI가 다시 못 연다 | High — **수정, 3개 런타임 라이브 확인** | 전체 | 서버가 저장 시 `Pipeline` 구조체로 재직렬화하면서 `omitempty` 없는 필드(`Run.Source/Repo/Branch/Path/Dir/URL/Command`, `Artifact.From`, k8s의 빈 `pod_template`)를 빈 값으로 채운다. "New Version"으로 열면 즉시 `Design view cannot preserve "spec.steps[0].outputs[0].from"` → Design 탭 사용 불가. |
| **QA-7** k8s 노트북 폼 기본 네임스페이스가 허용 목록 밖 | High — **수정, k8s 라이브 확인** | k8s | 폼 기본값 `notebooks`, 공식 배포의 `runtime.namespaces`는 `[piper]`. 기본값 제출 → `namespace "notebooks" is not allowed`. 400으로 거절하지 않고 `failed` 레코드를 만든다. |
| **QA-8** docker 드라이버가 이미지를 pull하지 않음 | High — **수정, docker 라이브 확인** | docker | 노트북/파이프라인/서빙 docker 드라이버 어디에도 `ImagePull`이 없다. 노트북 폼 기본 이미지 `jupyter/minimal-notebook:latest`가 로컬에 없으면 `No such image`로 실패 — **신규 설치에서 기본 폼은 항상 실패**. |
| **QA-9** 노트북 실패 사유가 UI/API 어디에도 없음 | Medium — **수정, 3개 런타임 라이브 확인** | 전체 | 노트북 레코드에 에러 필드가 없다. QA-7/8 모두 UI에는 사유 없이 `failed`만 표시, 원인은 서버 로그에만. (레코드 `image: ""` vs `yaml`의 image도 불일치) |
| **QA-10** Design 에디터가 모든 파라미터를 문자열로 만든다 | **해결** (params는 YAML 스칼라로 기록; `-f` 파라미터 파일 도입 후엔 필수였음) | 전체 | `n_estimators: 120` 입력 → YAML `"120"`. **실행 피해는 없음**: papermill `-p`가 `"120"`→`120`으로 타입을 추론하고(`'true'`는 문자열로 남음), command 스텝은 원래 `PIPER_PARAM_*` 문자열 env. 남는 문제는 YAML에 숫자로 쓰면 Design이 "cannot preserve"로 잠기는 마찰뿐. |
| **QA-11** 제출 다이얼로그가 소스 볼륨을 다시 고르게 함 | Medium — **수정, 라이브 확인** | 전체 | 에디터는 "소스 워크스페이스는 하나, 먼저 고정"이라 해놓고 Submit 다이얼로그에서 다른 볼륨을 고를 수 있다. 편집 중 파일 피커가 본 볼륨과 스냅샷 볼륨이 어긋날 수 있음. |
| **QA-12** 스텝마다 venv 생성 + papermill 재설치 | Low (성능) — **해결: 컨테이너는 venv 없음(docker 35s→16s)** | 전체 | 1-셀 노트북 스텝이 baremetal 54초, docker 35초. 캐시/재사용 없음. |
| QA-13 Design 노드는 더블클릭으로만 편집 | Low | — | 단일 클릭은 선택만, 편집기가 안 열림. 힌트 없음. |
| QA-14 Deploy 폼에 파라미터 오버라이드 없음 | Low | — | 스케줄마다 다른 하이퍼파라미터 불가(YAML 복제 필요). |
| QA-15 docker 이미지 필수인데 폼에 표시 없음 | Low | docker | 비워 두면 서버가 `driver.docker.image is required`로 거절(메시지는 명확). |
| QA-16 아티팩트 Preview 모달 줄바꿈 없음 | Low | — | 짧은 JSON도 오른쪽이 잘림. |
| — 미존재 라우트 `/templates` | Info | — | 404 대신 Schedules로 조용히 이동. |

**정정/비버그**: k8s 태스크 Secret과 Job이 run 종료 후 남는 것은 누수가 아님 —
Secret은 Job ownerReference, Job은 `ttlSecondsAfterFinished: 86400`. 서버 종료 후
baremetal Jupyter 프로세스가 살아 있는 것도 pid/meta 파일 기반 재접속 설계로 판단.

## 수정 내역 (2026-10-17, 같은 세션)

- **QA-1**: `srcfetch.Config`에 `StorageToken` 추가. `internal/srcfetch/s3.go`의
  두 `storage.Open` 호출에 토큰 전달. 에이전트(`runner.go`)와 서버(`piper.go:sourceConfig`)
  양쪽에서 채움. 회귀 테스트 `TestS3Fetcher_snapshotSendsStorageToken`는 수정 전 코드에서
  라이브와 같은 `httpstore list: status 401`로 실패한다.
- **QA-2**: 태스크 Secret `DefaultMode` 0400 → 0444 (`k8slauncher/launcher.go`).
  Secret은 해당 파드의 스텝 컨테이너에만 마운트되므로 노출 범위는 그대로.
  `launcher_test.go`에 단언 추가(수정 전 `DefaultMode:*256`으로 실패).
- **라이브 검증**: 수정 바이너리로 만든 이미지 `piper/piper:qa-1017b`를 `deploy/k8s` 매니페스트
  그대로(`workload_token` 설정) `piper-qa`에 배포. UI에서 노트북 → 템플릿
  (`jupyter/minimal-notebook`, 비루트) → Run: 파드 로그 `s3 source snapshot ready`,
  `accuracy 0.9777…`, run `success`, 아티팩트 `metrics.json`/`model.pkl` 업로드 확인.
- `go test ./...` 전체 통과.
- 나머지(QA-3 이후)는 `docs/pipeline-execution-templates.md` 설계에 따라 진행.

## 실행 모델 축소 (2026-10-17, 같은 세션)

설계: `docs/pipeline-execution-templates.md`. 세 런타임은 환경 제공만 다르고 스텝 실행은
동일하다(fetch → prepare → command → outputs).

- 에이전트의 venv/papermill/setuptools/커널스펙 로직, `NotebookExecutor`, 스펙의
  `run.type`/`run.notebook` 제거. `PIPER_PARAMS_FILE`(타입 보존 YAML) 추가, prepare와 command가
  같은 환경변수를 받는다.
- 에디터: 팔레트 Notebook/Python Task = 런타임별 템플릿(baremetal은 venv + 전용 커널
  `piper-step`, 컨테이너는 이미지 python)이 Prepare/Command 칸에 보이게 펼쳐짐. Task Type
  선택 제거(종류는 `path` 확장자로 복원). params는 YAML 스칼라로 기록(QA-10).
- 발견 후 수정: 옛 형식으로 저장된 템플릿 Run/Deploy가 불투명한 500 → 이유를 담은 400.
- 발견 후 수정: 이 개발 머신의 사용자 `python3` 커널스펙이 존재하지 않는 python을 가리켜
  papermill이 실패 → 예전 core가 커널스펙 덮어쓰기로 숨기던 문제. baremetal 템플릿이 venv 안에
  전용 커널을 등록하고 `-k`로 지정.
- 발견 후 수정: 템플릿에서 `--log-output`이 빠져 셀 출력이 로그에 안 보임 → 템플릿에 추가.
- 라이브(UI로 생성·제출·실행): baremetal success(`piper-step` 커널, executed.ipynb 아티팩트),
  docker success 16s, k8s(공식 매니페스트, workload_token, 비루트 jupyter 이미지) success,
  폼에서 입력한 `n_estimators=120`이 노트북에 `int`로 주입됨.
- 테스트: `go test ./...` 통과(단 `TestNoRawErrorsIn500Responses`는 `notebook.go` 삭제가
  커밋 전이라 `git ls-files`에 남아 실패 — 커밋하면 해소), example_e2e 통과(노트북 템플릿
  실제 실행), vitest 24, Playwright 27/27, tsc, eslint 에러 0.

## 런타임별 차이 정리 (2026-10-17, QA-4/7/8/9)

원칙: 세 런타임은 실행 환경을 만드는 것만 다르고 나머지는 같다. 기본값은 소유자(서버)에서,
같은 값은 한 곳에만.

- **QA-4**: baremetal 드라이버가 docker/k8s와 같이 시작마다 생성되는 토큰으로 Jupyter를
  띄운다(`notebookdriver/process/driver.go`의 `launchCommand`, 기존 `""` 하드코딩 제거). Piper
  프록시가 토큰을 주입하므로 사용자 경로는 그대로. `jupyter/client.go`·`runtime.go`의 "빈 토큰"
  주석 정정. 테스트 `TestProcessLaunchCommandRequiresToken`(venv/conda)은 수정 전 코드에서
  `--ServerApp.token=`으로 실패.
- **QA-8**: `internal/docker.EnsureImage`(없으면 pull, kubelet IfNotPresent와 같은 동작)를
  노트북·파이프라인·서빙 docker 드라이버가 `ContainerCreate` 직전에 호출. `dockertest.Images`
  페이크는 데몬처럼 없는 이미지로 create하면 `No such image`를 낸다. 세 드라이버의
  `*PullsMissingImage` 테스트는 수정 전 코드에서 라이브와 같은 `No such image`로 실패.
  한계: 서빙 `Deploy`는 원래 동기라 pull 동안 HTTP 요청이 기다린다(노트북·파이프라인은 비동기).
- **QA-7**: 서버가 `GET /api/system/settings`의 `runtime.namespaces`(k8s만)를 내려주고, k8s
  노트북 폼은 네임스페이스를 그 목록의 Select로 바꿔 첫 항목을 기본값으로 쓴다(프론트의
  `'notebooks'` 하드코딩 제거). 허용 여부 판단은 k8s 드라이버 한 곳(`namespace()`)에 두고
  `notebook.Driver.ValidateSpec`으로 노출, `Manager.Create/CreateWithVolume/Restart`가 레코드·볼륨을
  만들기 전에 호출 → 400, 메시지에 허용 목록 포함.
- **QA-9**: 노트북 레코드에 `reason` 컬럼(마이그레이션 00049, sqlite/postgres). 상태 쓰기는
  항상 사유와 함께(`Repository.SetStatus(…, status, reason)`, 드라이버 보고는
  `notebook.StatusReport{Status, Reason, …}`) → 재시작하면 옛 실패 사유가 남지 않는다. 사유 출처:
  Manager의 provision/start 오류, 로컬 드라이버의 비동기 start 오류, k8s 파드의
  `ErrImagePull/ImagePullBackOff/CrashLoopBackOff` 사유+kubelet 메시지, 프로세스/컨테이너가 스스로
  실패 종료한 경우 "see its logs". history에도 보존. UI는 노트북·히스토리 상세 패널에 표시만 한다.
  `image` 컬럼은 어떤 드라이버도 채우지 않아 항상 `""`였으므로 삭제하고, 응답의 `image`는 저장된
  YAML에서 읽는다(`notebook.ManifestImage`).
- **라이브(같은 날, `bin/piper` + `piper/piper:qa-1017c`)**: baremetal(`localhost:18081`),
  docker(`docker.localhost:18082`, 실행 전 `jupyter/minimal-notebook:latest`를 로컬에서 삭제),
  k8s(`deploy/k8s` 매니페스트를 `piper-qa`로, `runtime.namespaces: [piper-qa]`, `workload_token` 설정)
  각각에서 렌더링된 폼에 이름만 넣고 기본값으로 Launch → 세 개 모두 `running`.
  - QA-8: docker 서버 로그 `docker: pulling image image=jupyter/minimal-notebook:latest` →
    `notebook docker container started`.
  - QA-7: k8s 폼의 Namespace 기본값이 `piper-qa`. YAML 탭에서 `notebooks`로 바꿔 제출 →
    `400 notebook: k8sdriver: namespace "notebooks" is not allowed (runtime.namespaces: piper-qa)`,
    레코드·PVC 생성 없음(서버 로그의 provision은 정상 노트북 2건뿐).
  - QA-4: `curl 127.0.0.1:18900/…/proxy/api/kernels` 직접 접근 200 → **403**(docker도 403).
    로그인한 브라우저의 프록시 경로는 세 런타임 모두 200, baremetal JupyterLab에서 셀 실행(`42`) 확인.
  - QA-9: 폼으로 없는 이미지/경로를 넣어 실패시킴 → 레코드·API·상세 패널에 사유 표시. docker
    `pull image piper-qa/does-not-exist:nope: … pull access denied…`, k8s
    `ErrImagePull: failed to pull and unpack image "docker.io/piper-qa/does-not-exist:nope": …`,
    baremetal `create venv "/nonexistent/qa-venv": … Read-only file system`. 각 서버 로그의
    `localdriver: start failed` / 파드 `ImagePullBackOff`와 일치. 실패 노트북 삭제 후 history에
    사유·image 유지.
- 테스트: `go test ./...` 통과(기존과 같은 `TestNoRawErrorsIn500Responses` 1건만 —
  `notebook.go` 삭제 미커밋), 프론트 tsc·vitest 24·eslint 에러 0.

## 상세

### QA-1 스냅샷 fetch 토큰 누락 (Critical)

증거 (k8s, 공식 매니페스트 그대로):

```
step.failed error:"fetch failed: s3 source: download snapshot snapshots/ea98db64-.../: httpstore list: status 401"
```

`internal/k8sruntime/pipeline/runtime.go`의 `taskStorageForK8sRuntime`은 토큰을 올바르게
매핑해 `ExecSpec.StorageToken`에 넣는다. 그러나 에이전트 측
`pkg/pipeline/worker/agent/runner.go`가 만드는 `srcfetch.Config{GitToken, GitUser, StorageURL}`
에는 토큰 필드 자체가 없고, `internal/srcfetch/s3.go`는 두 곳 모두
`storage.Open(f.cfg.StorageURL, "")`. 아티팩트 업/다운로드 경로는 토큰을 쓰기 때문에
command-only 파이프라인은 통과하고, **소스 스냅샷을 쓰는 경로만** 깨진다.

검증: `workload_token`만 비우고 서버 재시작 → 같은 템플릿 v2 Run `success`.
docker 인스턴스는 `workload_token` 미설정이라 통과했을 뿐 같은 결함.

재발 방지: `srcfetch.Config`에 `StorageToken`을 추가하고 `storage.Open(url, "")`
리터럴을 금지하는 lint(또는 grep CI). e2e는 `workload_token`이 설정된 구성으로 돌려야 한다.

### QA-2 비루트 이미지 task.json 권한 (Critical)

```
ERROR agent exec: decode task err="read task file: open /piper-task/task.json: permission denied"
```

`k8slauncher/launcher.go:533` `DefaultMode: ptrInt32(0400)`, 파드
`securityContext: {}`. `jupyter/minimal-notebook`(UID 1000) → 실패,
`python:3.12-slim`(root) → task.json 통과(이후 QA-1로 실패). 이전 QA들이 root로 도는
alpine/busybox류만 써서 드러나지 않았다. 노트북 스텝에 가장 자연스러운 이미지가
jupyter 계열이라 실제 사용자는 거의 확실히 밟는다.

수정 방향: 파드에 `fsGroup` 지정 + Secret `defaultMode: 0440`(또는 컨테이너
`runAsUser`와 무관하게 읽히도록). e2e에 비루트 이미지 케이스 추가.

### QA-3 노트북 ↔ 파이프라인 의존성 단절 (High)

baremetal v1 run 로그(요약): `Collecting papermill … Successfully installed … papermill-2.7.0`
→ `ModuleNotFoundError: No module named 'sklearn'`.

- 노트북 서버는 볼륨의 `.venv`(baremetal) / 이미지(docker·k8s)에 설치한 패키지를 쓴다.
- 파이프라인 스텝은 `runner.go:prepareIsolatedPython`이 매번 새 `.task-venv`를 만들고
  papermill+ipykernel만 설치한다.
- Design 에디터 드래프트 모델(`editor.ts`)에 `prepare`가 없다. 볼륨의
  `requirements.txt`를 자동으로 쓰지도 않는다.

즉 "노트북에서 돌던 코드가 템플릿에서 그대로 돈다"는 제품의 핵심 약속이 UI 경로에서
성립하지 않는다. 최소한 Task Editor에 Prepare/Requirements 입력을 추가하거나,
스냅샷에 `requirements.txt`가 있으면 자동 설치하는 규칙 중 하나가 필요하다.

### QA-4 baremetal Jupyter 무인증 (High, 보안)

```
$ lsof -iTCP:18900 -sTCP:LISTEN  →  127.0.0.1:18900, [::1]:18900
$ curl http://127.0.0.1:18900/projects/default/notebooks/ml-nb/proxy/api/kernels  →  200
docker 노트북 동일 요청                                                       →  403
```

로컬 바인딩이라 원격 노출은 아니지만, 같은 호스트의 다른 사용자/프로세스는 Piper
인증·권한 체크를 우회해 커널(=임의 코드 실행)에 접근한다. `allow_origin=*` +
무토큰이면 사용자의 브라우저에 열린 외부 페이지도 `GET /api/kernels`(CORS 허용)와
커널 WebSocket에 접근할 수 있다. docker/k8s와 같이 토큰을 생성해 프록시가 주입하도록
통일하는 것이 맞다.

### QA-5 RunDAG 정규식 파싱 (High)

`frontend/src/features/runs/components/RunDAG.tsx:26`:
`yaml.split(/\n(?=\s*-\s+name:)/)` → outputs의 `- name: model`, `- name: metrics`,
env의 `- name: PYTHONPATH`까지 모두 스텝 노드가 된다. 증거: 성공한 run
`dd713f57…`의 DAG에 `task-1`(✅) 아래 `model`, `metrics`(⌛) 노드, 연결선 없음.
`yaml` 패키지는 이미 의존성(`editor.ts`, `ScheduleForm.tsx`에서 사용)이므로
`parse()` 후 `spec.steps`만 읽으면 된다.

### QA-6 저장 YAML 재직렬화 → 왕복 붕괴 (High)

`pkg/template/handler.go:166`, `:504` 모두 `yaml.Marshal(pl)`. `pkg/pipeline/spec.go:211-229`
태그에 `omitempty` 누락. 저장된 YAML 예:

```yaml
run:
  type: notebook
  source: ""
  repo: ""
  branch: ""
  path: ""
  notebook: train_iris.ipynb
  dir: ""
  url: ""
  command: []
outputs:
  - name: model
    path: model.pkl
    from: ""
```

k8s는 여기에 `pod_template: {metadata: {}, spec: {containers: null}}`까지 붙는다.
프론트의 `findPipelineDraftYamlDifference`가 이 빈 값을 "보존 불가"로 판정해
UI가 만든 템플릿의 New Version이 Design에서 잠긴다. 구조체 재직렬화는 또한 오타 키를
조용히 버리고 주석을 지운다.

### QA-7 / QA-8 / QA-9 런타임별 기본값 실패

- k8s: `{"level":"ERROR","msg":"notebook: provision volume failed","err":"k8sdriver: namespace \"notebooks\" is not allowed"}`
- docker: `level=ERROR msg="localdriver: start failed" err="create notebook container: Error response from daemon: No such image: jupyter/minimal-notebook:latest"`
- 두 경우 모두 UI 목록은 `failed`만, `GET /api/projects/default/notebooks` 응답에도 사유 필드 없음.

플레이북 §1("선택 필드는 기본값 그대로") 그대로 제출했을 때 docker·k8s 모두 첫 노트북이
실패한다. 폼 기본 네임스페이스는 서버의 `runtime.namespaces[0]`을 따라야 하고, 허용 목록
밖이면 생성 전에 400이어야 한다. docker 드라이버는 이미지가 없으면 pull해야 한다
(참고: Docker Hub의 `jupyter/*`는 2023년 이후 갱신 중단, 현재 `quay.io/jupyter/*`).

## "과하게 만들어 꼬인" 구조 — 공통 원인

이번 회차 버그 다수(QA-5, 6, 10, 그리고 QA-3의 우회 불편)는 같은 뿌리에서 나온다:
**파이프라인 YAML을 다루는 방식이 네 갈래로 따로 논다.**

| 위치 | 방식 |
|---|---|
| `editor.ts` `buildPipelineDraftYaml` | 문자열 이어붙이기로 YAML **생성**, 드래프트 모델에 없는 필드는 소실 |
| `editor.ts` `stripMetadataVersion` | 줄 단위 들여쓰기 파싱 |
| `RunDAG.tsx` `parseStepsFromYaml` | 정규식 파싱 |
| `pkg/template/handler.go` | Go 구조체로 재직렬화(빈 필드 추가, 미지 키 삭제) |

그리고 이 불일치를 메우기 위해 `findPipelineDraftYamlDifference`(파싱 후 재생성해
깊은 비교)라는 감시 장치가 추가됐는데, 이 장치가 이번엔 서버가 넣은 빈 값과 숫자
파라미터에 걸려 정상 템플릿을 잠근다. 장치를 더 정교하게 하는 대신 원인을 줄이는 게 맞다:

1. 서버는 사용자가 보낸 YAML 원문을 저장하고, `metadata.version`/`snapshot_prefix`만
   `yaml.Node` 수준에서 주입(또는 최소한 `omitempty` 정리).
2. 프론트는 이미 있는 `yaml` 패키지의 `parseDocument` + `setIn`으로 Design 변경을
   원문 문서에 반영 → 미지 필드·주석·숫자 타입이 보존되고 "Design cannot preserve" 경로가
   거의 필요 없어진다. RunDAG/stripMetadataVersion도 같은 파서로.

## 재발 방지 제안

- e2e/QA 픽스처를 공식 `deploy/k8s` 매니페스트(= `workload_token` 설정) + 비루트 이미지
  (`jupyter/*`) 조합으로 최소 1회 돌린다. QA-1, QA-2는 "root 이미지 + 토큰 없음"
  픽스처에서만 숨는다.
- 노트북 템플릿 e2e에 서드파티 import(예: sklearn) 1줄을 넣는다 → QA-3이 바로 드러남.
- `storage.Open(<url>, "")` 리터럴을 금지하는 grep/lint.
- 프론트 lint: `.split(/\n(?=` 등 YAML 정규식 파싱 금지, YAML은 `yaml` 패키지로만.

## YAML 단일화 + 합친 상태 재검증 (2026-10-17)

[런타임별 차이 정리] 세션의 worktree 변경(QA-4/7/8/9)을 이 체크아웃에 합친 뒤 진행.

- **QA-6**: 템플릿 저장 시 구조체 재직렬화를 없앰. 제출한 원문에 `metadata.version`만 YAML
  노드 수준에서 추가(`pipeline.EditYAML`, `pkg/pipeline/yamledit.go`). 실행·배포 시
  스냅샷 경로를 넣는 `rewriteLocalSources`도 같은 방식. 남은 구조체 직렬화 경로
  (`pipeline.Marshal`)를 위해 `Run`/`Artifact`의 빈 필드에 `omitempty`. 테스트
  `TestSubmitStoresSubmittedYAMLWithVersion`(저장 텍스트 = 제출 원문 + version 한 줄).
- **QA-5**: Run/Schedule DAG가 `yaml` 파서로 `spec.steps`만 읽음(`features/runs/dag.ts`).
  `stripMetadataVersion`도 줄 단위 → `parseDocument().deleteIn`(주석·따옴표 유지).
- 에디터 YAML **생성**(문자열 + `JSON.stringify` 스칼라)은 유지: 값이 모두 JSON 문자열이라
  유효한 YAML이고, 왕복 무손실을 테스트로 고정했으며, 바꾸면 노트북·서빙 에디터가 공유하는
  헬퍼까지 동작 변화 없이 바뀐다. "Design cannot preserve" 검사기도 유지 — 이제 Design이
  다루지 않는 필드를 YAML 탭에 직접 쓴 경우에만 걸리고, UI가 만든 템플릿의 New Version에서는
  걸리지 않음(vitest `New Version of a template the editor made`).
- **발견·수정 — 노트북 프록시 토큰 생략 휴리스틱**: 프록시가 `username-*` 쿠키가 하나라도
  있으면 Jupyter 토큰 주입을 건너뛰었다. 쿠키 이름에는 호스트·포트가 들어가는데 검사는 접두사만
  봐서, 같은 호스트의 다른 Jupyter 쿠키가 있으면 주입이 생략되고 Jupyter 로그인 페이지가 뜬다
  (QA-4로 baremetal에 토큰이 생기면서 드러남). 항상 주입하도록 단순화(`InjectQueryParam`).
  라이브: 옛 쿠키가 남은 `localhost`에서 403 → 200.
- **발견·수정 — 로컬 드라이버 Recover 미호출(기존 결함)**: 노트북·서빙 로컬 드라이버의
  `Recover`가 어디서도 호출되지 않아, 서버 재시작 후 죽은 baremetal/docker 노트북이 계속
  `running`으로 남고 살아 있는 것은 종료 감지가 끊겼다(k8s는 `Observe`로 맞춰짐 — 런타임별 차이).
  `composeNotebookRuntime`/`composeServingRuntime`에서 생성 직후 호출. 테스트
  `TestNotebookLocalRuntimeRecoversOnStartup`(수정 전 `running`으로 실패). 라이브: 재시작 직후
  `stopped`로 정정.
- **라이브(새 데이터, 기본값 폼)**: baremetal(`:18091`), docker(`docker.localhost:18092`, 로컬
  노트북 이미지 삭제 후 자동 pull), k8s(공식 매니페스트, `workload_token`, 기본 네임스페이스
  `piper-qa`, 기본 10Gi) 각각: 노트북 기본값 Launch → `running` → 프록시로 노트북 작성 →
  팔레트 Notebook 템플릿 + Prepare에 sklearn 한 줄 + 폼 파라미터 `n_estimators=120` →
  Run `success`, 로그에 `accuracy 0.977… n_estimators 120 int`, DAG 노드 1개(가짜 출력 노드
  없음), New Version이 Design에서 경고 없이 열림.
- 테스트: `go test ./...`(커밋 전 `notebook.go` 인덱스 1건 제외 통과), example_e2e, Playwright
  27/27, vitest 27, tsc, eslint 에러 0.

## 남은 항목 마무리 (2026-10-17)

- **QA-11**: 제출 다이얼로그는 에디터에서 고정한 소스 볼륨을 읽기 전용으로 보여주고 그 볼륨으로
  제출한다(선택 컨트롤 제거).
- **Prepare 프리셋**: Notebook/Python Task의 Prepare 칸에 `pip`/`uv` 버튼. 템플릿과 같은 환경
  규칙(baremetal은 `.venv`, 컨테이너는 이미지 python)으로 보이는 줄을 덧붙이고, 설치할 파일
  (`requirements.txt`/`pyproject.toml`)을 Source Dependencies에 추가한다 — 스냅샷에는 진입 파일과
  deps만 들어가므로, 파일을 빼면 줄이 실행 시 실패한다(라이브 중 발견해 같이 처리). uv 줄은
  `pip install uv` 후 `python -m uv pip install`(컨테이너 `--system`, baremetal
  `--python .venv/bin/python`) — 호스트 venv와 `jupyter/minimal-notebook` 컨테이너에서 실행 확인.
- **환경 기본값**: 에디터가 한 번, 보이게 채운다 — `placement.runtime`(설치가 소유한 런타임),
  docker/k8s 이미지(소스 노트북의 이미지), k8s 네임스페이스(서버 `runtime.namespaces[0]`).
  이미 값이 있으면 건드리지 않는다(`environmentDefaults`, 기존 runtime 자동 채움과 한 effect로 합침).
- **라이브**: baremetal — pip 버튼만으로 success, 스냅샷에 `requirements.txt` 포함, 제출
  다이얼로그는 "Snapshot from ml-nb · …"만 표시. docker — Container Image 자동 채움, uv 버튼만으로
  success. k8s(공식 매니페스트) — 이미지·네임스페이스 입력 없이 자동 채움, pip 버튼만으로 success.
- 테스트: vitest 31, tsc, eslint 에러 0, Playwright 27/27(프리셋의 deps 추가 수정 직전 빌드), `go test ./...`
  (커밋 전 인덱스 1건 제외).
- **알려진 한계(수정하지 않음)**: docker 노트북 복구는 데몬에 남은 컨테이너를 기준으로 한다.
  서버가 꺼진 동안 컨테이너를 `docker rm`으로 지우면 레코드는 `running`으로 남는다. DB 전체를
  훑는 정리(sweep)는 과잉 방어 원칙에 따라 넣지 않았다. Stop/Delete로 정리 가능.

## 골든패스 e2e 자동화 (2026-10-17)

QA 플레이북이 요구하는 "런타임별, 실사용 구성" 검증 중 반복 가능한 부분을 테스트로 고정했다.

- `golden_path_e2e_test.go`(`-tags golden_e2e`, `make test-golden-e2e`): 런타임마다
  관리자 부트스트랩·로그인 → 노트북 실행 → 프록시로 노트북·`requirements.txt` 작성 → 템플릿 제출
  (저장 YAML = 제출 원문 + `version`) → Run `success` → 실행된 노트북 아티팩트에
  `n_estimators 120 int`. 세 런타임이 같은 `runGoldenPath`를 쓰고 서버를 띄우는 방법만 다르다:
  baremetal·docker는 실제 `piper server` 바이너리(인증, 내장 저장소, docker는 `workload_token`),
  k8s는 `deploy/k8s` 그대로(네임스페이스·이미지만 바꿈).
- 제출 YAML은 `testdata/golden/notebook-template.<runtime>.yaml`이고,
  `frontend/test/golden-templates.test.ts`가 에디터 출력과 같음을 확인한다 — e2e는 항상 UI가
  실제로 내보내는 YAML을 실행한다.
- 결과: baremetal 85–90s, docker 19–52s, k8s 46s 통과. 변이 확인: QA-1(스냅샷 토큰)을 되살리면
  docker 골든패스가 라이브와 같은 `httpstore list: status 401`로 실패.
- CI: 새 `golden-e2e` 잡(baremetal·docker), 기존 `k8s-e2e` 잡에 k8s 골든패스 단계 추가.
- 발견(문서 수정): docker 런타임의 `workload_url` 예시가 `host.docker.internal`뿐이었는데 리눅스
  Docker 호스트에서는 풀리지 않는다. `config/piper.yaml`에 브리지 게이트웨이 안내 추가, 테스트는
  `PIPER_GOLDEN_DOCKER_HOST`로 바꿀 수 있다.
- 테스트 자체 버그 1건: k8s 시크릿의 `secret_encryption_key`를 hex로 만들어 서버가 기동 실패 —
  `deploy/k8s/server-secret.example.yaml`과 같은 base64 32바이트로 수정.
