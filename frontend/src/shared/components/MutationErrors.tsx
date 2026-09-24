import { errorMessage } from '@/lib/format'

import type { Mutation } from '@/shared/mutationErrors'

/**
 * The failure message of each mutation that runs without a confirmation
 * dialog (start/stop, toggle, rerun, …). Put it in the page's `notice` or at
 * the top of a panel so a failed action is never silent; it clears itself
 * when the mutation next succeeds.
 */
export function MutationErrors({ of }: { of: Mutation[] }) {
  const failed = of.filter(m => m.isError)
  if (failed.length === 0) return null
  return (
    <>
      {failed.map((m, i) => (
        <p key={i} className="text-sm text-destructive">{errorMessage(m.error)}</p>
      ))}
    </>
  )
}
