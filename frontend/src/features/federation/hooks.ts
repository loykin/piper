import { useQuery } from '@tanstack/react-query'
import { listFederationMembers } from './api'

export const federationKeys = {
  members: () => ['federation', 'members'] as const,
}

export function useFederationMembers() {
  return useQuery({
    queryKey: federationKeys.members(),
    queryFn: listFederationMembers,
    staleTime: 10_000,
  })
}
