// workers feature hooks — React Query wrappers
import { useQuery } from '@tanstack/react-query'
import * as api from './api'

export const workerKeys = {
  all: ['workers'] as const,
  list: () => ['workers', 'list'] as const,
}

export function useWorkers() {
  return useQuery({
    queryKey: workerKeys.list(),
    queryFn: api.listWorkers,
    refetchInterval: 5000,
    notifyOnChangeProps: ['data', 'isLoading'],
  })
}
