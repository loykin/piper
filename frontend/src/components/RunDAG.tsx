import { useMemo, useCallback } from 'react'
import {
  ReactFlow,
  Background,
  Controls,
  type Node,
  type Edge,
  type NodeTypes,
  Position,
  Handle,
  useNodesState,
  useEdgesState,
} from '@xyflow/react'
import '@xyflow/react/dist/style.css'
import type { Step } from '../api'

// ── YAML parser (minimal — no external dep) ──────────────────────────────────

interface StepDef {
  name: string
  depends_on: string[]
}

function parseStepsFromYaml(yaml?: string | null): StepDef[] {
  if (!yaml) return []
  const steps: StepDef[] = []
  // Split on "- name:" blocks
  const blocks = yaml.split(/\n(?=\s*-\s+name:)/)
  for (const block of blocks) {
    const nameMatch = block.match(/^\s*-\s+name:\s*(.+)/)
    if (!nameMatch) continue
    const name = nameMatch[1].trim()

    // depends_on: [a, b] or multi-line list
    let depends_on: string[] = []
    const inlineMatch = block.match(/depends_on:\s*\[([^\]]*)]/)
    if (inlineMatch) {
      depends_on = inlineMatch[1]
        .split(',')
        .map(s => s.trim().replace(/['"]/g, ''))
        .filter(Boolean)
    } else {
      const multilineMatch = block.match(/depends_on:([\s\S]*?)(?=\n\s+\w|\n\s*-\s+name:|$)/)
      if (multilineMatch) {
        const items = multilineMatch[1].matchAll(/^\s*-\s+(.+)$/gm)
        for (const m of items) {
          depends_on.push(m[1].trim().replace(/['"]/g, ''))
        }
      }
    }

    steps.push({ name, depends_on })
  }
  return steps
}

// ── Layout (simple topological left-to-right) ────────────────────────────────

function layoutNodes(steps: StepDef[]): Map<string, { x: number; y: number }> {
  // Compute depth (longest path from a root)
  const depth = new Map<string, number>()
  const compute = (name: string, visited = new Set<string>()): number => {
    if (depth.has(name)) return depth.get(name)!
    if (visited.has(name)) return 0
    visited.add(name)
    const step = steps.find(s => s.name === name)
    if (!step || step.depends_on.length === 0) {
      depth.set(name, 0)
      return 0
    }
    const d = Math.max(...step.depends_on.map(dep => compute(dep, visited) + 1))
    depth.set(name, d)
    return d
  }
  for (const s of steps) compute(s.name)

  // Group by depth
  const cols = new Map<number, string[]>()
  for (const [name, d] of depth) {
    if (!cols.has(d)) cols.set(d, [])
    cols.get(d)!.push(name)
  }

  const positions = new Map<string, { x: number; y: number }>()
  const NODE_W = 180
  const NODE_H = 60
  const GAP_X = 60
  const GAP_Y = 20

  for (const [col, names] of cols) {
    names.forEach((name, row) => {
      positions.set(name, {
        x: col * (NODE_W + GAP_X),
        y: row * (NODE_H + GAP_Y),
      })
    })
  }
  return positions
}

// ── Status helpers ────────────────────────────────────────────────────────────

const STATUS_BORDER: Record<string, string> = {
  done:    'border-green-500',
  success: 'border-green-500',
  running: 'border-blue-400',
  failed:  'border-red-500',
  pending: 'border-gray-600',
  skipped: 'border-yellow-500',
}

const STATUS_BG: Record<string, string> = {
  done:    'bg-green-500/10',
  success: 'bg-green-500/10',
  running: 'bg-blue-500/10',
  failed:  'bg-red-500/10',
  pending: 'bg-gray-800',
  skipped: 'bg-yellow-500/10',
}

const STATUS_DOT: Record<string, string> = {
  done:    '✅',
  success: '✅',
  running: '⏳',
  failed:  '❌',
  pending: '⌛',
  skipped: '⏭',
}

// ── Custom node ───────────────────────────────────────────────────────────────

interface StepNodeData {
  label: string
  status: string
  selected: boolean
  onClick: () => void
  [key: string]: unknown
}

function StepNode({ data }: { data: StepNodeData }) {
  const border = STATUS_BORDER[data.status] ?? STATUS_BORDER.pending
  const bg     = STATUS_BG[data.status]    ?? STATUS_BG.pending
  const dot    = STATUS_DOT[data.status]   ?? '⌛'
  const ring   = data.selected ? 'ring-2 ring-indigo-400 ring-offset-1 ring-offset-gray-950' : ''

  return (
    <div
      onClick={data.onClick}
      className={`
        cursor-pointer rounded-lg border-2 ${border} ${bg} ${ring}
        px-3 py-2 w-40 text-center transition-all
        hover:brightness-125
      `}
    >
      <Handle type="target" position={Position.Left} className="bg-gray-600! border-gray-500!" />
      <div className="text-xs text-gray-400 mb-0.5">{dot}</div>
      <div className="text-sm font-medium text-gray-100 truncate">{data.label}</div>
      {data.status === 'running' && (
        <span className="mt-1 inline-block h-1.5 w-1.5 rounded-full bg-blue-400 animate-pulse" />
      )}
      <Handle type="source" position={Position.Right} className="bg-gray-600! border-gray-500!" />
    </div>
  )
}

const nodeTypes: NodeTypes = { stepNode: StepNode as never }

// ── Main component ────────────────────────────────────────────────────────────

interface RunDAGProps {
  pipelineYaml?: string | null
  steps: Step[]
  selected: string | null
  onSelectStep: (name: string) => void
}

export default function RunDAG({ pipelineYaml, steps, selected, onSelectStep }: RunDAGProps) {
  const stepMap = useMemo(() => {
    const m = new Map<string, Step>()
    for (const s of steps) m.set(s.step_name, s)
    return m
  }, [steps])

  const parsedStepDefs = useMemo(() => parseStepsFromYaml(pipelineYaml), [pipelineYaml])
  const stepDefs = useMemo(
    () => (parsedStepDefs.length > 0 ? parsedStepDefs : steps.map((s) => ({ name: s.step_name, depends_on: [] }))),
    [parsedStepDefs, steps],
  )
  const positions = useMemo(() => layoutNodes(stepDefs), [stepDefs])

  const initialNodes: Node[] = useMemo(() =>
    stepDefs.map(def => {
      const step = stepMap.get(def.name)
      const status = step?.status ?? 'pending'
      return {
        id: def.name,
        type: 'stepNode',
        position: positions.get(def.name) ?? { x: 0, y: 0 },
        data: {
          label: def.name,
          status,
          selected: selected === def.name,
          onClick: () => onSelectStep(def.name),
        },
      }
    }),
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [stepDefs, stepMap, selected],
  )

  const initialEdges: Edge[] = useMemo(() =>
    stepDefs.flatMap(def =>
      def.depends_on.map(dep => ({
        id: `${dep}->${def.name}`,
        source: dep,
        target: def.name,
        animated: stepMap.get(def.name)?.status === 'running',
        style: { stroke: '#4b5563' },
        type: 'smoothstep',
      }))
    ),
    [stepDefs, stepMap],
  )

  const [nodes, , onNodesChange] = useNodesState(initialNodes)
  const [edges, , onEdgesChange] = useEdgesState(initialEdges)

  // Sync nodes when status/selection changes
  const syncedNodes = useMemo(() =>
    nodes.map(n => {
      const step = stepMap.get(n.id)
      const status = step?.status ?? 'pending'
      return {
        ...n,
        data: {
          ...n.data,
          status,
          selected: selected === n.id,
          onClick: () => onSelectStep(n.id),
        },
      }
    }),
    [nodes, stepMap, selected, onSelectStep],
  )

  const syncedEdges = useMemo(() =>
    edges.map(e => ({
      ...e,
      animated: stepMap.get(e.target)?.status === 'running',
    })),
    [edges, stepMap],
  )

  const onNodeClick = useCallback(() => {
    // handled inside StepNode via data.onClick
  }, [])

  if (stepDefs.length === 0) return null

  return (
    <section className="rounded-xl border border-gray-800 bg-gray-950 overflow-hidden">
      <div className="border-b border-gray-800 bg-gray-900 px-4 py-3">
        <h2 className="text-sm font-semibold uppercase tracking-wider text-gray-300">Pipeline DAG</h2>
      </div>
      <div style={{ height: Math.max(260, stepDefs.length * 30 + 120) }}>
        <ReactFlow
          nodes={syncedNodes}
          edges={syncedEdges}
          onNodesChange={onNodesChange}
          onEdgesChange={onEdgesChange}
          onNodeClick={onNodeClick}
          nodeTypes={nodeTypes}
          fitView
          fitViewOptions={{ padding: 0.3 }}
          proOptions={{ hideAttribution: true }}
          colorMode="dark"
          className="bg-gray-950"
        >
          <Background color="#1f2937" gap={20} />
          <Controls className="bg-gray-900! border-gray-700! text-gray-300!" />
        </ReactFlow>
      </div>
    </section>
  )
}
