/**
 * The one place semantic status colors are defined. Pages and features pick a
 * tone (via `statusTone` for a backend status string, or directly for a UI
 * meaning like "this action cancels something") and use the class sets below
 * — they never spell out a Tailwind palette class themselves (enforced by the
 * `no-restricted-syntax` palette rule in eslint.config.js). Every text tone
 * defines both a light and a dark shade, since a `-300`/`-400` shade alone is
 * illegible on the light theme's white background.
 */

export type Tone =
  | 'success'
  | 'info'
  | 'danger'
  | 'warning'
  | 'attention'
  | 'neutral'
  | 'scheduled'
  | 'starting'
  | 'accent'

// Every status string the backend sends (runs, steps, notebooks, services,
// executions, volumes, integrations, kernels, federation members). An
// unlisted status renders neutral grey, so `status.test.ts` fails when one
// the UI shows is missing here.
const STATUS_TONE: Record<string, Tone> = {
  // finished well
  success: 'success',
  succeeded: 'success',
  done: 'success',
  ready: 'success',
  healthy: 'success',
  enabled: 'success',
  online: 'success',
  synced: 'success',
  delivered: 'success',
  // in progress
  running: 'info',
  busy: 'info',
  bound: 'info',
  syncing: 'info',
  delivering: 'info',
  // failed
  failed: 'danger',
  timed_out: 'danger',
  conflicted: 'danger',
  dead: 'danger',
  unhealthy: 'danger',
  offline: 'danger',
  // needs attention
  skipped: 'warning',
  degraded: 'warning',
  awaiting_approval: 'warning',
  retrying: 'warning',
  canceled: 'attention',
  cancelled: 'attention',
  cancelling: 'attention',
  stopping: 'attention',
  recovering: 'attention',
  // waiting / at rest
  pending: 'neutral',
  queued: 'neutral',
  stopped: 'neutral',
  released: 'neutral',
  disabled: 'neutral',
  idle: 'neutral',
  closed: 'neutral',
  scheduled: 'scheduled',
  provisioning: 'scheduled',
  starting: 'starting',
  restarting: 'starting',
}

/** Every status the table above knows — for the coverage test. */
export const KNOWN_STATUSES = Object.keys(STATUS_TONE)

/** Maps a backend run/step/notebook/service status to its tone. */
export function statusTone(status: string): Tone {
  return STATUS_TONE[status] ?? 'neutral'
}

/** Statuses that are still changing and get a pulsing indicator. */
export const LIVE_STATUSES = new Set(['running', 'provisioning', 'starting', 'stopping', 'restarting', 'cancelling', 'syncing', 'delivering', 'retrying', 'recovering'])

/** Foreground text. */
export const toneText: Record<Tone, string> = {
  success: 'text-green-700 dark:text-green-400',
  info: 'text-blue-700 dark:text-blue-400',
  danger: 'text-red-700 dark:text-red-400',
  warning: 'text-yellow-700 dark:text-yellow-400',
  attention: 'text-orange-700 dark:text-orange-400',
  neutral: 'text-muted-foreground',
  scheduled: 'text-violet-700 dark:text-violet-400',
  starting: 'text-sky-700 dark:text-sky-400',
  accent: 'text-indigo-700 dark:text-indigo-400',
}

/** Pill badge: tinted background, readable text, matching border. */
export const toneBadge: Record<Tone, string> = {
  success: 'bg-green-500/20 text-green-700 dark:text-green-300 border-green-500/30',
  info: 'bg-blue-500/20 text-blue-700 dark:text-blue-300 border-blue-500/30',
  danger: 'bg-red-500/20 text-red-700 dark:text-red-300 border-red-500/30',
  warning: 'bg-yellow-500/20 text-yellow-700 dark:text-yellow-300 border-yellow-500/30',
  attention: 'bg-orange-500/20 text-orange-700 dark:text-orange-300 border-orange-500/30',
  neutral: 'bg-gray-500/20 text-gray-600 dark:text-gray-400 border-gray-500/30',
  scheduled: 'bg-violet-500/20 text-violet-700 dark:text-violet-300 border-violet-500/30',
  starting: 'bg-sky-500/20 text-sky-700 dark:text-sky-300 border-sky-500/30',
  accent: 'bg-indigo-500/20 text-indigo-700 dark:text-indigo-300 border-indigo-500/30',
}

/** Solid fill for small markers (step dots, live indicators). */
export const toneFill: Record<Tone, string> = {
  success: 'bg-green-500',
  info: 'bg-blue-500',
  danger: 'bg-red-500',
  warning: 'bg-yellow-500',
  attention: 'bg-orange-500',
  neutral: 'bg-gray-400 dark:bg-gray-600',
  scheduled: 'bg-violet-500',
  starting: 'bg-sky-500',
  accent: 'bg-indigo-500',
}

/** Outline plus faint surface, for bordered cards such as DAG nodes. */
export const toneOutline: Record<Tone, string> = {
  success: 'border-green-500 bg-green-500/10',
  info: 'border-blue-500 bg-blue-500/10',
  danger: 'border-red-500 bg-red-500/10',
  warning: 'border-yellow-500 bg-yellow-500/10',
  attention: 'border-orange-500 bg-orange-500/10',
  neutral: 'border-border bg-muted',
  scheduled: 'border-violet-500 bg-violet-500/10',
  starting: 'border-sky-500 bg-sky-500/10',
  accent: 'border-indigo-500 bg-indigo-500/10',
}

/** Icon-only row/header action whose meaning is carried by color (cancel, retry, rerun). */
export const toneAction: Record<Tone, string> = {
  success: 'text-green-700 hover:bg-green-100 dark:text-green-400 dark:hover:bg-green-950',
  info: 'text-blue-700 hover:bg-blue-100 dark:text-blue-400 dark:hover:bg-blue-950',
  danger: 'text-red-700 hover:bg-red-100 dark:text-red-400 dark:hover:bg-red-950',
  warning: 'text-yellow-700 hover:bg-yellow-100 dark:text-yellow-400 dark:hover:bg-yellow-950',
  attention: 'text-orange-700 hover:bg-orange-100 dark:text-orange-400 dark:hover:bg-orange-950',
  neutral: 'text-muted-foreground hover:bg-muted',
  scheduled: 'text-violet-700 hover:bg-violet-100 dark:text-violet-400 dark:hover:bg-violet-950',
  starting: 'text-sky-700 hover:bg-sky-100 dark:text-sky-400 dark:hover:bg-sky-950',
  accent: 'text-indigo-700 hover:bg-indigo-100 dark:text-indigo-400 dark:hover:bg-indigo-950',
}

/** Inline notice box for a non-fatal condition (degraded backend, disabled integration). */
export const warningNotice = 'rounded-md border border-amber-500/30 bg-amber-500/5 text-amber-700 dark:text-amber-400'
