import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'

interface FileBrowseDropdownProps {
  files: string[]
  ext?: string
  query: string
  onQueryChange: (q: string) => void
  onSelect: (file: string) => void
}

export function FileBrowseDropdown({ files, ext, query, onQueryChange, onSelect }: FileBrowseDropdownProps) {
  const q = query.toLowerCase()
  const filtered = files.filter(f => f.toLowerCase().includes(q))
  return (
    <div className="absolute left-0 right-0 top-full z-50 mt-1 overflow-hidden rounded-lg border border-border bg-card shadow-lg">
      <div className="border-b border-border px-2 py-2">
        <Input
          autoFocus
          className="h-7 text-xs"
          placeholder={ext ? `Search ${ext} files…` : 'Search files…'}
          value={query}
          onChange={e => onQueryChange(e.target.value)}
        />
      </div>
      {filtered.length === 0 ? (
        <p className="px-3 py-3 text-xs text-muted-foreground">No files found.</p>
      ) : (
        <div className="max-h-48 overflow-y-auto">
          {filtered.map(f => (
            <Button
              key={f}
              type="button"
              variant="ghost"
              size="sm"
              className="w-full justify-start rounded-none px-3 font-normal"
              onClick={() => onSelect(f)}
            >
              {ext && <span className="font-mono text-muted-foreground">{ext}</span>}
              <span className="truncate font-mono">{f}</span>
            </Button>
          ))}
        </div>
      )}
    </div>
  )
}

interface DepBrowseDropdownProps {
  files: string[]
  query: string
  onQueryChange: (q: string) => void
  onSelect: (path: string) => void
}

export function DepBrowseDropdown({ files, query, onQueryChange, onSelect }: DepBrowseDropdownProps) {
  const dirs = [...new Set(
    files.flatMap(f => {
      const parts = f.split('/')
      const result: string[] = []
      for (let i = 1; i < parts.length; i++) {
        result.push(parts.slice(0, i).join('/') + '/')
      }
      return result
    })
  )].sort()

  const q = query.toLowerCase()
  const filteredDirs = dirs.filter(d => d.toLowerCase().includes(q))
  const filteredFiles = files.filter(f => f.toLowerCase().includes(q))
  const empty = filteredDirs.length === 0 && filteredFiles.length === 0

  return (
    <div className="absolute left-0 right-0 top-full z-50 mt-1 overflow-hidden rounded-lg border border-border bg-card shadow-lg">
      <div className="border-b border-border px-2 py-2">
        <Input
          autoFocus
          className="h-7 text-xs"
          placeholder="Search files and directories…"
          value={query}
          onChange={e => onQueryChange(e.target.value)}
        />
      </div>
      {empty ? (
        <p className="px-3 py-3 text-xs text-muted-foreground">No results.</p>
      ) : (
        <div className="max-h-52 overflow-y-auto">
          {filteredDirs.length > 0 && (
            <>
              <p className="px-3 pt-2 text-[10px] uppercase tracking-wider text-muted-foreground">Directories</p>
              {filteredDirs.map(d => (
                <Button key={d} type="button" variant="ghost" size="sm"
                  className="w-full justify-start rounded-none px-3 font-normal"
                  onClick={() => onSelect(d)}
                >
                  <span className="shrink-0 text-muted-foreground">📁</span>
                  <span className="truncate font-mono">{d}</span>
                </Button>
              ))}
            </>
          )}
          {filteredFiles.length > 0 && (
            <>
              <p className="px-3 pt-2 text-[10px] uppercase tracking-wider text-muted-foreground">Files</p>
              {filteredFiles.map(f => (
                <Button key={f} type="button" variant="ghost" size="sm"
                  className="w-full justify-start rounded-none px-3 font-normal"
                  onClick={() => onSelect(f)}
                >
                  <span className="shrink-0 text-muted-foreground">📄</span>
                  <span className="truncate font-mono">{f}</span>
                </Button>
              ))}
            </>
          )}
        </div>
      )}
    </div>
  )
}
