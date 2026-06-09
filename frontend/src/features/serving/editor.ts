// serving feature — YAML builder utilities
// Extracted from ServingPage

export interface RuntimeTemplate {
  label: string
  description: string
  runtimeMode: string
  image: string
  imageOptions?: string[]
  command: string
  port: string
  healthPath: string
}

export const RUNTIME_TEMPLATES: Record<string, RuntimeTemplate> = {
  custom: {
    label: 'Custom',
    description: 'Custom Python script',
    runtimeMode: 'local',
    image: '',
    command: 'python\nserve.py',
    port: '8000',
    healthPath: '/',
  },
  triton: {
    label: 'Triton',
    description: 'NVIDIA Triton Inference Server (local)',
    runtimeMode: 'local',
    image: '',
    command: 'tritonserver\n--model-repository=$PIPER_MODEL_DIR\n--http-port=8000',
    port: '8000',
    healthPath: '/v2/health/ready',
  },
  torchserve: {
    label: 'TorchServe',
    description: 'PyTorch model server (local)',
    runtimeMode: 'local',
    image: '',
    command: 'torchserve\n--start\n--model-store=$PIPER_MODEL_DIR\n--foreground',
    port: '8080',
    healthPath: '/ping',
  },
  mlflow: {
    label: 'MLflow',
    description: 'MLflow built-in model server (local)',
    runtimeMode: 'local',
    image: '',
    command: 'mlflow\nmodels\nserve\n-m $PIPER_MODEL_DIR\n--port=5000\n--no-conda',
    port: '5000',
    healthPath: '/health',
  },
  vllm: {
    label: 'vLLM',
    description: 'High-throughput LLM inference engine (local)',
    runtimeMode: 'local',
    image: '',
    command: 'python\n-m vllm.entrypoints.openai.api_server\n--model=$PIPER_MODEL_DIR\n--port=8000',
    port: '8000',
    healthPath: '/health',
  },
  ollama: {
    label: 'Ollama',
    description: 'Local LLM runtime',
    runtimeMode: 'local',
    image: '',
    command: 'ollama\nserve',
    port: '11434',
    healthPath: '/api/version',
  },
  triton_k8s: {
    label: 'Triton (k8s)',
    description: 'NVIDIA Triton on Kubernetes',
    runtimeMode: 'k8s',
    image: 'nvcr.io/nvidia/tritonserver:24.12-py3',
    imageOptions: [
      'nvcr.io/nvidia/tritonserver:24.12-py3',
      'nvcr.io/nvidia/tritonserver:24.09-py3',
    ],
    command: 'tritonserver\n--model-repository=$PIPER_MODEL_DIR\n--http-port=8000',
    port: '8000',
    healthPath: '/v2/health/ready',
  },
  vllm_k8s: {
    label: 'vLLM (k8s)',
    description: 'vLLM inference engine on Kubernetes',
    runtimeMode: 'k8s',
    image: 'vllm/vllm-openai:latest',
    imageOptions: ['vllm/vllm-openai:latest', 'vllm/vllm-openai:v0.6.6'],
    command: 'python\n-m vllm.entrypoints.openai.api_server\n--model=$PIPER_MODEL_DIR\n--port=8000',
    port: '8000',
    healthPath: '/health',
  },
  torchserve_k8s: {
    label: 'TorchServe (k8s)',
    description: 'PyTorch TorchServe on Kubernetes',
    runtimeMode: 'k8s',
    image: 'pytorch/torchserve:latest-gpu',
    imageOptions: ['pytorch/torchserve:latest-gpu', 'pytorch/torchserve:latest'],
    command: 'torchserve\n--start\n--model-store=$PIPER_MODEL_DIR\n--foreground',
    port: '8080',
    healthPath: '/ping',
  },
  mlflow_k8s: {
    label: 'MLflow (k8s)',
    description: 'MLflow model server on Kubernetes',
    runtimeMode: 'k8s',
    image: 'ghcr.io/mlflow/mlflow:v2.19.0',
    imageOptions: ['ghcr.io/mlflow/mlflow:v2.19.0', 'ghcr.io/mlflow/mlflow:latest'],
    command: 'mlflow\nmodels\nserve\n-m $PIPER_MODEL_DIR\n--port=5000\n--no-conda\n--host=0.0.0.0',
    port: '5000',
    healthPath: '/health',
  },
}

export interface FormState {
  name: string
  pipeline: string
  run: string
  step: string
  artifact: string
  templateKey: string
  runtimeMode: string
  image: string
  command: string
  port: string
  healthPath: string
  worker: string
  k8sNamespace: string
  k8sReplicas: string
  k8sCPU: string
  k8sMemory: string
  k8sGPU: string
  k8sImagePullPolicy: string
}

export const DEFAULT_FORM: FormState = {
  name: '',
  pipeline: '',
  run: 'latest',
  step: '',
  artifact: '',
  templateKey: 'custom',
  runtimeMode: 'local',
  image: '',
  command: 'python\nserve.py',
  port: '8000',
  healthPath: '/',
  worker: '',
  k8sNamespace: 'default',
  k8sReplicas: '1',
  k8sCPU: '',
  k8sMemory: '',
  k8sGPU: '',
  k8sImagePullPolicy: 'Always',
}

export function buildYAML(f: FormState): string {
  const cmdLines = f.command.split('\n').filter(Boolean).map(c => `      - ${JSON.stringify(c)}`).join('\n')
  const isK8s = f.runtimeMode === 'k8s'

  // driver block
  const driverLines: string[] = [`  driver:`]
  if (f.image) driverLines.push(`    image: ${JSON.stringify(f.image)}`)

  // placement
  if (isK8s) {
    driverLines.push(`    placement:`, `      runtime: k8s`)
  } else if (f.worker) {
    driverLines.push(`    placement:`, `      worker: ${JSON.stringify(f.worker)}`)
  }

  // resources (top-level in driver, applies to all runtimes)
  const hasResources = f.k8sCPU || f.k8sMemory || f.k8sGPU
  if (hasResources) {
    driverLines.push(`    resources:`)
    if (f.k8sCPU)    driverLines.push(`      cpu: ${JSON.stringify(f.k8sCPU)}`)
    if (f.k8sMemory) driverLines.push(`      memory: ${JSON.stringify(f.k8sMemory)}`)
    if (f.k8sGPU)    driverLines.push(`      gpu: ${JSON.stringify(f.k8sGPU)}`)
  }

  // k8s-specific settings
  if (isK8s) {
    driverLines.push(
      `    k8s:`,
      `      namespace: ${JSON.stringify(f.k8sNamespace || 'default')}`,
      `      replicas: ${f.k8sReplicas || '1'}`,
      `      image_pull_policy: ${JSON.stringify(f.k8sImagePullPolicy || 'Always')}`,
    )
  }

  return `apiVersion: piper/v1
kind: ModelService
metadata:
  name: ${JSON.stringify(f.name || 'my-model')}
spec:
  model:
    from_artifact:
      pipeline: ${JSON.stringify(f.pipeline || 'my-pipeline')}
      step: ${JSON.stringify(f.step || 'train')}
      artifact: ${JSON.stringify(f.artifact || 'model')}
      run: ${JSON.stringify(f.run || 'latest')}
  run:
    command:
${cmdLines || '      - "python"\n      - "serve.py"'}
    port: ${f.port || '8000'}
    health_path: ${JSON.stringify(f.healthPath || '/')}
${driverLines.join('\n')}
`
}
