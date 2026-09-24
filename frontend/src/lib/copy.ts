/**
 * UI copy style: labels, buttons, and titles are Title Case; descriptions,
 * help text, messages, and questions ("Delete this schedule?") are sentence
 * case. `src/copy.test.ts` checks every literal label/title in the source.
 */

// Articles, conjunctions, and short prepositions stay lower-case mid-phrase.
const MINOR = new Set(['a', 'an', 'the', 'and', 'but', 'or', 'nor', 'for', 'to', 'of', 'in', 'on', 'at', 'by', 'as', 'via', 'vs', 'with', 'from'])

/** "deploy to schedule" → "Deploy to Schedule"; leaves acronyms (YAML, MLflow) alone. */
export function titleCase(text: string): string {
  return text.split(' ').map((word, i) => {
    // Leave acronyms and identifiers (access_key_id) as written.
    if (!/^[a-z]/.test(word) || word.includes('_')) return word
    if (i > 0 && MINOR.has(word)) return word
    return word[0].toUpperCase() + word.slice(1)
  }).join(' ')
}

/** How a list page starts a resource: created ("New"), added, or started. */
export type CreateVerb = 'New' | 'Add' | 'Deploy' | 'Launch'

/** A form's primary action. */
export type SubmitVerb = 'Create' | 'Add' | 'Deploy' | 'Launch' | 'Save' | 'Rotate' | 'Upload' | 'Submit' | 'Test' | 'Start'

/**
 * An action that needs confirmation: irreversible, or it interrupts something
 * running. Reversible toggles (enable/disable) run immediately instead.
 */
export type ConfirmVerb = 'Delete' | 'Remove' | 'Stop' | 'Cancel' | 'Purge'

const SUBMIT_FOR: Record<CreateVerb, SubmitVerb> = { New: 'Create', Add: 'Add', Deploy: 'Deploy', Launch: 'Launch' }

const IN_PROGRESS: Record<SubmitVerb | ConfirmVerb, string> = {
  Create: 'Creating', Add: 'Adding', Deploy: 'Deploying', Launch: 'Launching', Save: 'Saving', Rotate: 'Rotating',
  Upload: 'Uploading', Submit: 'Submitting', Test: 'Testing', Start: 'Starting',
  Delete: 'Deleting', Remove: 'Removing', Stop: 'Stopping', Cancel: 'Cancelling', Purge: 'Purging',
}

/**
 * The copy of one create flow, so the list button, the create page's title
 * and last breadcrumb, and its submit button always agree:
 * `createCopy('Schedule')` → "New Schedule" / "New Schedule" / "Create Schedule".
 */
export function createCopy(noun: string, verb: CreateVerb = 'New') {
  const action = `${verb} ${noun}`
  return { action, title: action, crumb: action, submitVerb: SUBMIT_FOR[verb], noun }
}

/**
 * A primary button's label: "Create Schedule", or "Creating…" while the
 * request is in flight. The noun is Title Cased ("alert rule" → "Alert Rule").
 */
export function actionLabel(verb: SubmitVerb | ConfirmVerb, noun: string, pending: boolean): string {
  return pending ? `${IN_PROGRESS[verb]}…` : `${verb} ${titleCase(noun)}`
}
