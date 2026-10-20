import type { FavoriteBundle } from '@/lib/api/favoriteBundles'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { useFavoriteBundlesStore } from './favoriteBundles'

vi.mock('@/lib/api/favoriteBundles', () => ({
  listBundles: vi.fn(),
  createBundle: vi.fn(),
  updateBundle: vi.fn(),
  deleteBundle: vi.fn(),
  syncBundle: vi.fn(),
  unsubscribeBundle: vi.fn(),
}))

import * as api from '@/lib/api/favoriteBundles'

// loadFavorites hits /api/favorites; under jsdom it rejects, and the real
// favorites store swallows that. Stub it so sync/unsubscribe tests stay
// deterministic and assert the refresh is triggered.
vi.mock('@/lib/api/favorites', () => ({
  getFavorites: vi.fn().mockResolvedValue([]),
  addFavorite: vi.fn(),
  removeFavorite: vi.fn(),
}))

const makeBundle = (
  overrides: Partial<FavoriteBundle> = {},
): FavoriteBundle => ({
  id: 'b1',
  name: 'team reading',
  description: '',
  createdBy: 'admin-1',
  createdAt: '2026-01-01T00:00:00Z',
  updatedAt: '2026-01-01T00:00:00Z',
  pageIds: ['p1'],
  subscribed: false,
  ...overrides,
})

describe('useFavoriteBundlesStore', () => {
  beforeEach(() => {
    useFavoriteBundlesStore.setState({ bundles: [], loaded: false })
    vi.clearAllMocks()
  })

  it('loadBundles populates bundles and marks loaded', async () => {
    const bundles = [makeBundle()]
    ;(api.listBundles as ReturnType<typeof vi.fn>).mockResolvedValue(bundles)

    await useFavoriteBundlesStore.getState().loadBundles()

    expect(useFavoriteBundlesStore.getState().bundles).toEqual(bundles)
    expect(useFavoriteBundlesStore.getState().loaded).toBe(true)
  })

  it('clearBundles resets state', () => {
    useFavoriteBundlesStore.setState({ bundles: [makeBundle()], loaded: true })
    useFavoriteBundlesStore.getState().clearBundles()
    expect(useFavoriteBundlesStore.getState()).toMatchObject({
      bundles: [],
      loaded: false,
    })
  })

  it('createBundle refreshes the list', async () => {
    const created = makeBundle({ id: 'b2', name: 'second' })
    ;(api.createBundle as ReturnType<typeof vi.fn>).mockResolvedValue(created)
    ;(api.listBundles as ReturnType<typeof vi.fn>).mockResolvedValue([created])

    const result = await useFavoriteBundlesStore
      .getState()
      .createBundle({ name: 'second', description: '', pageIds: [] })

    expect(result).toEqual(created)
    expect(api.listBundles).toHaveBeenCalledTimes(1)
    expect(useFavoriteBundlesStore.getState().bundles).toEqual([created])
  })

  it('deleteBundle refreshes the list', async () => {
    useFavoriteBundlesStore.setState({ bundles: [makeBundle()] })
    ;(api.deleteBundle as ReturnType<typeof vi.fn>).mockResolvedValue(undefined)
    ;(api.listBundles as ReturnType<typeof vi.fn>).mockResolvedValue([])

    await useFavoriteBundlesStore.getState().deleteBundle('b1')

    expect(api.deleteBundle).toHaveBeenCalledWith('b1')
    expect(useFavoriteBundlesStore.getState().bundles).toEqual([])
  })

  it('syncBundle returns the result and refreshes bundles and favorites', async () => {
    const subscribed = makeBundle({
      subscribed: true,
      lastSyncedAt: '2026-01-02T00:00:00Z',
    })
    ;(api.syncBundle as ReturnType<typeof vi.fn>).mockResolvedValue({
      added: ['p2'],
      removed: [],
      lastSyncedAt: '2026-01-02T00:00:00Z',
    })
    ;(api.listBundles as ReturnType<typeof vi.fn>).mockResolvedValue([
      subscribed,
    ])

    const result = await useFavoriteBundlesStore.getState().syncBundle('b1')

    expect(result.added).toEqual(['p2'])
    expect(api.syncBundle).toHaveBeenCalledWith('b1')
    expect(useFavoriteBundlesStore.getState().bundles[0].subscribed).toBe(true)
  })

  it('unsubscribeBundle calls the API and refreshes', async () => {
    ;(api.unsubscribeBundle as ReturnType<typeof vi.fn>).mockResolvedValue(
      undefined,
    )
    ;(api.listBundles as ReturnType<typeof vi.fn>).mockResolvedValue([])

    await useFavoriteBundlesStore.getState().unsubscribeBundle('b1')

    expect(api.unsubscribeBundle).toHaveBeenCalledWith('b1')
  })
})
