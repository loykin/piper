import { describe, expect, it } from 'vitest'
import {
  buildPipelineDraftYaml,
  defaultPipelineDraft,
  findPipelineDraftYamlDifference,
  parsePipelineDraftYaml,
} from './editor'
import { autoMatchGitCredential, defaultTask, draftProblems } from './editorModel'

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
