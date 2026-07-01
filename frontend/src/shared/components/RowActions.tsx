import type { ReactNode } from 'react'
import { cn } from '@/lib/utils'

interface RowActionsProps {
  children: ReactNode
  className?: string
}

export function RowActions({ children, className }: RowActionsProps) {
  return (
    <div
      className={cn('flex items-center justify-end gap-0.5', className)}
      onPointerDown={event => event.stopPropagation()}
      onMouseDown={event => event.stopPropagation()}
      onClick={event => event.stopPropagation()}
    >
      {children}
    </div>
  )
}
