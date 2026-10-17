# Pipeline 실행 모델 — core는 단순하게, 편의는 템플릿으로

상태: 구현 완료 (2026-10-17) — core 축소, 에디터 템플릿, pip/uv 프리셋, 환경 기본값. 배경: `docs/adversarial-qa-2026-10-17.md`의 QA-3, QA-12.

## 1. 문제

노트북에서 돌던 코드가 템플릿으로 만든 파이프라인에서는 돌지 않는다
(sklearn `ModuleNotFoundError`). 원인은 **환경의 주인이 둘**이라는 데 있다.

- 노트북 서버는 이미지(docker/k8s)나 볼륨의 `.venv`(baremetal)를 환경으로 쓴다.
- 파이프라인 스텝은 에이전트가 매번 새 `.task-venv`를 만들고
  (`pkg/pipeline/worker/agent/runner.go:prepareIsolatedPython`), setuptools,
  papermill, ipykernel을 설치하고, 커널스펙을 쓴 다음 그 안에서 실행한다.
  `--system-site-packages`가 없으므로 docker/k8s에서는 **이미지에 깔린 패키지도 안 보인다**.
  `uv sync`처럼 자기 venv를 만드는 도구와도 충돌한다.

이걸 "패키지 매니저 지원"(requirements.txt 감지, pip freeze, uv 분기 등)으로
메우면 core가 계속 커지고, 그게 다시 꼬인다.

## 2. 원칙

- 직관적이고 단순하게. 보이는 기본값은 쓰되, 숨은 규칙이나 추측은 두지 않는다.
- core는 실행만 한다. 도구별 지식(papermill, pip, uv, conda)은 core에 넣지 않는다.
- 편의는 **템플릿**으로 제공한다. 템플릿은 스텝 YAML에 보이는 텍스트로 펼쳐진다.
  유연성은 템플릿이 갖고, 정합성은 core 계약이 지킨다.
- 하나의 사실은 한 곳에만 저장한다. 템플릿은 고르는 순간 파이프라인 YAML에 복사되고,
  이후 템플릿이 바뀌어도 기존 파이프라인에는 영향이 없다.

## 3. 참조

| 참조 | 취할 점 | 취하지 않을 점 |
|---|---|---|
| Argo Workflows | 엔진은 image + command + env만 다룬다. 재사용은 WorkflowTemplate으로 한다 | — |
| KFP / Elyra | 이미지가 곧 환경이다. `packages_to_install`은 보이는 command로 컴파일된다 | Elyra 부트스트래퍼의 숨은 papermill 설치 |
| SageMaker Notebook Jobs | 노트북이 쓰던 이미지를 잡에 그대로 쓴다 | 관리형 이미지 카탈로그(지금은 과함) |
| Databricks | 환경은 컴퓨트가 소유하고, 잡은 붙기만 한다 | 공유 환경이 바뀌면 기존 잡이 깨지는 구조 |
| Metaflow `@conda`/`@pypi` | — | 환경 관리를 core에 넣어 복잡도가 커진 반면교사 |
| Airflow `PapermillOperator` | — | 도구별 오퍼레이터와 플러그인이 늘어나며 생기는 버전 호환 문제 |

## 4. Core 계약 (세 런타임 공통)

스텝 하나에 대해 core가 하는 일은 이것뿐이다.

1. **환경**
   - docker/k8s: `driver.<runtime>.image`로 컨테이너를 띄우고, 이미지의 기본 python/PATH를 그대로 쓴다.
   - baremetal: 스텝 작업 디렉터리에서 호스트 환경으로 실행한다.
2. **입력 전달**: 소스(스냅샷/git)를 작업 디렉터리에 받고, 입력 아티팩트와 파라미터를 놓는다.
3. **실행**: `run.prepare`의 각 명령을 순서대로, 그다음 `run.command`를 실행한다.
4. **회수**: `outputs`를 업로드하고, 로그와 상태를 보고한다.

스텝에 주는 환경변수가 계약의 전부다.

| 변수 | 의미 | 현재 |
|---|---|---|
| `PIPER_SCRIPT_PATH` | `run.path` 진입 파일의 절대 경로 | 있음 (command 스텝) |
| `PIPER_INPUT_DIR` / `PIPER_OUTPUT_DIR` | 입력/출력 디렉터리 | 있음 |
| `PIPER_PARAM_<name>` | 파라미터 값 (문자열) | 있음 (command 스텝) |
| `PIPER_PARAMS_FILE` | 파라미터 전체를 담은 YAML 파일 (타입 보존) | **신규** |

### core에서 빠지는 것

- `prepareIsolatedPython`: venv 생성, setuptools/papermill/ipykernel 설치, `writeVenvKernelSpec`
- `NotebookExecutor`: papermill 인자 조립. 노트북 실행은 템플릿이 만든 command가 된다.
- `ExecConfig.PapermillBin`, `PythonBin`, `JupyterDataDir`, `PIPER_PAPERMILL`
- 스펙의 `run.type`, `run.notebook`. 진입 파일은 `run.path` 하나로 쓴다.
  에디터의 노드 표시(노트북 아이콘, `.ipynb` 파일 피커)는 확장자로 판단한다.
  출시 전이라 레거시 호환 경로는 두지 않는다. `examples/`와 e2e는 같이 옮긴다.

## 5. 템플릿

템플릿은 프론트에 내장된 스텝 정의 조각이다. 고르면 스텝 YAML에 펼쳐지고,
이후에는 사용자가 쓴 텍스트와 똑같이 취급된다. 백엔드에는 저장하지 않는다(1단계).

