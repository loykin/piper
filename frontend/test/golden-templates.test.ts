import { mkdirSync, readFileSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
import { describe, expect, it } from 'vitest'
import { buildPipelineDraftYaml, defaultPipelineDraft } from '../src/features/pipelines/editor.ts'
import { applyPreparePreset, defaultTask, environmentDefaults } from '../src/features/pipelines/editorModel.ts'

// The golden-path e2e (golden_path_e2e_test.go) submits exactly the YAML the
// editor produces for "Notebook Task + pip preset + n_estimators=120" on each
// runtime. These files are that YAML; this test keeps them equal to what the
// editor emits, so the e2e always runs the UI's real output.
// Regenerate after an intended editor change: GOLDEN_UPDATE=1 pnpm test
const GOLDEN = join(import.meta.dirname, '../../testdata/golden')
const IMAGE = 'jupyter/minimal-notebook:latest'
// The k8s e2e substitutes its namespace for this placeholder.
const NAMESPACE = 'piper-golden'

function editorYaml(runtime: string): string {
  const task = defaultTask('notebook', 0, runtime)
  task.sourcePath = 'train_iris.ipynb'
  Object.assign(task, applyPreparePreset(task, 'pip', runtime))
  task.params = [{ key: 'n_estimators', value: '120' }]
  const defaults = environmentDefaults(defaultPipelineDraft().defaults, runtime, IMAGE, [NAMESPACE])
  return buildPipelineDraftYaml({ ...defaultPipelineDraft(), name: 'iris', steps: [task], defaults })
}

describe('golden-path template fixtures', () => {
  for (const runtime of ['baremetal', 'docker', 'k8s']) {
    it(`match the editor's output (${runtime})`, () => {
      const path = join(GOLDEN, `notebook-template.${runtime}.yaml`)
      const yaml = editorYaml(runtime)
      if (process.env.GOLDEN_UPDATE) {
        mkdirSync(GOLDEN, { recursive: true })
        writeFileSync(path, yaml)
      }
      expect(readFileSync(path, 'utf8')).toBe(yaml)
    })
  }
})
