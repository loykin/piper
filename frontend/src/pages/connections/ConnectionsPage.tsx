import { useCallback, useMemo, useState } from 'react'
import { useNavigate } from '@tanstack/react-router'
import { DataBodyTemplate } from '@loykin/designkit'
import { DataGrid, type DataGridColumnDef } from '@loykin/gridkit'
import { FlaskConical, Plus, Power, RotateCw, Trash2 } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { IconButton } from '@/components/ui/icon-button'
import { connectionColumns } from '@/features/connections/columns'
import {
  useConnections,
  useDeleteConnection,
  usePatchConnection,
  useTestConnection,
} from '@/features/connections/hooks'
import type { Connection } from '@/features/connections/types'
import { useProjectId } from '@/lib/projectContext'
import RotateConnectionDialog from './RotateConnectionDialog'
import TestConnectionDialog from './TestConnectionDialog'

export default function ConnectionsPage() {
  const projectId = useProjectId()
  const navigate = useNavigate()
  const { data = [], isLoading } = useConnections()
  const patchConnection = usePatchConnection()
  const deleteConnection = useDeleteConnection()
  const testConnection = useTestConnection()

  const [rotateTarget, setRotateTarget] = useState<Connection | null>(null)
  const [testTarget, setTestTarget] = useState<Connection | null>(null)
  const [actionError, setActionError] = useState('')

  const toggleConnection = useCallback(async (conn: Connection) => {
    setActionError('')
    const enabled = conn.disabled
    if (!confirm(`${enabled ? 'Enable' : 'Disable'} connection "${conn.name}"?`)) return
    try {
      await patchConnection.mutateAsync({ name: conn.name, patch: { enabled } })
    } catch (err) {
      setActionError(err instanceof Error ? err.message : String(err))
    }
  }, [patchConnection])

  const handleDelete = useCallback(async (conn: Connection) => {
    if (!confirm(`Delete connection "${conn.name}"? This cannot be undone.`)) return
    try {
      await deleteConnection.mutateAsync(conn.name)
    } catch (err) {
      setActionError(err instanceof Error ? err.message : String(err))
    }
  }, [deleteConnection])

  const columns = useMemo<DataGridColumnDef<Connection>[]>(() => [
    ...connectionColumns,
    {
      id: 'actions',
      header: '',
      meta: { minWidth: 160, align: 'right' },
      cell: ({ row }) => (
        <div className="flex justify-end gap-1">
          <IconButton
            icon={<FlaskConical />}
            label="Test"
            onClick={e => { e.stopPropagation(); setTestTarget(row.original) }}
            disabled={row.original.disabled}
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
            onClick={e => { e.stopPropagation(); void toggleConnection(row.original) }}
            className={row.original.disabled ? 'text-primary hover:bg-primary/10' : 'text-muted-foreground hover:bg-muted'}
          />
          <IconButton
            icon={<Trash2 />}
            label="Delete"
            onClick={e => { e.stopPropagation(); void handleDelete(row.original) }}
            className="text-destructive hover:bg-destructive/10"
          />
        </div>
      ),
    },
  ], [toggleConnection, handleDelete])

  return (
    <DataBodyTemplate
      title="Connections"
      description="Infrastructure credentials for git repositories and container registries. Referenced by pipeline runs."
      actions={
        <Button size="sm" onClick={() => void navigate({ to: `/projects/${projectId}/connections/new` })}>
          <Plus className="mr-2 size-4" />
          New Connection
        </Button>
      }
    >
      <DataBodyTemplate.Body>
        {actionError && <p className="mb-3 text-sm text-destructive">{actionError}</p>}
        <DataGrid
          data={data}
          columns={columns}
          isLoading={isLoading}
          emptyMessage="No connections configured."
        />
      </DataBodyTemplate.Body>

      <RotateConnectionDialog
        target={rotateTarget}
        onClose={() => setRotateTarget(null)}
      />
      <TestConnectionDialog
        target={testTarget}
        testConnection={testConnection}
        onClose={() => setTestTarget(null)}
      />
    </DataBodyTemplate>
  )
}
