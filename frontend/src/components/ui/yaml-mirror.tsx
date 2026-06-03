import * as React from 'react'

import { cn } from '@/lib/utils'

type YamlMirrorProps = React.TextareaHTMLAttributes<HTMLTextAreaElement>

function YamlMirror({ className, spellCheck = false, ...props }: YamlMirrorProps) {
  return (
    <textarea
      className={cn(
        'w-full resize-none rounded-lg border border-border bg-card px-3 py-2 font-mono text-sm leading-5 text-foreground focus:border-primary focus:outline-none',
        className,
      )}
      spellCheck={spellCheck}
      {...props}
    />
  )
}

export { YamlMirror }
