/**
 * Shared options for genuinely live status queries.
 *
 * Do not use this for catalogs or configuration resources. Those should use
 * mutation invalidation and an appropriate staleTime instead.
 */
export const backgroundPollingNotifications = {
  notifyOnChangeProps: ['data', 'isLoading'] as Array<'data' | 'isLoading'>,
}

export function backgroundPolling(interval: number) {
  return {
    refetchInterval: interval,
    ...backgroundPollingNotifications,
  }
}
