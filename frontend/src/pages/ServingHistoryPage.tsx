import { useEffect, useState } from 'react'
import { DataGrid, DataGridPaginationCompact } from '@loykin/gridkit'
import { DataBodyTemplate } from '@loykin/designkit'
import { listServingHistory, type ServiceHistory } from '@/features/serving/api'
import { useProjectId } from '@/lib/projectContext'
import { serviceHistoryColumns } from '@/features/serving/columns'

export default function ServingHistoryPage() {
  const projectId = useProjectId()
  const [history, setHistory] = useState<ServiceHistory[]>([])
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    if (!projectId) return
    listServingHistory(projectId)
      .then(setHistory)
      .catch(() => {})
      .finally(() => setLoading(false))
  }, [projectId])

  return (
    <DataBodyTemplate
      title="History"
      description="Past ModelService deployments."
    >
      <DataBodyTemplate.Body>
        {loading ? (
          <div className="py-8 text-sm text-muted-foreground">Loading…</div>
        ) : history.length === 0 ? (
          <div className="py-8 text-sm text-muted-foreground">No deployment history yet.</div>
        ) : (
          <DataGrid
            data={history}
            columns={serviceHistoryColumns}
            tableWidthMode="fill-last"
            rowHeight={44}
            pagination={{ pageSize: 20 }}
            footer={(table) => (
              <div className="flex h-9 items-center justify-between px-1 text-xs text-muted-foreground">
                <span>{history.length} records</span>
                <DataGridPaginationCompact table={table} />
              </div>
            )}
          />
        )}
      </DataBodyTemplate.Body>
    </DataBodyTemplate>
  )
}