서로 독립적인 두 종류로 나눠서 조합 수가 늘어나지 않게 한다.

**실행 템플릿** (`command`, `outputs`를 채움)

| 이름 | 펼쳐지는 내용 |
|---|---|
| Notebook (papermill) | prepare: `python3 -m pip install -q papermill ipykernel`<br>command: `papermill --log-output "$PIPER_SCRIPT_PATH" "$PIPER_OUTPUT_DIR/executed.ipynb" -f "$PIPER_PARAMS_FILE"`<br>outputs: `executed-notebook: executed.ipynb` |
| Python script | command: `python3 "$PIPER_SCRIPT_PATH"` |
| Command | command: 빈 칸 |

**Prepare 프리셋** (`prepare`에 텍스트만 덧붙임)

| 이름 | 덧붙이는 명령 |
|---|---|
| pip requirements | `python3 -m pip install -r requirements.txt` |
| uv | `uv pip install --system -r pyproject.toml` (정확한 형태는 구현 시 실제로 실행해 확인) |
| baremetal venv | `python3 -m venv .venv` 생성 + 이후 명령을 `.venv/bin/` 경로로 실행하는 형태 |

에디터의 보이는 기본값도 같은 원칙을 따른다.
- docker/k8s 스텝 이미지는 소스 노트북의 이미지로 미리 채운다. 사용자가 이미 고른 값이다.
- 파일 존재 여부로 프리셋을 고르거나 추천하지 않는다.

예: Notebook 템플릿을 고른 스텝

```yaml
- name: train
  run:
    path: train_iris.ipynb
    prepare:
      - [python3, -m, pip, install, -q, papermill, ipykernel]
      - [python3, -m, pip, install, -q, -r, requirements.txt]   # pip 프리셋
    command:
      - sh
      - -c
      - papermill --log-output "$PIPER_SCRIPT_PATH" "$PIPER_OUTPUT_DIR/executed.ipynb" -f "$PIPER_PARAMS_FILE"
  params:
    n_estimators: 120
  outputs:
    - name: executed-notebook
      path: executed.ipynb
    - name: model
      path: model.pkl
```

이미지에 papermill이 이미 있으면 첫 prepare 줄은 바로 끝난다. 매번 설치를 피하고 싶으면
이미지에 넣고 그 줄을 지우면 된다. 그 선택은 사용자 몫이다.

## 6. 플러그인을 쓰지 않는 이유와 기준

papermill 실행은 명령어 한 줄로 완전히 표현되므로 템플릿으로 충분하다.
플러그인은 core에 인터페이스, 로딩, 버전 관리를 만들고, docker/k8s에서는 플러그인 코드가
컨테이너 안의 에이전트까지 배포돼야 한다.

플러그인은 **명령어로는 표현이 안 되고 서버나 UI와 맞물려야 하는 요구가 실제로 생겼을 때만**
검토한다. 실행된 노트북을 화면에 보여주는 것은 실행 쪽 문제가 아니라 아티팩트 뷰어 문제다.
기존 `Artifact.type` 뷰어 힌트에 `notebook`을 추가하는 것으로 해결한다.

## 7. 하지 않을 것

- requirements.txt, pyproject.toml, environment.yml 감지 및 자동 설치
- 템플릿 제출 시 `pip freeze` 캡처
- 노트북 `.venv`나 컨테이너를 파이프라인과 공유하거나 스냅샷하는 것
- 런타임 이미지 카탈로그 (Elyra식). 필요해지면 별도로 검토한다.

## 8. baremetal 격리 — 결정: 안 A

baremetal의 venv는 템플릿의 `prepare`가 만들고, core는 venv를 모른다.
Notebook 템플릿의 baremetal 변형은 venv 안에 전용 커널(`piper-step`)을 등록하고
`papermill -k piper-step`으로 실행한다. 호스트의 사용자 `python3` 커널스펙이 다른
python을 가리키면(실제로 이 개발 머신은 존재하지 않는 `/usr/local/bin/python3`을 가리킴)
그 커널이 대신 선택되기 때문이다. 예전 core가 커널스펙을 직접 쓰던 이유가 이것이었고,
이제는 그 처리가 보이는 템플릿 텍스트로 옮겨졌다.

## 9. 검증: 골든패스 e2e

계약 자체를 테스트로 고정한다. 세 런타임 각각에서 아래를 확인한다.

1. 노트북 워크스페이스에 `import sklearn` 노트북과 `requirements.txt`를 둔다.
2. UI 에디터에서 Notebook 템플릿과 pip 프리셋으로 템플릿을 만든다.
3. Run 결과가 `success`이고, `model.pkl`과 `executed.ipynb` 아티팩트가 있다.
4. New Version으로 다시 열었을 때 Design 탭이 잠기지 않는다.

이때 실사용 조건을 쓴다: 비루트 `jupyter/*` 이미지, `workload_token` 설정.

## 10. 순서

1. ~~Critical: 스냅샷 fetch 토큰(QA-1), task.json 권한(QA-2)~~ 완료 (2026-10-17)
2. ~~core 실행 모델 축소 + `PIPER_PARAMS_FILE` 추가~~ 완료
3. ~~에디터: 팔레트 템플릿 펼침, Prepare 칸, pip·uv 프리셋(파일을 deps에 추가), 이미지·네임스페이스 기본값~~ 완료
4. ~~골든패스 e2e~~ 완료 — `make test-golden-e2e`(`golden_path_e2e_test.go`), CI `golden-e2e`·`k8s-e2e` 잡
5. 이후 별도: YAML 처리 단일화(QA-5, QA-6), 중복과 기본값 정리(QA-7, QA-9, QA-11)
