import js from '@eslint/js'
import globals from 'globals'
import reactHooks from 'eslint-plugin-react-hooks'
import reactRefresh from 'eslint-plugin-react-refresh'
import tseslint from 'typescript-eslint'
import { defineConfig, globalIgnores } from 'eslint/config'

// Raw Tailwind palette classes (text-red-400, bg-green-500/20, …). Colors go
// through semantic theme tokens (text-muted-foreground, bg-card, …) or the
// status tones in src/shared/status.ts, which define light and dark shades
// together — a palette class picked for one theme is what left status badges
// illegible on the light theme.
const PALETTE_CLASS = String.raw`\b(?:[a-z]+:)*(?:text|bg|border|ring|fill|stroke|outline|from|to|via|divide|placeholder|decoration|accent|caret|shadow)-(?:red|green|blue|yellow|orange|amber|violet|sky|gray|zinc|slate|indigo|emerald|rose|purple|pink|teal|cyan|lime|neutral|stone|fuchsia)-\d{2,3}\b`
const paletteMessage = 'Use a semantic token or a tone from @/shared/status instead of a raw palette class.'

// One navigation API: pages and features use @/lib/router (string paths).
// Only the route tree (App.tsx) and the wrapper itself touch TanStack Router.
const routerImport = {
  name: '@tanstack/react-router',
  message: 'Import navigation from @/lib/router instead (only App.tsx and lib/router.tsx use TanStack Router directly).',
}

// Confirmations go through shared/components/ConfirmDialog, which derives the
// title and button copy from verb + noun so every dialog reads the same way.
const confirmMessage = 'Use ConfirmDialog from @/shared/components/ConfirmDialog instead of assembling an AlertDialog.'
const alertDialogImports = [
  { name: '@/components/ui/alert-dialog', message: confirmMessage },
  { name: '@loykin/designkit', importNames: ['AlertDialog', 'AlertDialogContent', 'AlertDialogAction', 'AlertDialogCancel'], message: confirmMessage },
]
// Forms end with FormSubmitBar: FormActions' own status line is muted text,
// so a server error passed to it looked like a neutral note.
const formActionsImport = {
  name: '@loykin/designkit',
  importNames: ['FormActions'],
  message: 'Use FormSubmitBar from @/shared/components/FormSubmitBar (errors in destructive color, labels from verb + noun).',
}
const featurePaths = [routerImport, ...alertDialogImports, formActionsImport]

export default defineConfig([
  globalIgnores(['dist']),
  {
    files: ['**/*.{ts,tsx}'],
    extends: [
      js.configs.recommended,
      tseslint.configs.recommended,
      reactHooks.configs.flat.recommended,
      reactRefresh.configs.vite,
    ],
    languageOptions: {
      ecmaVersion: 2020,
      globals: globals.browser,
    },
    rules: {
      // setState-in-effect is intentional for reset patterns (usePolling, log stream, etc.)
      'react-hooks/set-state-in-effect': 'warn',
      // Context files export both Provider and hook — HMR trade-off accepted.
      'react-refresh/only-export-components': ['warn', { allowConstantExport: true }],
      // Underscore-prefixed params are intentionally unused (placeholder functions, etc.)
      '@typescript-eslint/no-unused-vars': ['error', { argsIgnorePattern: '^_', varsIgnorePattern: '^_' }],
      // Browser dialogs block the page and are suppressed in embedded views;
      // show errors inline and confirm with ConfirmDialog.
      'no-restricted-globals': ['error',
        { name: 'alert', message: 'Show the message inline (notice, dialog error, MutationErrors) instead.' },
        { name: 'confirm', message: 'Use ConfirmDialog instead.' },
        { name: 'prompt', message: 'Use a form field instead.' },
      ],
      'no-restricted-syntax': ['error',
        { selector: `Literal[value=/${PALETTE_CLASS}/]`, message: paletteMessage },
        { selector: `TemplateElement[value.raw=/${PALETTE_CLASS}/]`, message: paletteMessage },
        // Forwarding onOpenChange(true) into state is what let a confirm
        // dialog reopen itself against a different target (AY). Wire it as
        // `open => { if (!open) cancel() }` with useDeleteTarget/useConfirmAction.
        {
          selector: "JSXOpeningElement[name.name='AlertDialog'] > JSXAttribute[name.name='onOpenChange'] > JSXExpressionContainer > Identifier",
          message: 'Do not pass a state setter to AlertDialog onOpenChange — use `open => { if (!open) cancel() }` (see docs/frontend/develop.md rule 8).',
        },
        // A swallowed error is a failed action the user never hears about
        // (the Serving panel's Restart/Stop did exactly this).
        {
          selector: 'CatchClause > BlockStatement[body.length=0]',
          message: 'Do not swallow errors — show them (dialog `error`, notice) or handle them explicitly.',
        },
        // react-hook-form reserves `errors.root` and clears it before deciding
        // a submit is valid, so a form field named `root` submits with {} when
        // it is the only invalid field (the pipeline source setup crash).
        ...[
          "CallExpression[callee.object.name='z'][callee.property.name='object'] > ObjectExpression > Property[key.name='root']",
          "CallExpression[callee.name='register'] > Literal:first-child[value='root']",
          "JSXOpeningElement[name.name='Controller'] > JSXAttribute[name.name='name'] > Literal[value='root']",
          "Property[key.name='path'] > ArrayExpression > Literal:first-child[value='root']",
        ].map(selector => ({ selector, message: 'Do not name a form field `root` — react-hook-form reserves errors.root (see docs/frontend/develop.md Form Convention).' })),
      ],
    },
  },
  {
    files: ['src/shared/status.ts'],
    rules: { 'no-restricted-syntax': 'off' },
  },
  // Layering: lib/ components/ shared/ → features/ → pages/. Lower layers
  // never import upward; domain UI that needs a feature's hooks or types
  // lives in that feature, not in shared/.
  {
    files: ['src/lib/**', 'src/components/**', 'src/shared/**'],
    rules: {
      'no-restricted-imports': ['error', { paths: [routerImport], patterns: [
        { group: ['@/features/*', '@/pages/*'], message: 'lib/, components/, and shared/ must not depend on features/ or pages/ — move the component into its feature.' },
      ] }],
    },
  },
  {
    files: ['src/features/**', 'src/pages/**'],
    rules: {
      'no-restricted-imports': ['error', { paths: featurePaths, patterns: [
        { group: ['@/pages/*'], message: 'features/ must not depend on pages/.' },
      ] }],
    },
  },
  {
    files: ['src/lib/router.tsx'],
    rules: { 'no-restricted-imports': 'off' },
  },
  // schedules depends on pipelines (a schedule runs a template), never the
  // reverse — template deploy lives in features/schedules for that reason.
  {
    files: ['src/features/pipelines/**'],
    rules: {
      'no-restricted-imports': ['error', { paths: featurePaths, patterns: [
        { group: ['@/pages/*'], message: 'features/ must not depend on pages/.' },
        { group: ['@/features/schedules/*'], message: 'pipelines must not import schedules (schedules → pipelines only).' },
      ] }],
    },
  },
])
