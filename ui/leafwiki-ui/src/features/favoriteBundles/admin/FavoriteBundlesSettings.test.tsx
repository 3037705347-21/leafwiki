import '@/lib/i18n'
import type { FavoriteBundle } from '@/lib/api/favoriteBundles'
import type { PageNode } from '@/lib/api/pages'
import { useFavoriteBundlesStore } from '@/stores/favoriteBundles'
import { useTreeStore } from '@/stores/tree'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import FavoriteBundlesSettings from './FavoriteBundlesSettings'

vi.mock('sonner', () => ({
  toast: { error: vi.fn(), success: vi.fn(), warning: vi.fn() },
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

const makeBundle = (
  overrides: Partial<FavoriteBundle> = {},
): FavoriteBundle => ({
  id: 'b1',
  name: 'team reading',
  description: 'desc',
  createdBy: 'admin-1',
  createdAt: '2026-01-01T00:00:00Z',
  updatedAt: '2026-01-01T00:00:00Z',
  pageIds: ['p1'],
  subscribed: false,
  ...overrides,
})

function makeNode(
  id: string,
  title: string,
  kind: 'page' | 'section',
  children: PageNode[] = [],
): PageNode {
  return { id, title, slug: id, path: id, version: 'v1', kind, children }
}

describe('FavoriteBundlesSettings', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    useFavoriteBundlesStore.setState({ bundles: [], loaded: false })
    useTreeStore.setState({
      tree: makeNode('root', 'root', 'section', [
        makeNode('p1', 'Alpha', 'page'),
      ]),
      byId: { p1: makeNode('p1', 'Alpha', 'page') },
    })
  })

  it('shows the empty state when no lists exist', async () => {
    ;(api.listBundles as ReturnType<typeof vi.fn>).mockResolvedValue([])

    render(<FavoriteBundlesSettings />)

    await waitFor(() =>
      expect(screen.getByText('No reading lists yet')).toBeInTheDocument(),
    )
  })

  it('creates a bundle with selected pages via the edit dialog', async () => {
    ;(api.listBundles as ReturnType<typeof vi.fn>).mockResolvedValue([])
    ;(api.createBundle as ReturnType<typeof vi.fn>).mockResolvedValue(
      makeBundle({ id: 'b2', name: 'new list' }),
    )
    const user = userEvent.setup()

    render(<FavoriteBundlesSettings />)
    await waitFor(() =>
      expect(screen.getByText('No reading lists yet')).toBeInTheDocument(),
    )

    await user.click(screen.getByTestId('bundle-create-button'))
    expect(screen.getByTestId('bundle-edit-dialog')).toBeInTheDocument()

    await user.type(screen.getByTestId('bundle-name-input'), 'new list')
    await user.click(screen.getByTestId('bundle-page-picker-checkbox-p1'))

    await user.click(screen.getByTestId('bundle-save-button'))

    await waitFor(() => {
      expect(api.createBundle).toHaveBeenCalledWith({
        name: 'new list',
        description: '',
        pageIds: ['p1'],
      })
    })
  })

  it('deletes a bundle after confirmation', async () => {
    ;(api.listBundles as ReturnType<typeof vi.fn>).mockResolvedValue([
      makeBundle(),
    ])
    ;(api.deleteBundle as ReturnType<typeof vi.fn>).mockResolvedValue(undefined)
    const user = userEvent.setup()

    render(<FavoriteBundlesSettings />)
    await waitFor(() =>
      expect(screen.getByText('team reading')).toBeInTheDocument(),
    )

    await user.click(screen.getByTestId('bundle-delete-b1'))
    await waitFor(() =>
      expect(screen.getByText('Delete reading list?')).toBeInTheDocument(),
    )
    await user.click(screen.getByTestId('bundle-delete-confirm'))

    await waitFor(() => {
      expect(api.deleteBundle).toHaveBeenCalledWith('b1')
    })
  })
})
