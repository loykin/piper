// Re-export of @loykin/designkit's alert-dialog so the app has one copy of each
// primitive (a local fork drifted from DesignKit). Import from here, not from
// '@loykin/designkit' directly: AlertDialogContent adds the side-panel marker.
import type { ComponentProps } from 'react'
import { AlertDialogContent as DesignKitAlertDialogContent } from '@loykin/designkit'

export { AlertDialog, AlertDialogAction, AlertDialogCancel, AlertDialogDescription, AlertDialogFooter, AlertDialogHeader, AlertDialogTitle } from '@loykin/designkit'

// @loykin/side-panel only recognizes Radix's data-state="open" when deciding
// a click inside a dialog isn't an outside click; Base UI sets data-open
// instead. Without this, confirming a dialog opened from a side panel closes
// the panel first and the action never runs. Remove once fixed upstream:
// basekit packages/side-panel/ISSUES.md.
export function AlertDialogContent(props: ComponentProps<typeof DesignKitAlertDialogContent>) {
  return <DesignKitAlertDialogContent data-state="open" {...props} />
}
