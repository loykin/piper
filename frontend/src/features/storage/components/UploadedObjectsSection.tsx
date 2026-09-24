import { Fragment, useState } from 'react'
import { Download, Folder, Plus, RefreshCw, Search, Trash2 } from 'lucide-react'
import {
  Breadcrumb,
  BreadcrumbItem,
  BreadcrumbLink,
  BreadcrumbList,
  BreadcrumbPage,
  BreadcrumbSeparator,
  DataBodyTemplate,
} from '@loykin/designkit'
import { DataGrid, DataGridPaginationBar, type DataGridColumnDef } from '@loykin/gridkit'
import { useSidePanel } from '@loykin/side-panel'
import { FilterInput } from '@loykin/filter-input'
import { Button } from '@/components/ui/button'
import { IconButton } from '@/components/ui/icon-button'
import { useStorageObjectsPaged, useDeleteObject } from '@/features/storage/hooks'
import { QueryErrorNotice } from '@/shared/components/QueryErrorNotice'
import { RowActions } from '@/shared/components/RowActions'
import { storageObjectURL, type StorageObjectInfo } from '@/features/storage/api'
import { fmtBytes, fmtDate } from '@/lib/format'
import { ObjectDetailPanel } from '@/features/storage/components/ObjectDetailPanel'
import { UploadObjectDialog } from './UploadObjectDialog'
import { ConfirmDialog } from '@/shared/components/ConfirmDialog'
import { useDeleteTarget } from '@/shared/hooks/useDeleteTarget'

// ── Uploaded Objects ────────────────────────────────────────────────────────
// Folder-tree browser over this project's uploads/ prefix, with delete as its
// only mutation. Per managed-table.md, list content lives in
// DataBodyTemplate.Resource (toolbar/notice/pagination), not Group — Group is
// reserved for the form-workflow save boundaries above (Config, Credentials).
//
// Listing is one level at a time (S3 ListObjectsV2 Delimiter="/" semantics —
// see storage.Store.List on the Go side), the same convention the AWS/MinIO
// consoles use: a folder row is a pseudo-directory aggregated server-side,
// not a real DataGrid nesting, and drilling in means re-querying with that
// folder's own key as the new prefix. Root Label is what breadcrumb segment
// 0 always reads.

const OBJECTS_PAGE_SIZE = 20
const ROOT_LABEL = 'uploads'

function breadcrumbSegments(prefix: string): string[] {
  return prefix.split('/').filter(Boolean)
}

