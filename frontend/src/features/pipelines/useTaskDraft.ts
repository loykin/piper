import { useCallback, useEffect, useState } from 'react'
import { emptyEnvVarDraft, type EnvVarDraft } from '@/shared/env'
import type { PipelineArtifactDraft, PipelineKeyValueDraft, PipelineStepDraft, PipelineTaskType } from './editor'
import { buildPositions, defaultTask, emptyArtifact, emptyPair } from './editorModel'

type Position = { x: number; y: number }

/**
 * The pipeline editor's step list plus canvas layout and selection, with
 * every step mutation the Design tab performs. Renaming a step rewrites the
 * dependsOn references to it; removing one drops them.
 */
export function useTaskDraft(initialSteps: PipelineStepDraft[]) {
  const [tasks, setTasks] = useState<PipelineStepDraft[]>(initialSteps)
  const [positions, setPositions] = useState<Record<string, Position>>(() => buildPositions(initialSteps))
  const [selectedId, setSelectedId] = useState<string>(initialSteps[0]?.id ?? '')
  const [editingId, setEditingId] = useState<string | null>(null)

  useEffect(() => {
    setPositions(prev => {
      const next = { ...prev }
      const known = new Set(tasks.map(t => t.id))
      for (const task of tasks) {
        if (!next[task.id]) next[task.id] = buildPositions(tasks)[task.id] ?? { x: 24, y: 24 }
      }
      for (const id of Object.keys(next)) {
        if (!known.has(id)) delete next[id]
      }
      return next
    })
  }, [tasks])

  /** Replaces every step (YAML applied, previous version loaded) and re-lays out the canvas. */
  const replaceSteps = useCallback((steps: PipelineStepDraft[]) => {
    setTasks(steps)
    setPositions(buildPositions(steps))
    setSelectedId(steps[0]?.id ?? '')
    setEditingId(null)
  }, [])

  function updateTask(index: number, patch: Partial<PipelineStepDraft>) {
    setTasks(current => {
      const next = current.map((task, i) => i !== index ? task : { ...task, ...patch })
      const prev = current[index]
      const nextName = patch.name?.trim()
      if (prev && nextName && nextName !== prev.name) {
        next.forEach((task, i) => {
          if (i !== index) task.dependsOn = task.dependsOn.map(dep => dep === prev.name ? nextName : dep)
        })
      }
      return next
    })
  }

  function updateTaskDriver(index: number, patch: Partial<PipelineStepDraft['driver']>) {
    setTasks(current => current.map((task, i) => (
      i === index ? { ...task, driver: { ...task.driver, ...patch } } : task
    )))
  }

  function updateDep(index: number, depIndex: number, val: string) {
    setTasks(current => current.map((task, i) => {
      if (i !== index) return task
      const deps = [...task.deps]
      deps[depIndex] = val
      return { ...task, deps }
    }))
  }

  function addDep(index: number) {
    setTasks(current => current.map((task, i) =>
      i !== index ? task : { ...task, deps: [...task.deps, ''] }
    ))
  }

  function removeDep(index: number, depIndex: number) {
    setTasks(current => current.map((task, i) =>
      i !== index ? task : { ...task, deps: task.deps.filter((_, j) => j !== depIndex) }
    ))
  }

  function addTask(type: PipelineTaskType = 'command', position?: { x: number; y: number }) {
    setTasks(current => {
      const next = [...current, defaultTask(type, current.length)]
      const created = next[next.length - 1]
      setSelectedId(created.id)
      if (position) setPositions(pos => ({ ...pos, [created.id]: position }))
      return next
    })
  }

  function removeTask(index: number) {
    setTasks(current => {
      const removed = current[index]
      const next = current.filter((_, i) => i !== index).map(task => ({
        ...task,
        dependsOn: task.dependsOn.filter(dep => dep !== removed?.name),
      }))
      if (removed) setPositions(pos => { const p = { ...pos }; delete p[removed.id]; return p })
      if (selectedId === removed?.id) setSelectedId(next[0]?.id ?? '')
      if (editingId === removed?.id) setEditingId(null)
      return next
    })
  }

  function moveTask(index: number, delta: -1 | 1) {
    setTasks(current => {
      const target = index + delta
      if (target < 0 || target >= current.length) return current
      const next = [...current]
      const [item] = next.splice(index, 1)
      next.splice(target, 0, item)
      return next
    })
  }

  function connectTasks(sourceId: string, targetId: string) {
    setTasks(current => {
      const source = current.find(t => t.id === sourceId)
      const targetIndex = current.findIndex(t => t.id === targetId)
      if (!source || targetIndex < 0 || source.id === targetId) return current
      setSelectedId(targetId)
      return current.map(task => task.id !== targetId ? task : {
        ...task,
        dependsOn: Array.from(new Set([...task.dependsOn, source.name])),
      })
    })
  }

  function disconnectTasks(sourceId: string, targetId: string) {
    setTasks(current => {
      const source = current.find(t => t.id === sourceId)
      if (!source) return current
      return current.map(task => task.id !== targetId ? task : {
        ...task,
        dependsOn: task.dependsOn.filter(dep => dep !== source.name),
      })
    })
  }

  function updateArtifactField(index: number, kind: 'inputs' | 'outputs', rowIndex: number, patch: Partial<PipelineArtifactDraft>) {
    setTasks(current => current.map((task, i) => {
      if (i !== index) return task
      const items = [...task[kind]]
      items[rowIndex] = { ...items[rowIndex], ...patch }
      return { ...task, [kind]: items } as PipelineStepDraft
    }))
  }

  function updateParamField(index: number, rowIndex: number, patch: Partial<PipelineKeyValueDraft>) {
    setTasks(current => current.map((task, i) => {
      if (i !== index) return task
      const items = [...task.params]
      items[rowIndex] = { ...items[rowIndex], ...patch }
      return { ...task, params: items } as PipelineStepDraft
    }))
  }

  function updateEnvField(index: number, rowIndex: number, patch: Partial<EnvVarDraft>) {
    setTasks(current => current.map((task, i) => {
      if (i !== index) return task
      const env = [...task.env]
      env[rowIndex] = { ...env[rowIndex], ...patch }
      return { ...task, env } as PipelineStepDraft
    }))
  }

  function addArtifactRow(index: number, kind: 'inputs' | 'outputs') {
    setTasks(current => current.map((task, i) =>
      i !== index ? task : { ...task, [kind]: [...task[kind], emptyArtifact()] } as PipelineStepDraft
    ))
  }

  function removeArtifactRow(index: number, kind: 'inputs' | 'outputs', rowIndex: number) {
    setTasks(current => current.map((task, i) =>
      i !== index ? task : { ...task, [kind]: task[kind].filter((_, j) => j !== rowIndex) } as PipelineStepDraft
    ))
  }

  function addParamRow(index: number) {
    setTasks(current => current.map((task, i) =>
      i !== index ? task : { ...task, params: [...task.params, emptyPair()] } as PipelineStepDraft
    ))
  }

  function addEnvRow(index: number) {
    setTasks(current => current.map((task, i) =>
      i !== index ? task : { ...task, env: [...task.env, emptyEnvVarDraft()] } as PipelineStepDraft
    ))
  }

  function removeParamRow(index: number, rowIndex: number) {
    setTasks(current => current.map((task, i) =>
      i !== index ? task : { ...task, params: task.params.filter((_, j) => j !== rowIndex) } as PipelineStepDraft
    ))
  }

  function removeEnvRow(index: number, rowIndex: number) {
    setTasks(current => current.map((task, i) =>
      i !== index ? task : { ...task, env: task.env.filter((_, j) => j !== rowIndex) } as PipelineStepDraft
    ))
  }

  return {
    tasks, positions, setPositions, selectedId, setSelectedId, editingId, setEditingId, replaceSteps,
    updateTask, updateTaskDriver, updateDep, addDep, removeDep, addTask, removeTask, moveTask,
    connectTasks, disconnectTasks, updateArtifactField, updateParamField, updateEnvField,
    addArtifactRow, removeArtifactRow, addParamRow, addEnvRow, removeParamRow, removeEnvRow,
  }
}

export type TaskDraft = ReturnType<typeof useTaskDraft>
export type { EnvVarDraft, PipelineArtifactDraft, PipelineKeyValueDraft, PipelineTaskType }
