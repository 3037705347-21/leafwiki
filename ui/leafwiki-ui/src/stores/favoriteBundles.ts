// stores/favoriteBundles.ts
// Team reading lists ("favorite bundles") plus the current user's
// subscription state. Server truth fetched once per session (like the
// favorites store), never persisted to localStorage and cleared on logout.
// Every mutation re-fetches both the bundle list and the personal favorites
// list, since sync/unsubscribe change favorites on the server.

import * as favoriteBundlesApi from '@/lib/api/favoriteBundles'
import type {
  BundleInput,
  BundleSyncResult,
  FavoriteBundle,
} from '@/lib/api/favoriteBundles'
import { create } from 'zustand'
import { useFavoritesStore } from './favorites'

type FavoriteBundlesStore = {
  bundles: FavoriteBundle[]
  loaded: boolean
  loadBundles: () => Promise<void>
  createBundle: (input: BundleInput) => Promise<FavoriteBundle>
  updateBundle: (id: string, input: BundleInput) => Promise<FavoriteBundle>
  deleteBundle: (id: string) => Promise<void>
  syncBundle: (id: string) => Promise<BundleSyncResult>
  unsubscribeBundle: (id: string) => Promise<void>
  clearBundles: () => void
}

async function refreshFavorites(): Promise<void> {
  await useFavoritesStore.getState().loadFavorites()
}

export const useFavoriteBundlesStore = create<FavoriteBundlesStore>()(
  (set, get) => ({
    bundles: [],
    loaded: false,

    loadBundles: async () => {
      const bundles = await favoriteBundlesApi.listBundles()
      set({ bundles, loaded: true })
    },

    createBundle: async (input) => {
      const created = await favoriteBundlesApi.createBundle(input)
      await get().loadBundles()
      return created
    },

    updateBundle: async (id, input) => {
      const updated = await favoriteBundlesApi.updateBundle(id, input)
      await get().loadBundles()
      return updated
    },

    deleteBundle: async (id) => {
      await favoriteBundlesApi.deleteBundle(id)
      await get().loadBundles()
    },

    syncBundle: async (id) => {
      const result = await favoriteBundlesApi.syncBundle(id)
      await get().loadBundles()
      await refreshFavorites()
      return result
    },

    unsubscribeBundle: async (id) => {
      await favoriteBundlesApi.unsubscribeBundle(id)
      await get().loadBundles()
      await refreshFavorites()
    },

    clearBundles: () => set({ bundles: [], loaded: false }),
  }),
)
