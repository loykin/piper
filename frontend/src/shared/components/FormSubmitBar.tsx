import { FormActions } from '@loykin/designkit'
import { actionLabel, type SubmitVerb } from '@/lib/copy'

/**
 * The bottom row of every form: the server error (in destructive color —
 * FormActions' own `status` is muted text, which made failures look like a
 * neutral note), Cancel, and the submit button whose label follows
 * `actionLabel(verb, noun, pending)`.
 */
export function FormSubmitBar({ verb, noun, pending, error, disabled, onCancel }: {
  verb: SubmitVerb
  noun: string
  pending: boolean
  error?: string
  /** Extra reasons to block submit; `pending` always blocks it. */
  disabled?: boolean
  /** Omit when the form has nowhere to go back to. */
  onCancel?: () => void
}) {
  return (
    <FormActions
      status={error ? <span className="text-destructive">{error}</span> : undefined}
      submitLabel={actionLabel(verb, noun, pending)}
      submitDisabled={pending || disabled}
      onCancel={onCancel}
    />
  )
}
