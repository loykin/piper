import { useEffect } from 'react'

/**
 * @base-ui/react's Select never calls onValueChange when it has exactly one
 * item (confirmed with pure keyboard input too, so it isn't an
 * automation-click artifact — docs/qa/adversarial-qa-playbook.md §3c): the
 * trigger visually shows the sole candidate selected, but the field a form
 * relies on to read the selection never actually updates. With only one
 * candidate there is nothing to actually choose between anyway, so route
 * around the buggy interaction entirely by auto-selecting it.
 *
 * No-ops when there are zero or 2+ candidates, when `currentValue` is
 * already non-empty (so it never clobbers an existing value, e.g. editing a
 * record that already has one), or when `skip` is set.
 */
export function useAutoSelectSole<T>(
  candidates: T[] | undefined,
  currentValue: string,
  getValue: (item: T) => string,
  setValue: (value: string) => void,
  options?: { skip?: boolean },
): void {
  const sole = !options?.skip && !currentValue && (candidates?.length ?? 0) === 1
    ? getValue(candidates![0])
    : null

  useEffect(() => {
    if (sole) setValue(sole)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [sole])
}
