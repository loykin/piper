import { afterEach, describe, expect, it, vi } from 'vitest'
import { api, ContractError } from './api'

function mockResponse(body: unknown, headers: Record<string, string> = {}) {
  vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify(body), { status: 200, headers })))
}

afterEach(() => vi.unstubAllGlobals())

describe('list contract', () => {
  it('getPaged returns items and total', async () => {
    mockResponse([{ id: 'a' }], { 'X-Total-Count': '7' })
    await expect(api.getPaged('/x')).resolves.toEqual({ items: [{ id: 'a' }], total: 7 })
  })

  it('getPaged rejects a missing X-Total-Count', async () => {
    mockResponse([])
    await expect(api.getPaged('/x')).rejects.toBeInstanceOf(ContractError)
  })

  it('getList rejects a null body instead of showing an empty list', async () => {
    mockResponse(null)
    await expect(api.getList('/x')).rejects.toBeInstanceOf(ContractError)
  })
})
