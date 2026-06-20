import { useQuery } from '@tanstack/react-query'
import * as api from './api'

export const systemKeys = {
  settings: () => ['system', 'settings'] as const,
}

export function useSystemSettings() {
  return useQuery({
    queryKey: systemKeys.settings(),
    queryFn: api.getSystemSettings,
  })
}
