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
      'no-restricted-imports': ['error', { paths: [routerImport], patterns: [
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
      'no-restricted-imports': ['error', { paths: [routerImport], patterns: [
        { group: ['@/pages/*'], message: 'features/ must not depend on pages/.' },
        { group: ['@/features/schedules/*'], message: 'pipelines must not import schedules (schedules → pipelines only).' },
      ] }],
    },
  },
])
