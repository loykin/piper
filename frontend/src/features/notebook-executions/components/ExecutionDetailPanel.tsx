import { Button } from '@/components/ui/button'
import { PanelTemplate } from '@loykin/designkit'
import { useSidePanel } from '@loykin/side-panel'
import { Check, Square, X } from 'lucide-react'
import StatusBadge from '@/shared/components/StatusBadge'
import { useApproveExecution, useCancelExecution, useDenyExecution } from '../hooks'
import type { NotebookExecution } from '../types'

function date(value?: string) {
  return value ? new Date(value).toLocaleString() : '—'
}

export function ExecutionDetailPanel({ execution, canAdmin, canCancel }: { execution: NotebookExecution; canAdmin: boolean; canCancel: boolean }) {
  const { close } = useSidePanel()
  const approve = useApproveExecution()
  const deny = useDenyExecution()
  const cancel = useCancelExecution()
  const awaiting = execution.status === 'awaiting_approval'
  const active = ['queued', 'running'].includes(execution.status)

  return (
    <PanelTemplate
      eyebrow="Notebook execution"
      title={execution.id}
      status={<StatusBadge status={execution.status} />}
      actions={<div className="flex items-center gap-1">
        {awaiting && canAdmin && <Button size="sm" onClick={() => approve.mutate(execution)} disabled={approve.isPending}><Check />Approve</Button>}
        {awaiting && canAdmin && <Button size="sm" variant="destructive" onClick={() => deny.mutate(execution)} disabled={deny.isPending}><X />Deny</Button>}
        {active && canCancel && <Button size="sm" variant="outline" onClick={() => cancel.mutate(execution)} disabled={cancel.isPending}><Square />Cancel</Button>}
        <Button variant="ghost" size="icon-sm" onClick={() => void close()}><X /><span className="sr-only">Close</span></Button>
      </div>}
    >
      <PanelTemplate.Section title="Target">
        <dl className="space-y-2">
          <PanelTemplate.Row label="Notebook">{execution.notebook_name}</PanelTemplate.Row>
          <PanelTemplate.Row label="Path"><span className="break-all font-mono text-xs">{execution.notebook_path}</span></PanelTemplate.Row>
          <PanelTemplate.Row label="Result"><span className="break-all font-mono text-xs">{execution.result_path || '—'}</span></PanelTemplate.Row>
          <PanelTemplate.Row label="Kind">{execution.kind}</PanelTemplate.Row>
          <PanelTemplate.Row label="Progress">{execution.current_cell} / {execution.total_cells}</PanelTemplate.Row>
        </dl>
      </PanelTemplate.Section>
      <PanelTemplate.Section title="Audit">
        <dl className="space-y-2">
          <PanelTemplate.Row label="Requested by">{execution.requested_by || '—'}</PanelTemplate.Row>
          <PanelTemplate.Row label="Source">{execution.client_id || '—'}</PanelTemplate.Row>
          <PanelTemplate.Row label="Queued">{date(execution.queued_at)}</PanelTemplate.Row>
          <PanelTemplate.Row label="Started">{date(execution.started_at)}</PanelTemplate.Row>
          <PanelTemplate.Row label="Finished">{date(execution.finished_at)}</PanelTemplate.Row>
          <PanelTemplate.Row label="Approved by">{execution.approved_by || '—'} {execution.approved_at ? `· ${date(execution.approved_at)}` : ''}</PanelTemplate.Row>
          <PanelTemplate.Row label="Denied by">{execution.denied_by || '—'} {execution.denied_at ? `· ${date(execution.denied_at)}` : ''}</PanelTemplate.Row>
        </dl>
      </PanelTemplate.Section>
      {(execution.error_code || execution.error_message) && <PanelTemplate.Section title="Error">
        <p className="text-sm text-destructive">{execution.error_code || 'execution_error'}</p>
        <p className="mt-1 whitespace-pre-wrap text-xs text-muted-foreground">{execution.error_message}</p>
      </PanelTemplate.Section>}
      {execution.output_summary && <PanelTemplate.Section title="Output summary">
        <pre className="max-h-64 overflow-auto whitespace-pre-wrap break-all rounded-md bg-muted p-3 font-mono text-xs text-muted-foreground">{execution.output_summary}</pre>
      </PanelTemplate.Section>}
    </PanelTemplate>
  )
}
