import CodeMirror from '@uiw/react-codemirror'
import { yaml } from '@codemirror/lang-yaml'
import { oneDark } from '@codemirror/theme-one-dark'
import { cn } from '@/lib/utils'
import type { ChangeEvent } from 'react'

interface YamlMirrorProps {
  value: string
  onChange?: (e: ChangeEvent<HTMLTextAreaElement>) => void
  className?: string
  readOnly?: boolean
  rows?: number
}

function YamlMirror({ value, onChange, className, readOnly, rows: _ }: YamlMirrorProps) {
  return (
    <div className={cn('overflow-hidden rounded-lg border border-border', className)}>
      <CodeMirror
        value={value}
        height="100%"
        minHeight="34rem"
        theme={oneDark}
        extensions={[yaml()]}
        readOnly={readOnly}
        basicSetup={{
          lineNumbers: true,
          foldGutter: true,
          highlightActiveLine: true,
          autocompletion: true,
        }}
        onChange={val => {
          if (!onChange) return
          const syntheticEvent = { target: { value: val } } as ChangeEvent<HTMLTextAreaElement>
          onChange(syntheticEvent)
        }}
        style={{ fontSize: 13 }}
      />
    </div>
  )
}

export { YamlMirror }
