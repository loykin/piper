import { describe, expect, it } from 'vitest'
import {
  buildPipelineDraftYaml,
  defaultPipelineDraft,
  findPipelineDraftYamlDifference,
  parsePipelineDraftYaml,
  stripMetadataVersion,
} from './editor'
import { applyPreparePreset, autoMatchGitCredential, defaultTask, draftProblems, environmentDefaults, preparePresetLines } from './editorModel'

function sampleDraft() {
  const draft = defaultPipelineDraft()
  draft.name = 'train'
  const prep = defaultTask('command', 0)
  prep.name = 'prep'
  prep.command = ['echo', 'hi']
  const fit = defaultTask('python', 1)
  fit.name = 'fit'
  fit.sourcePath = 'fit.py'
  fit.dependsOn = ['prep']
  draft.steps = [prep, fit]
  return draft
}

describe('pipeline draft YAML', () => {
  it('round-trips through YAML without losing design data', () => {
    const yaml = buildPipelineDraftYaml(sampleDraft())
    const parsed = parsePipelineDraftYaml(yaml)
    expect(parsed.name).toBe('train')
    expect(parsed.steps.map(s => s.name)).toEqual(['prep', 'fit'])
    expect(parsed.steps[1].dependsOn).toEqual(['prep'])
    expect(findPipelineDraftYamlDifference(yaml, parsed)).toBeNull()
  })
})

describe('draftProblems', () => {
  it('reports unknown dependencies and missing sources', () => {
    const draft = sampleDraft()
    draft.steps[1].sourcePath = ''
    draft.steps[1].dependsOn = ['missing']
    const problems = draftProblems(draft, '')
    expect(problems).toContain('Task "fit" needs a source file.')
    expect(problems).toContain('Task "fit" depends on unknown task "missing"')
  })

  it('puts a runtime placement mismatch first', () => {
    const draft = sampleDraft()
    draft.defaults.placementRuntime = 'k8s'
    expect(draftProblems(draft, 'docker')[0]).toMatch(/owns the docker runtime/)
    expect(draftProblems(draft, 'k8s')).toEqual([])
  })
})

describe('autoMatchGitCredential', () => {
  const creds = [
    { name: 'host', endpoint: 'https://git.example.com' },
    { name: 'org', endpoint: 'https://git.example.com/acme' },
    { name: 'other', endpoint: 'https://git.other.com' },
  ]

  it('picks the most specific in-scope endpoint', () => {
    expect(autoMatchGitCredential(creds, 'https://git.example.com/acme/repo.git')?.name).toBe('org')
    expect(autoMatchGitCredential(creds, 'https://git.example.com/beta/repo.git')?.name).toBe('host')
  })

  it('respects path boundaries', () => {
    expect(autoMatchGitCredential(creds, 'https://git.example.com/acme-evil/repo.git')?.name).toBe('host')
    expect(autoMatchGitCredential(creds, '')).toBeUndefined()
  })
})

describe('defaultTask', () => {
  // The default used to be `python task.py`, which ran a file named task.py
  // regardless of the Script File the form set.
  it('runs the python task\'s own script file', () => {
    const task = defaultTask('python', 0)
    task.sourcePath = 'hello.py'
    const yaml = buildPipelineDraftYaml({ ...defaultPipelineDraft(), name: 'p', steps: [task] })
    const step = parsePipelineDraftYaml(yaml).steps[0]
    expect(step.sourcePath).toBe('hello.py')
    expect(step.command.join(' ')).toContain('$PIPER_SCRIPT_PATH')
    expect(yaml).not.toContain('task.py')
  })
})

describe('task templates', () => {
  for (const runtime of ['baremetal', 'docker', 'k8s']) {
    it(`notebook template round-trips without Design loss (${runtime})`, () => {
      const task = defaultTask('notebook', 0, runtime)
      task.sourcePath = 'train.ipynb'
      const yaml = buildPipelineDraftYaml({ ...defaultPipelineDraft(), name: 'p', steps: [task] })
      expect(yaml).not.toMatch(/^\s+(type|notebook):/m)
      expect(yaml).toContain('$PIPER_PARAMS_FILE')
      const draft = parsePipelineDraftYaml(yaml)
      expect(draft.steps[0].type).toBe('notebook')
      expect(findPipelineDraftYamlDifference(yaml, draft)).toBeNull()
    })
  }

  // Only the environment differs by runtime: baremetal builds a venv and its
  // own kernel; containers use the image's python.
  it('differs by runtime only in the environment', () => {
    const bare = defaultTask('notebook', 0, 'baremetal')
    const docker = defaultTask('notebook', 0, 'docker')
    expect(bare.prepare[0]).toBe('python3 -m venv .venv')
    expect(bare.command.join(' ')).toContain('-k piper-step')
    expect(docker.prepare.join('\n')).not.toContain('.venv')
    expect(bare.outputs).toEqual(docker.outputs)
  })
})

