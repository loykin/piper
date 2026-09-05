import { useCallback, useMemo, useState } from 'react'
import { useNavigate } from '@tanstack/react-router'
import { DataBodyTemplate, Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@loykin/designkit'
import { DataGrid, DataGridPaginationBar, type DataGridColumnDef } from '@loykin/gridkit'
import { SidePanelProvider, useSidePanel } from '@loykin/side-panel'
import { FilterInput } from '@loykin/filter-input'
import { FlaskConical, Plus, Power, RotateCw, Search, Trash2 } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { IconButton } from '@/components/ui/icon-button'
import { credentialColumns } from '@/features/credentials/columns'
import {
  useCredentialsPaged,
  useDeleteCredential,
  usePatchCredential,
  useRotateCredential,
  useTestCredential,
} from '@/features/credentials/hooks'
import type { Credential, CredentialKind } from '@/features/credentials/types'
import { useProjectId } from '@/lib/projectContext'
import RotateCredentialDialog from './RotateCredentialDialog'
import TestCredentialDialog from './TestCredentialDialog'
import { QueryErrorNotice } from '@/shared/components/QueryErrorNotice'
import { RowActions } from '@/shared/components/RowActions'
import { CredentialDetailPanel } from '@/features/credentials/components/CredentialDetailPanel'

const PAGE_SIZE = 20

type KindFilter = 'all' | CredentialKind
type PendingAction =
  | { type: 'toggle'; credential: Credential }
  | { type: 'delete'; credential: Credential }

function CredentialsPageInner() {
  const { open } = useSidePanel()
  const projectId = useProjectId()
  const navigate = useNavigate()
  const [pageIndex, setPageIndex] = useState(0)
  const credentialsQuery = useCredentialsPaged(PAGE_SIZE, pageIndex * PAGE_SIZE)
  const data = useMemo(() => credentialsQuery.data?.credentials ?? [], [credentialsQuery.data])
  const total = credentialsQuery.data?.total ?? 0
  const patchCredential = usePatchCredential()
  const rotateCredential = useRotateCredential()
  const deleteCredential = useDeleteCredential()
  const testCredential = useTestCredential()

  const [kindFilter, setKindFilter] = useState<KindFilter>('all')
  const [nameFilter, setNameFilter] = useState('')
  const [rotateTarget, setRotateTarget] = useState<Credential | null>(null)
  const [testTarget, setTestTarget] = useState<Credential | null>(null)
  const [pendingAction, setPendingAction] = useState<PendingAction | null>(null)
  const [actionError, setActionError] = useState('')

  // Filters only the current page — neither the kind nor name filter is
  // server-side yet, so switching them can show fewer rows than this page's
  // total until paging to where matching rows land.
  const filtered = useMemo(() => {
    let rows = kindFilter === 'all' ? data : data.filter(item => item.kind === kindFilter)
    if (nameFilter.trim()) {
      const q = nameFilter.trim().toLowerCase()
      rows = rows.filter(item => item.name.toLowerCase().includes(q))
    }
    return rows
  }, [data, kindFilter, nameFilter])

  const runPendingAction = useCallback(async () => {
    if (!pendingAction) return
    setActionError('')
    try {
      if (pendingAction.type === 'toggle') {
        await patchCredential.mutateAsync({
          name: pendingAction.credential.name,
          patch: { enabled: pendingAction.credential.disabled },
        })
      } else {
        await deleteCredential.mutateAsync(pendingAction.credential.name)
      }
      setPendingAction(null)
    } catch (err) {
      setActionError(err instanceof Error ? err.message : String(err))
    }
  }, [deleteCredential, patchCredential, pendingAction])

  const columns = useMemo<DataGridColumnDef<Credential>[]>(() => [
    ...credentialColumns,
    {
      id: 'actions',
      header: '',
      meta: { minWidth: 190, align: 'right' },
      cell: ({ row }) => (
        <RowActions>
          <IconButton
            icon={<FlaskConical />}
            label="Test"
            onClick={e => { e.stopPropagation(); setTestTarget(row.original) }}
            disabled={row.original.disabled || !['git', 'slack', 'webhook'].includes(row.original.kind)}
          />
          <IconButton
            icon={<RotateCw />}
            label="Rotate"
            onClick={e => { e.stopPropagation(); setRotateTarget(row.original) }}
            disabled={row.original.disabled}
          />
          <IconButton
            icon={<Power />}
            label={row.original.disabled ? 'Enable' : 'Disable'}
            onClick={e => { e.stopPropagation(); setPendingAction({ type: 'toggle', credential: row.original }) }}
            className={row.original.disabled ? 'text-primary hover:bg-primary/10' : 'text-muted-foreground hover:bg-muted'}
          />
          <IconButton
            icon={<Trash2 />}
            label="Delete"
            onClick={e => { e.stopPropagation(); setPendingAction({ type: 'delete', credential: row.original }) }}
            className="text-destructive hover:bg-destructive/10"
          />
        </RowActions>
      ),
    },
  ], [])

  const pendingTitle = pendingAction
    ? pendingAction.type === 'delete'
      ? `Delete ${pendingAction.credential.name}`
      : `${pendingAction.credential.disabled ? 'Enable' : 'Disable'} ${pendingAction.credential.name}`
    : ''
  const pendingDescription = pendingAction
    ? pendingAction.type === 'delete'
      ? 'This removes the credential and its encrypted values.'
      : `This ${pendingAction.credential.disabled ? 'enables' : 'disables'} credential use for future resolutions.`
    : ''

  return (
    <>
    <DataBodyTemplate
      title="Credentials"
      description="Project-scoped credentials for workload env and Git source access."
    >
      <DataBodyTemplate.Body>
        <DataBodyTemplate.Resource
          toolbarLeft={
            <>
              <div className="w-48">
                <FilterInput
                  config={{
                    key: 'credentialSearch',
                    type: 'text',
                    placeholder: 'Search credentials…',
                    display: { size: 'sm', leadingIcon: <Search /> },
                  }}
                  value={nameFilter}
                  onChange={v => setNameFilter(typeof v === 'string' ? v : '')}
                />
              </div>
              <Select
                items={[
                  { value: 'all', label: 'All kinds' },
                  { value: 'generic', label: 'Generic' },
                  { value: 'git', label: 'Git' },
                  { value: 'slack', label: 'Slack' },
                  { value: 'webhook', label: 'Webhook' },
                ]}
                value={kindFilter}
                onValueChange={value => setKindFilter((value ?? 'all') as KindFilter)}
              >
                <SelectTrigger size="sm" className="w-36">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="all">All kinds</SelectItem>
                  <SelectItem value="generic">Generic</SelectItem>
                  <SelectItem value="git">Git</SelectItem>
                  <SelectItem value="slack">Slack</SelectItem>
                  <SelectItem value="webhook">Webhook</SelectItem>
                </SelectContent>
              </Select>
            </>
          }
          toolbarRight={
            <Button size="sm" onClick={() => void navigate({ to: `/projects/${projectId}/credentials/new` })}>
              <Plus className="mr-2 size-4" />
              New Credential
            </Button>
          }
          notice={(credentialsQuery.isError || actionError) && (
            <>
              {credentialsQuery.isError && (
                <QueryErrorNotice
                  message="Failed to load credentials"
                  error={credentialsQuery.error}
                  onRetry={() => void credentialsQuery.refetch()}
                />
              )}
              {actionError && <p className="text-sm text-destructive">{actionError}</p>}
            </>
          )}
        >
          <DataGrid
            data={filtered}
            columns={columns}
            isLoading={credentialsQuery.isLoading}
            emptyMessage={credentialsQuery.isError ? undefined : 'No credentials configured.'}
            tableWidthMode="fill-last"
            rowCursor
            onRowClick={(credential) => open(
              <CredentialDetailPanel
                credential={credential}
                onTest={setTestTarget}
                onRotate={setRotateTarget}
                onToggle={c => setPendingAction({ type: 'toggle', credential: c })}
                onDelete={c => setPendingAction({ type: 'delete', credential: c })}
              />,
              { size: 480 },
            )}
            classNames={{ footer: 'pt-3' }}
            pagination={{
              pageSize: PAGE_SIZE,
              pageIndex,
              pageCount: Math.max(1, Math.ceil(total / PAGE_SIZE)),
              onPageChange: setPageIndex,
            }}
            footer={(table) => <DataGridPaginationBar table={table} totalCount={total} />}
          />
        </DataBodyTemplate.Resource>
      </DataBodyTemplate.Body>
    </DataBodyTemplate>
    {/*
      RotateCredentialDialog/TestCredentialDialog/the confirm Dialog below must
      render as siblings OUTSIDE <DataBodyTemplate>, not as children alongside
      <DataBodyTemplate.Body> — DataBodyTemplate only mounts children that are
      its own recognized sub-components (.Body/.Tab/.Group/...); a plain
      element placed directly beside .Body is silently dropped from the
      rendered tree even though the React state driving it updates normally.
    */}
    <RotateCredentialDialog
      key={rotateTarget?.name ?? 'rotate-credential'}
      target={rotateTarget}
      rotateCredential={rotateCredential}
      onClose={() => setRotateTarget(null)}
    />
    <TestCredentialDialog
      target={testTarget}
      testCredential={testCredential}
      onClose={() => setTestTarget(null)}
    />
    <Dialog open={!!pendingAction} onOpenChange={open => { if (!open) setPendingAction(null) }}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{pendingTitle}</DialogTitle>
          <DialogDescription>{pendingDescription}</DialogDescription>
        </DialogHeader>
        <DialogFooter>
          <Button variant="outline" onClick={() => setPendingAction(null)}>
            Cancel
          </Button>
          <Button
            variant={pendingAction?.type === 'delete' ? 'destructive' : 'default'}
            onClick={() => void runPendingAction()}
            disabled={deleteCredential.isPending || patchCredential.isPending}
          >
            {pendingAction?.type === 'delete' ? 'Delete' : pendingAction?.credential.disabled ? 'Enable' : 'Disable'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
    </>
  )
}

export default function CredentialsPage() {
  return (
    <SidePanelProvider defaultSize={480} defaultMinSize={380} defaultMaxSize={800}>
      <CredentialsPageInner />
    </SidePanelProvider>
  )
}
