import { describe, expect, it } from 'vitest'
import { ApiError } from '@/lib/api'
import { resourceState } from './queryState'

describe('resourceState', () => {
  it('reads only a 404 as missing', () => {
    expect(resourceState({ isLoading: false, error: new ApiError(404, 'not found') })).toBe('missing')
    expect(resourceState({ isLoading: false, error: null })).toBe('missing')
  })

  it('reads a server or network error as failed, not missing', () => {
    expect(resourceState({ isLoading: false, error: new ApiError(500, 'boom') })).toBe('failed')
    expect(resourceState({ isLoading: false, error: new TypeError('Failed to fetch') })).toBe('failed')
  })

  it('is loading while the query is loading', () => {
    expect(resourceState({ isLoading: true, error: null })).toBe('loading')
  })
})