describe('params', () => {
  // The step reads params from $PIPER_PARAMS_FILE with their YAML types, so a
  // number typed in the form must stay a number.
  it('writes typed scalars and round-trips without Design loss', () => {
    const task = defaultTask('notebook', 0, 'docker')
    task.sourcePath = 'train.ipynb'
    task.params = [{ key: 'n_estimators', value: '120' }, { key: 'lr', value: '0.5' }, { key: 'name', value: 'iris' }, { key: 'flag', value: 'true' }]
    const yaml = buildPipelineDraftYaml({ ...defaultPipelineDraft(), name: 'p', steps: [task] })
    expect(yaml).toContain('n_estimators: 120\n')
    expect(yaml).toContain('lr: 0.5\n')
    expect(yaml).toContain('name: "iris"\n')
    expect(yaml).toContain('flag: true\n')
    expect(findPipelineDraftYamlDifference(yaml, parsePipelineDraftYaml(yaml))).toBeNull()
  })
})

describe('stripMetadataVersion', () => {
  it('drops only metadata.version and keeps the rest of the text', () => {
    const stored = 'apiVersion: piper/v1\nkind: Pipeline\nmetadata:\n  name: "train" # keep\n  version: 3\nspec:\n  steps: []\n'
    expect(stripMetadataVersion(stored)).toBe('apiVersion: piper/v1\nkind: Pipeline\nmetadata:\n  name: "train" # keep\nspec:\n  steps: []\n')
  })
})

describe('New Version of a template the editor made', () => {
  // The server stores the submitted text plus metadata.version; New Version
  // strips that and must reopen in Design with nothing to lose.
  it('reopens without a Design-loss warning', () => {
    const task = defaultTask('notebook', 0, 'baremetal')
    task.sourcePath = 'train.ipynb'
    task.prepare.push('.venv/bin/python -m pip install -q scikit-learn')
    task.params = [{ key: 'n_estimators', value: '120' }]
    const submitted = buildPipelineDraftYaml({ ...defaultPipelineDraft(), name: 'iris', steps: [task] })
    const stored = submitted.replace('  name: "iris"\n', '  name: "iris"\n  version: 1\n')
    const reopened = stripMetadataVersion(stored)
    expect(findPipelineDraftYamlDifference(reopened, parsePipelineDraftYaml(reopened))).toBeNull()
  })
})

describe('prepare presets', () => {
  it('install into the same environment the template sets up', () => {
    expect(preparePresetLines('pip', 'baremetal')).toEqual(['.venv/bin/python -m pip install -q -r requirements.txt'])
    expect(preparePresetLines('pip', 'k8s')).toEqual(['python3 -m pip install -q -r requirements.txt'])
    expect(preparePresetLines('uv', 'baremetal')[1]).toBe('.venv/bin/python -m uv pip install --python .venv/bin/python -r pyproject.toml')
    expect(preparePresetLines('uv', 'docker')[1]).toBe('python3 -m uv pip install --system -r pyproject.toml')
  })
})

describe('environmentDefaults', () => {
  const empty = defaultPipelineDraft().defaults
  it('fills the source notebook image and the first allowed k8s namespace', () => {
    const k8s = environmentDefaults(empty, 'k8s', 'jupyter/minimal-notebook:latest', ['piper', 'team-a'])
    expect([k8s.k8sImage, k8s.k8sNamespace]).toEqual(['jupyter/minimal-notebook:latest', 'piper'])
    expect(environmentDefaults(empty, 'docker', 'img:1', []).dockerImage).toBe('img:1')
    expect(environmentDefaults(empty, 'baremetal', 'img:1', [])).toEqual({ ...empty, placementRuntime: 'baremetal' })
  })
  it('keeps values already set', () => {
    const set = { ...empty, placementRuntime: 'k8s', k8sImage: 'mine:2', k8sNamespace: 'team-a' }
    expect(environmentDefaults(set, 'k8s', 'img:1', ['piper'])).toEqual(set)
  })
})

describe('applyPreparePreset', () => {
  // The snapshot holds only the entry file and deps, so the file a preset
  // installs from has to be added to deps or the line fails at run time.
  it('adds the preset file to deps once', () => {
    const once = applyPreparePreset({ prepare: [], deps: [] }, 'pip', 'docker')
    expect(once.deps).toEqual(['requirements.txt'])
    expect(applyPreparePreset(once, 'pip', 'docker').deps).toEqual(['requirements.txt'])
    expect(applyPreparePreset({ prepare: [], deps: [] }, 'uv', 'k8s').deps).toEqual(['pyproject.toml'])
  })
})
