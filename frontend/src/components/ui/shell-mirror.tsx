import CodeMirror from '@uiw/react-codemirror'
import { StreamLanguage } from '@codemirror/language'
import { shell } from '@codemirror/legacy-modes/mode/shell'
import { oneDark } from '@codemirror/theme-one-dark'
import { cn } from '@/lib/utils'
import type { ChangeEvent } from 'react'

const shellLang = StreamLanguage.define(shell)

interface ShellMirrorProps {
  value: string
  onChange?: (e: ChangeEvent<HTMLTextAreaElement>) => void
  className?: string
  minHeight?: string
  placeholder?: string
}

function ShellMirror({ value, onChange, className, minHeight = '6rem', placeholder }: ShellMirrorProps) {
  return (
    <div className={cn('overflow-hidden rounded-lg border border-border', className)}>
      <CodeMirror
        value={value}
        height="100%"
        minHeight={minHeight}
        theme={oneDark}
        extensions={[shellLang]}
        placeholder={placeholder}
        basicSetup={{
          lineNumbers: true,
          foldGutter: false,
          highlightActiveLine: true,
          autocompletion: false,
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

export { ShellMirror }
