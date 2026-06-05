import { useCallback, useEffect, useMemo } from 'react'
import {
  Background,
  BaseEdge,
  Controls,
  EdgeLabelRenderer,
  getBezierPath,
  Handle,
  Position,
  ReactFlow,
  ReactFlowProvider,
  useEdgesState,
  useNodesState,
  useReactFlow,
  type Connection,
  type Edge,
  type EdgeChange,
  type EdgeTypes,
  type Node,
  type NodeTypes,
} from '@xyflow/react'
import '@xyflow/react/dist/style.css'
import { X } from 'lucide-react'
import type { PipelineStepDraft } from '@/features/pipelines/editor'
import type { DragEvent } from 'react'

interface PipelineCanvasProps {
  steps: PipelineStepDraft[]
  positions: Record<string, { x: number; y: number }>
  selectedId: string | null
  resetKey?: number
  onSelectStep: (id: string) => void
  onDoubleClickStep: (id: string) => void
  onAddStep: (type?: PipelineStepDraft['type'], position?: { x: number; y: number }) => void
  onMoveStep: (id: string, position: { x: number; y: number }) => void
  onConnectSteps: (sourceId: string, targetId: string) => void
  onDisconnectSteps: (sourceId: string, targetId: string) => void
}

interface StepNodeData extends Record<string, unknown> {
  title: string
  subtitle: string
  badge: string
  selected: boolean
}

function StepNode({ data }: { data: StepNodeData }) {
  return (
    <div
      className={[
        'relative w-56 rounded-xl border px-3 py-2 shadow-sm transition',
        data.selected
          ? 'border-primary bg-primary/10 ring-2 ring-primary/30'
          : 'border-border bg-card hover:border-primary/60 hover:bg-accent',
      ].join(' ')}
    >
      <Handle
        type="target"
        position={Position.Left}
        className="!h-3 !w-3 !border-2 !border-background !bg-primary"
      />
      <div className="flex items-start gap-2">
        <div className="min-w-0 flex-1">
          <div className="truncate text-sm font-semibold text-foreground">{data.title}</div>
          <div className="mt-0.5 truncate text-[11px] leading-4 text-muted-foreground">
            {data.subtitle}
          </div>
        </div>
        <span className="mt-0.5 shrink-0 rounded bg-muted px-1 py-0.5 text-[9px] font-semibold uppercase tracking-wide text-muted-foreground">
          {data.badge}
        </span>
      </div>
      <Handle
        type="source"
        position={Position.Right}
        className="!h-3 !w-3 !border-2 !border-background !bg-primary"
      />
    </div>
  )
}

function DeleteButtonEdge({
  id,
  sourceX,
  sourceY,
  targetX,
  targetY,
  selected,
}: {
  id: string
  sourceX: number
  sourceY: number
  targetX: number
  targetY: number
  selected?: boolean
}) {
  const { deleteElements } = useReactFlow()
  const [edgePath, labelX, labelY] = getBezierPath({ sourceX, sourceY, targetX, targetY })

  return (
    <>
      <BaseEdge
        path={edgePath}
        style={{
          stroke: selected ? '#818cf8' : '#4b5563',
          strokeWidth: selected ? 2.5 : 1.5,
        }}
      />
      {selected && (
        <EdgeLabelRenderer>
          <button
            type="button"
            style={{
              position: 'absolute',
              transform: `translate(-50%,-50%) translate(${labelX}px,${labelY}px)`,
              pointerEvents: 'all',
            }}
            className="nodrag nopan flex h-5 w-5 items-center justify-center rounded-full border border-border bg-background text-muted-foreground shadow hover:border-destructive hover:bg-destructive hover:text-white"
            onClick={() => deleteElements({ edges: [{ id }] })}
          >
            <X size={10} />
          </button>
        </EdgeLabelRenderer>
      )}
    </>
  )
}

const nodeTypes: NodeTypes = { stepNode: StepNode as never }
const edgeTypes: EdgeTypes = { deleteEdge: DeleteButtonEdge as never }

function layoutSeed(index: number): { x: number; y: number } {
  return {
    x: 40 + (index % 3) * 280,
    y: 40 + Math.floor(index / 3) * 150,
  }
}

function buildNodeData(step: PipelineStepDraft, selectedId: string | null): StepNodeData {
  return {
    title: step.name,
    subtitle:
      step.type === 'notebook'
        ? (step.sourcePath || 'notebook task')
        : step.type === 'python'
          ? (step.sourcePath || 'python task')
          : (step.command.length > 0 ? step.command.join(' ') : 'command task'),
    badge: step.type,
    selected: selectedId === step.id,
  }
}

