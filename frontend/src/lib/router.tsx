/* eslint-disable react-refresh/only-export-components */
import { useCallback } from 'react'
import { Link, useNavigate as useTanStackNavigate, useParams as useTanStackParams, useRouterState } from '@tanstack/react-router'

type NavigateOptions = {
  replace?: boolean
}

type SearchParamsInit = string | URLSearchParams | Record<string, string>

function splitTo(to: string) {
  const [pathname, search = ''] = to.split('?', 2)
  return {
    to: pathname || '/',
    search: search ? Object.fromEntries(new URLSearchParams(search)) : undefined,
  }
}

export function useNavigate() {
  const navigate = useTanStackNavigate()
  return useCallback((to: string | number, options?: NavigateOptions) => {
    if (typeof to === 'number') {
      window.history.go(to)
      return
    }
    const next = splitTo(to)
    return navigate({
      to: next.to,
      search: next.search,
      replace: options?.replace,
    })
  }, [navigate])
}

export function useParams<T extends Record<string, string | undefined> = Record<string, string | undefined>>() {
  return useTanStackParams({ strict: false } as never) as T
}

export function useLocation() {
  return useRouterState({ select: state => state.location })
}

export function useSearchParams(): [
  URLSearchParams,
  (next: SearchParamsInit, options?: NavigateOptions) => void,
] {
  const location = useLocation()
  const navigate = useTanStackNavigate()
  const params = new URLSearchParams(location.searchStr)

  function setSearchParams(next: SearchParamsInit, options?: NavigateOptions) {
    const searchParams = typeof next === 'string'
      ? new URLSearchParams(next)
      : next instanceof URLSearchParams
        ? next
        : new URLSearchParams(next)
    void navigate({
      to: location.pathname,
      search: Object.fromEntries(searchParams),
      replace: options?.replace,
    })
  }

  return [params, setSearchParams]
}

export { Link }
