export type Mutation = { isError: boolean; error: unknown }

/** True when any of the mutations last failed — gate a `notice` prop on it. */
export function hasMutationError(mutations: Mutation[]): boolean {
  return mutations.some(m => m.isError)
}