function PipelineCanvasInner({
  steps,
  positions,
  selectedId,
  resetKey,
  onSelectStep,
  onDoubleClickStep,
  onAddStep,
  onMoveStep,
  onConnectSteps,
  onDisconnectSteps,
}: PipelineCanvasProps) {
  const { screenToFlowPosition, fitView } = useReactFlow()
  const [nodes, setNodes, onNodesChange] = useNodesState<Node<StepNodeData>>([])
  const [edges, setEdges, onEdgesChange] = useEdgesState<Edge>([])

  const idByName = useMemo(
    () => new Map(steps.map(s => [s.name, s.id])),
    [steps],
  )

  // Sync steps → nodes. Keep existing node positions managed by ReactFlow;
  // only seed position from prop for newly added nodes.
  useEffect(() => {
    setNodes(prev => {
      const prevById = new Map(prev.map(n => [n.id, n]))
      return steps.map((step, i) => {
        const prevNode = prevById.get(step.id)
        return {
          id: step.id,
          type: 'stepNode',
          position: prevNode
            ? prevNode.position
            : (positions[step.id] ?? layoutSeed(i)),
          data: buildNodeData(step, selectedId),
        }
      })
    })
  }, [steps, selectedId])

  // When resetKey changes, force-apply all positions from prop (layout reset).
  useEffect(() => {
    if (!resetKey) return
    setNodes(prev =>
      prev.map(n => ({ ...n, position: positions[n.id] ?? n.position })),
    )
    setTimeout(() => fitView({ padding: 0.2, duration: 300 }), 50)
  }, [resetKey])

  // Derive edges from steps.dependsOn.
  useEffect(() => {
    setEdges(
      steps.flatMap(step =>
        step.dependsOn
          .map(depName => {
            const sourceId = idByName.get(depName)
            if (!sourceId) return null
            return {
              id: `${sourceId}->${step.id}`,
              source: sourceId,
              target: step.id,
              type: 'deleteEdge',
            } as Edge
          })
          .filter((e): e is Edge => e !== null),
      ),
    )
  }, [steps, idByName])

  // On edge removal, propagate disconnect to parent.
  const handleEdgesChange = useCallback(
    (changes: EdgeChange<Edge>[]) => {
      for (const change of changes) {
        if (change.type === 'remove') {
          const edge = edges.find(e => e.id === change.id)
          if (edge) onDisconnectSteps(edge.source, edge.target)
        }
      }
      onEdgesChange(changes)
    },
    [edges, onEdgesChange, onDisconnectSteps],
  )

  const handleConnect = useCallback(
    (params: Connection) => {
      if (!params.source || !params.target || params.source === params.target) return
      onConnectSteps(params.source, params.target)
    },
    [onConnectSteps],
  )

  const handleNodeDragStop = useCallback(
    (_event: unknown, node: Node) => {
      onMoveStep(node.id, node.position)
    },
    [onMoveStep],
  )

  const onDrop = useCallback(
    (event: DragEvent<HTMLDivElement>) => {
      event.preventDefault()
      const kind =
        event.dataTransfer.getData('application/x-piper-step') ||
        event.dataTransfer.getData('text/plain')
      const type =
        kind === 'notebook' || kind === 'python' || kind === 'command' ? kind : 'command'
      onAddStep(type, screenToFlowPosition({ x: event.clientX, y: event.clientY }))
    },
    [onAddStep, screenToFlowPosition],
  )

  const onDragOver = useCallback((event: DragEvent<HTMLDivElement>) => {
    event.preventDefault()
    event.dataTransfer.dropEffect = 'copy'
  }, [])

  // Fit view when node count changes.
  useEffect(() => {
    if (steps.length === 0) return
    fitView({ padding: 0.2, duration: 200 })
  }, [fitView, steps.length])

  return (
    <div className="relative h-full overflow-hidden rounded-xl border border-border bg-background">

      <ReactFlow
        nodes={nodes}
        edges={edges}
        nodeTypes={nodeTypes}
        edgeTypes={edgeTypes}
        onNodesChange={onNodesChange}
        onEdgesChange={handleEdgesChange}
        onConnect={handleConnect}
        onNodeDragStop={handleNodeDragStop}
        onNodeClick={(_event, node) => onSelectStep(node.id)}
        onNodeDoubleClick={(_event, node) => onDoubleClickStep(node.id)}
        onDrop={onDrop}
        onDragOver={onDragOver}
        deleteKeyCode="Delete"
        snapToGrid
        snapGrid={[20, 20]}
        defaultEdgeOptions={{ type: 'deleteEdge' }}
        fitView
        minZoom={0.3}
        maxZoom={1.8}
        proOptions={{ hideAttribution: true }}
      >
        <Background gap={20} size={1} color="#1f2937" />
        <Controls showInteractive={false} />
      </ReactFlow>
    </div>
  )
}

export default function PipelineCanvas(props: PipelineCanvasProps) {
  return (
    <ReactFlowProvider>
      <PipelineCanvasInner {...props} />
    </ReactFlowProvider>
  )
}
