import { useMemo } from 'react'
import { useNavigate } from '@/lib/router'
import { stringify as yamlStringify } from 'yaml'
import { DataBodyTemplate, PageTopBar, PanelTemplate } from '@loykin/designkit'
import { DataGrid, DataGridPaginationCompact, type DataGridColumnDef } from '@loykin/gridkit'
import { SidePanelProvider, useSidePanel } from '@loykin/side-panel'
import { X } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { IconButton } from '@/components/ui/icon-button'
import { PodPolicyForm } from '@/features/workers/components/PodPolicyForm'
import { useWorkerPodPolicies, useDeleteWorkerPodPolicy } from '@/features/workers/hooks'
import type { WorkerPodPolicy } from '@/features/workers/types'

function relativeTime(ts: string): string {
  const ms = Date.now() - new Date(ts).getTime()
  if (ms < 60_000) return `${Math.floor(ms / 1000)}s ago`
  if (ms < 3_600_000) return `${Math.floor(ms / 60_000)}m ago`
  if (ms < 86_400_000) return `${Math.floor(ms / 3_600_000)}h ago`
  return `${Math.floor(ms / 86_400_000)}d ago`
}

// ── Edit sheet ────────────────────────────────────────────────────────────────

function EditPolicyPanel({ policy }: { policy: WorkerPodPolicy }) {
  const { close } = useSidePanel()
  const { mutateAsync: del, isPending: deleting } = useDeleteWorkerPodPolicy()

  const initialYaml = useMemo(
    () => policy.pod_template && Object.keys(policy.pod_template).length > 0
      ? yamlStringify(policy.pod_template, { indent: 2 })
      : '',
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [policy.worker_id],
  )

  return (
    <PanelTemplate
      eyebrow="Pod Policy"
      title={<span className="break-all font-mono text-sm">{policy.worker_id}</span>}
      actions={<IconButton icon={<X />} label="Close" onClick={() => void close()} />}
    >
      <PanelTemplate.Section title="Pod Template">
        <PodPolicyForm
          workerId={policy.worker_id}
          initialYaml={initialYaml}
          onSaved={() => { /* already invalidated by mutation */ }}
          onCancel={() => void close()}
        />
      </PanelTemplate.Section>

      <PanelTemplate.Section title="Danger Zone">
        <p className="mb-2 text-xs text-muted-foreground">
          Permanently remove the pod policy for this worker.
        </p>
        <Button
          variant="destructive"
          size="sm"
          disabled={deleting}
          onClick={async () => {
            if (!confirm(`Remove pod policy for ${policy.worker_id}?`)) return
            await del(policy.worker_id)
            void close()
          }}
        >
          {deleting ? 'Removing…' : 'Remove policy'}
        </Button>
      </PanelTemplate.Section>
    </PanelTemplate>
  )
}

// ── List page ─────────────────────────────────────────────────────────────────

function PodPoliciesContent() {
  const navigate = useNavigate()
  const { data: policies = [], isLoading } = useWorkerPodPolicies()
  const { open } = useSidePanel()

  const columns = useMemo<DataGridColumnDef<WorkerPodPolicy>[]>(() => [
    {
      accessorKey: 'worker_id',
      header: 'Worker ID',
      meta: { minWidth: 300, flex: 1 },
      cell: ({ row }) => (
        <span className="font-mono text-xs">{row.original.worker_id}</span>
      ),
    },
    {
      accessorKey: 'updated_by',
      header: 'Updated By',
      size: 140,
      meta: { minWidth: 100 },
      cell: ({ row }) => (
        <span className="text-xs text-muted-foreground">{row.original.updated_by || '—'}</span>
      ),
    },
    {
      accessorKey: 'updated_at',
      header: 'Updated',
      size: 110,
      meta: { minWidth: 100 },
      cell: ({ row }) => (
        <span className="text-xs text-muted-foreground">{relativeTime(row.original.updated_at)}</span>
      ),
    },
  ], [])

  return (
    <DataBodyTemplate
      topBar={<PageTopBar left="Kubernetes / Pod Policies" />}
      title="Pod Policies"
      description="Per-worker baseline pod templates merged into notebook dispatches at dispatch time. Policies persist for offline workers — create a policy before the worker connects."
      actions={
        <Button size="sm" onClick={() => navigate('/kubernetes/pod-policies/new')}>
          Add policy
        </Button>
      }
    >
      <DataBodyTemplate.Body>
        <DataGrid
          data={policies}
          columns={columns}
          isLoading={isLoading}
          emptyMessage="No pod policies configured. Click 'Add policy' to create one."
          tableWidthMode="fill-last"
          rowHeight={44}
          onRowClick={(row) => open(<EditPolicyPanel policy={row} />, { size: 560 })}
          pagination={{ pageSize: 20 }}
          footer={(table) => (
            <div className="flex h-9 items-center justify-between px-1 text-xs text-muted-foreground">
              <span>
                {policies.length} {policies.length === 1 ? 'policy' : 'policies'}
              </span>
              <DataGridPaginationCompact table={table} />
            </div>
          )}
        />
      </DataBodyTemplate.Body>
    </DataBodyTemplate>
  )
}

export default function PodPoliciesPage() {
  return (
    <SidePanelProvider defaultSize={560} defaultMinSize={420} defaultMaxSize={900}>
      <PodPoliciesContent />
    </SidePanelProvider>
  )
}
