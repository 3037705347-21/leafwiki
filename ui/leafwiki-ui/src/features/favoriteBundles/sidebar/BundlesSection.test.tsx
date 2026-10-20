import { ApiLocalizedError } from '@/lib/api/errors'
import type { FavoriteBundle } from '@/lib/api/favoriteBundles'
import { useFavoriteBundlesStore } from '@/stores/favoriteBundles'
import { fireEvent, render, screen } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { BundlesSection } from './BundlesSection'

vi.mock('react-i18next', () => ({
  initReactI18next: { type: '3rdParty', init: () => {} },
  useTranslation: () => ({ t: (key: string) => key, i18n: { language: 'en' } }),
}))

vi.mock('sonner', () => ({
  toast: { success: vi.fn(), error: vi.fn(), warning: vi.fn() },
}))

vi.mock('@/lib/api/favoriteBundles', () => ({
  listBundles: vi.fn(),
  createBundle: vi.fn(),
  updateBundle: vi.fn(),
  deleteBundle: vi.fn(),
  syncBundle: vi.fn(),
  unsubscribeBundle: vi.fn(),
}))

vi.mock('@/lib/api/favorites', () => ({
  getFavorites: vi.fn().mockResolvedValue([]),
  addFavorite: vi.fn(),
  removeFavorite: vi.fn(),
}))

import * as api from '@/lib/api/favoriteBundles'
import { toast } from 'sonner'

const makeBundle = (
  overrides: Partial<FavoriteBundle> = {},
): FavoriteBundle => ({
  id: 'b1',
  name: 'team',
  description: '',
  createdBy: 'admin-1',
  createdAt: '2026-01-01T00:00:00Z',
  updatedAt: '2026-01-01T00:00:00Z',
  pageIds: ['p1'],
  subscribed: false,
  ...overrides,
})

describe('BundlesSection', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    useFavoriteBundlesStore.setState({ bundles: [], loaded: false })
  })

  it('renders nothing before data is loaded or when no bundles exist', () => {
    useFavoriteBundlesStore.setState({ bundles: [makeBundle()], loaded: false })
    const { container, rerender } = render(<BundlesSection />)
    expect(container).toBeEmptyDOMElement()

    useFavoriteBundlesStore.setState({ bundles: [], loaded: true })
    rerender(<BundlesSection />)
    expect(screen.queryByTestId('bundles-section')).not.toBeInTheDocument()
  })

  it('offers subscribe-and-sync for an unsubscribed bundle and toasts success', async () => {
    useFavoriteBundlesStore.setState({ bundles: [makeBundle()], loaded: true })
    ;(api.syncBundle as ReturnType<typeof vi.fn>).mockResolvedValue({
      added: ['p1'],
      removed: [],
      lastSyncedAt: '2026-01-02T00:00:00Z',
    })
    ;(api.listBundles as ReturnType<typeof vi.fn>).mockResolvedValue([])

    render(<BundlesSection />)

    const syncButton = screen.getByTestId('bundle-sync-b1')
    expect(syncButton).toHaveTextContent('sidebar.subscribeAndSync')
    expect(
      screen.queryByTestId('bundle-unsubscribe-b1'),
    ).not.toBeInTheDocument()

    fireEvent.click(syncButton)

    expect(api.syncBundle).toHaveBeenCalledWith('b1')
    // Let the async store action finish.
    await vi.waitFor(() => {
      expect(toast.success).toHaveBeenCalledTimes(1)
    })
  })

  it('shows sync-again and unsubscribe for a subscribed bundle', () => {
    useFavoriteBundlesStore.setState({
      bundles: [
        makeBundle({
          subscribed: true,
          lastSyncedAt: '2026-01-02T00:00:00Z',
        }),
      ],
      loaded: true,
    })

    render(<BundlesSection />)

    expect(screen.getByTestId('bundle-sync-b1')).toHaveTextContent(
      'sidebar.syncAgain',
    )
    expect(screen.getByTestId('bundle-unsubscribe-b1')).toBeInTheDocument()
  })

  it('surfaces a 409 sync rejection as an error toast and does not throw', async () => {
    useFavoriteBundlesStore.setState({ bundles: [makeBundle()], loaded: true })
    ;(api.syncBundle as ReturnType<typeof vi.fn>).mockRejectedValue(
      new ApiLocalizedError({
        code: 'favorite_bundle_personal_conflict',
        message: 'conflict',
        template: 'a listed page is already a personal favorite',
      }),
    )

    render(<BundlesSection />)
    fireEvent.click(screen.getByTestId('bundle-sync-b1'))

    await vi.waitFor(() => {
      expect(toast.error).toHaveBeenCalledTimes(1)
    })
  })
})
