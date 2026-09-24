import { Button } from '@/components/ui/button'
import { PanelTemplate } from '@loykin/designkit'
import { Check, Square, X } from 'lucide-react'
import StatusBadge from '@/shared/components/StatusBadge'
import { useApproveExecution, useCancelExecution, useDenyExecution, useExecution } from '../hooks'
import { fmtDate } from '@/lib/format'
import { PanelCloseButton, PanelPlaceholder } from '@/shared/components/PanelPlaceholder'
import { ConfirmDialog } from '@/shared/components/ConfirmDialog'
import { MutationErrors } from '@/shared/components/MutationErrors'
import { hasMutationError } from '@/shared/mutationErrors'
import { useConfirmAction } from '@/shared/hooks/useConfirmAction'

function date(value?: string) {
  return value ? fmtDate(value) : '—'
}

export function ExecutionDetailPanel({ id, canAdmin, canCancel, actorNames }: {
  id: string
  canAdmin: boolean
  /** Whether this viewer may cancel `requestedBy`'s execution. */
  canCancel: (requestedBy: string | undefined) => boolean
  actorNames: ReadonlyMap<string, string>
}) {
  const approve = useApproveExecution()
  const deny = useDenyExecution()
  const cancel = useCancelExecution()
  const cancelConfirmation = useConfirmAction<'cancel'>()
  // Polls while the execution is still running — see useExecution.
  const query = useExecution(id)
  const execution = query.data
  if (!execution) return <PanelPlaceholder query={query} noun="execution" />
  // Prefer the server-resolved username (works regardless of the viewer's
  // own privileges) and fall back to the project-member map, then the raw ID.
  const actorName = (id?: string, username?: string) => username || (id ? actorNames.get(id) ?? id : '—')
  const requestedBy = actorName(execution.requested_by, execution.requested_by_username)
  const approvedBy = actorName(execution.approved_by, execution.approved_by_username)
  const deniedBy = actorName(execution.denied_by, execution.denied_by_username)
  const awaiting = execution.status === 'awaiting_approval'
  const active = ['queued', 'running'].includes(execution.status)

  return (
    <>
    <PanelTemplate
      eyebrow="Notebook Execution"
      title={execution.id}
      status={<StatusBadge status={execution.status} />}
      actions={<div className="flex items-center gap-1">
        {awaiting && canAdmin && <Button size="sm" onClick={() => approve.mutate(execution)} disabled={approve.isPending}><Check />Approve</Button>}
        {awaiting && canAdmin && <Button size="sm" variant="destructive" onClick={() => deny.mutate(execution)} disabled={deny.isPending}><X />Deny</Button>}
        {active && canCancel(execution.requested_by) && <Button size="sm" variant="outline" onClick={() => cancelConfirmation.requestAction('cancel')}><Square />Cancel</Button>}
        <PanelCloseButton />
      </div>}
    >
      {hasMutationError([approve, deny]) && (
        <PanelTemplate.Section>
          <MutationErrors of={[approve, deny]} />
        </PanelTemplate.Section>
      )}
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
          <PanelTemplate.Row label="Requested by">{requestedBy}</PanelTemplate.Row>
          <PanelTemplate.Row label="Source">{execution.client_id || '—'}</PanelTemplate.Row>
          <PanelTemplate.Row label="Queued">{date(execution.queued_at)}</PanelTemplate.Row>
          <PanelTemplate.Row label="Started">{date(execution.started_at)}</PanelTemplate.Row>
          <PanelTemplate.Row label="Finished">{date(execution.finished_at)}</PanelTemplate.Row>
          <PanelTemplate.Row label="Approved by">{execution.approved_by ? approvedBy : '—'} {execution.approved_at ? `· ${date(execution.approved_at)}` : ''}</PanelTemplate.Row>
          <PanelTemplate.Row label="Denied by">{execution.denied_by ? deniedBy : '—'} {execution.denied_at ? `· ${date(execution.denied_at)}` : ''}</PanelTemplate.Row>
        </dl>
      </PanelTemplate.Section>
      {(execution.error_code || execution.error_message) && <PanelTemplate.Section title="Error">
        <p className="text-sm text-destructive">{execution.error_code || 'execution_error'}</p>
        <p className="mt-1 whitespace-pre-wrap text-xs text-muted-foreground">{execution.error_message}</p>
      </PanelTemplate.Section>}
      {execution.output_summary && <PanelTemplate.Section title="Output Summary">
        <pre className="max-h-64 overflow-auto whitespace-pre-wrap break-all rounded-md bg-muted p-3 font-mono text-xs text-muted-foreground">{execution.output_summary}</pre>
      </PanelTemplate.Section>}
    </PanelTemplate>
    <ConfirmDialog
      open={cancelConfirmation.open}
      onCancel={cancelConfirmation.cancel}
      verb="Cancel"
      noun="execution"
      description={`Execution ${execution.id} of "${execution.notebook_name}" will be stopped.`}
      error={cancelConfirmation.error}
      pending={cancel.isPending}
      onConfirm={() => void cancelConfirmation.confirm(async () => { await cancel.mutateAsync(execution) })}
    />
    </>
  )
}