export function UploadedObjectsSection({ projectId }: { projectId: string }) {
  const { open, close } = useSidePanel()
  const [appliedPrefix, setAppliedPrefix] = useState('')
  const [pageIndex, setPageIndex] = useState(0)
  const [nameFilter, setNameFilter] = useState('')
  const objectsQuery = useStorageObjectsPaged(OBJECTS_PAGE_SIZE, pageIndex * OBJECTS_PAGE_SIZE, appliedPrefix)
  const objects = objectsQuery.data?.items ?? []
  const total = objectsQuery.data?.total ?? 0
  // Filters only the current page/folder level — matches the accepted
  // trade-off in CredentialsPage's kind filter. A true cross-page name
  // search would need server-side support object storage doesn't offer.
  const filteredObjects = nameFilter.trim()
    ? objects.filter(o => o.key.slice(appliedPrefix.length).toLowerCase().includes(nameFilter.trim().toLowerCase()))
    : objects
  const deleteObject = useDeleteObject()
  const deleteTarget = useDeleteTarget<string>()
  const [uploadOpen, setUploadOpen] = useState(false)

  function navigateTo(nextPrefix: string) {
    setAppliedPrefix(nextPrefix)
    setPageIndex(0)
  }

  const segments = breadcrumbSegments(appliedPrefix)

  const objectColumns: DataGridColumnDef<StorageObjectInfo>[] = [
    {
      accessorKey: 'key',
      header: 'Name',
      meta: { minWidth: 240, flex: 1 },
      cell: ({ row }) => {
        const leaf = row.original.key.slice(appliedPrefix.length).replace(/\/$/, '')
        return row.original.is_dir ? (
          <span className="flex items-center gap-2 font-medium">
            <Folder className="size-4 shrink-0 text-muted-foreground" />
            {leaf}
          </span>
        ) : (
          <span className="block truncate font-mono text-xs" title={row.original.key}>
            {leaf}
          </span>
        )
      },
    },
    {
      accessorKey: 'size',
      header: 'Size',
      meta: { minWidth: 90 },
      cell: ({ row }) => (
        <span className="text-xs text-muted-foreground">
          {row.original.is_dir ? '—' : fmtBytes(row.original.size)}
        </span>
      ),
    },
    {
      accessorKey: 'modified_at',
      header: 'Modified',
      meta: { minWidth: 160 },
      cell: ({ row }) => (
        <span className="text-xs text-muted-foreground">
          {row.original.is_dir ? '—' : fmtDate(row.original.modified_at)}
        </span>
      ),
    },
    {
      id: 'actions',
      header: '',
      meta: { minWidth: 80, align: 'right' },
      cell: ({ row }) => row.original.is_dir ? null : (
        <RowActions>
          <IconButton
            icon={<Download />}
            label="Download"
            onClick={e => {
              e.stopPropagation()
              window.open(storageObjectURL(projectId, row.original.key), '_blank', 'noopener,noreferrer')
            }}
          />
          <IconButton
            icon={<Trash2 />}
            label="Delete"
            disabled={deleteObject.isPending && deleteObject.variables === row.original.key}
            onClick={e => { e.stopPropagation(); deleteTarget.requestDelete(row.original.key) }}
            className="text-destructive hover:bg-destructive/10"
          />
        </RowActions>
      ),
    },
  ]

  return (
    <>
      <div className="mb-3 flex items-center gap-2 text-sm font-medium text-foreground">
        <Folder className="size-4 shrink-0 text-muted-foreground" />
        <Breadcrumb>
          <BreadcrumbList>
            <BreadcrumbItem>
              {segments.length === 0 ? (
                <BreadcrumbPage>{ROOT_LABEL}</BreadcrumbPage>
              ) : (
                <BreadcrumbLink href="#" onClick={e => { e.preventDefault(); navigateTo('') }}>
                  {ROOT_LABEL}
                </BreadcrumbLink>
              )}
            </BreadcrumbItem>
            {segments.map((segment, i) => {
              const segmentPrefix = segments.slice(0, i + 1).join('/') + '/'
              const isLast = i === segments.length - 1
              return (
                <Fragment key={segmentPrefix}>
                  <BreadcrumbSeparator />
                  <BreadcrumbItem>
                    {isLast ? (
                      <BreadcrumbPage>{segment}</BreadcrumbPage>
                    ) : (
                      <BreadcrumbLink href="#" onClick={e => { e.preventDefault(); navigateTo(segmentPrefix) }}>
                        {segment}
                      </BreadcrumbLink>
                    )}
                  </BreadcrumbItem>
                </Fragment>
              )
            })}
          </BreadcrumbList>
        </Breadcrumb>
      </div>

      <DataBodyTemplate.Resource
        toolbarLeft={
          <div className="w-52">
            <FilterInput
              config={{
                key: 'objectSearch',
                type: 'text',
                placeholder: 'Search objects…',
                display: { size: 'sm', leadingIcon: <Search /> },
              }}
              value={nameFilter}
              onChange={v => setNameFilter(typeof v === 'string' ? v : '')}
            />
          </div>
        }
        toolbarRight={
          <>
            <Button
              variant="outline"
              size="sm"
              onClick={() => void objectsQuery.refetch()}
              disabled={objectsQuery.isFetching}
            >
              <RefreshCw className={objectsQuery.isFetching ? 'size-4 animate-spin' : 'size-4'} />
            </Button>
            <Button size="sm" onClick={() => setUploadOpen(true)}>
              <Plus className="mr-1.5 size-3.5" />
              Upload
            </Button>
          </>
        }
        notice={objectsQuery.isError && (
          <QueryErrorNotice
            message="Failed to load uploaded objects"
            error={objectsQuery.error}
            onRetry={() => void objectsQuery.refetch()}
          />
        )}
      >
        <DataGrid
          data={filteredObjects}
          columns={objectColumns}
          isLoading={objectsQuery.isPending}
          emptyMessage={objectsQuery.isError ? undefined : (segments.length > 0 ? 'This folder is empty.' : 'No uploaded objects yet.')}
          tableWidthMode="fill-last"
          rowHeight={44}
          rowCursor
          onRowClick={(object) => {
            if (object.is_dir) { navigateTo(object.key); return }
            open(
              <ObjectDetailPanel
                projectId={projectId}
                object={object}
                onDelete={o => deleteTarget.requestDelete(o.key)}
              />,
              { size: 480 },
            )
          }}
          classNames={{ footer: 'pt-3' }}
          pagination={{
            pageSize: OBJECTS_PAGE_SIZE,
            pageIndex,
            pageCount: Math.max(1, Math.ceil(total / OBJECTS_PAGE_SIZE)),
            onPageChange: setPageIndex,
          }}
          footer={(table) => <DataGridPaginationBar table={table} totalCount={total} />}
        />
      </DataBodyTemplate.Resource>

      <UploadObjectDialog open={uploadOpen} onOpenChange={setUploadOpen} />

      <ConfirmDialog
        open={deleteTarget.open}
        onCancel={deleteTarget.cancel}
        verb="Delete"
        noun="object"
        description={`"${deleteTarget.target}" will be permanently deleted from the object store.`}
        error={deleteTarget.error}
        pending={deleteObject.isPending}
        onConfirm={() => void deleteTarget.confirm(async key => {
          await deleteObject.mutateAsync(key)
          void close()
        })}
      />
    </>
  )
}
