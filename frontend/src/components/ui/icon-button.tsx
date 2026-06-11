import type { ReactNode } from 'react'
import { Button } from './button'
import { Tooltip, TooltipContent, TooltipTrigger } from './tooltip'

interface IconButtonProps {
  icon: ReactNode
  label: string
  onClick?: (e: React.MouseEvent<HTMLButtonElement>) => void
  disabled?: boolean
  className?: string
  variant?: 'ghost' | 'destructive'
}

export function IconButton({ icon, label, onClick, disabled, className, variant = 'ghost' }: IconButtonProps) {
  return (
    <Tooltip>
      <TooltipTrigger
        render={
          <Button
          aria-label={label}
          variant={variant}
          size="icon-sm"
          disabled={disabled}
          onClick={onClick}
          className={className}
          />
        }
      >
        {icon}
      </TooltipTrigger>
      <TooltipContent>{label}</TooltipContent>
    </Tooltip>
  )
}
