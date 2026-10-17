import { parseDocument } from 'yaml'

export interface StepDef {
  name: string
  depends_on: string[]
}

// Steps and their dependencies, read with the YAML parser. A run's
// pipeline_yaml is the stored document, so anything that isn't spec.steps
// (outputs, env entries) must never become a node.
export function parseStepsFromYaml(yaml?: string | null): StepDef[] {
  if (!yaml) return []
  const doc = parseDocument(yaml)
  if (doc.errors.length > 0) return []
  const steps = (doc.toJS() as { spec?: { steps?: unknown } } | null)?.spec?.steps
  if (!Array.isArray(steps)) return []
  return steps.flatMap(value => {
    const step = (value ?? {}) as { name?: unknown, depends_on?: unknown }
    if (typeof step.name !== 'string' || !step.name) return []
    const dependsOn = Array.isArray(step.depends_on) ? step.depends_on.map(String) : []
    return [{ name: step.name, depends_on: dependsOn }]
  })
}
