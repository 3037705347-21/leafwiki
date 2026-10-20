import { fetchWithAuth } from './auth'

// FavoriteBundle is an admin-managed reading list with the requesting
// user's subscription state attached. The same shape is returned by the
// list and detail endpoints.
export type FavoriteBundle = {
  id: string
  name: string
  description: string
  createdBy: string
  createdAt: string
  updatedAt: string
  pageIds: string[]
  subscribed: boolean
  subscribedAt?: string
  lastSyncedAt?: string
}

export type BundleInput = {
  name: string
  description: string
  pageIds: string[]
}

export type BundleSyncResult = {
  added: string[]
  removed: string[]
  lastSyncedAt: string
}

export async function listBundles(): Promise<FavoriteBundle[]> {
  const res = await fetchWithAuth<{ bundles: FavoriteBundle[] }>(
    '/api/favorite-bundles',
  )
  return res.bundles
}

export async function createBundle(
  input: BundleInput,
): Promise<FavoriteBundle> {
  return fetchWithAuth<FavoriteBundle>('/api/favorite-bundles', {
    method: 'POST',
    body: JSON.stringify(input),
  })
}

export async function updateBundle(
  id: string,
  input: BundleInput,
): Promise<FavoriteBundle> {
  return fetchWithAuth<FavoriteBundle>(`/api/favorite-bundles/${id}`, {
    method: 'PUT',
    body: JSON.stringify(input),
  })
}

export async function deleteBundle(id: string): Promise<void> {
  await fetchWithAuth<null>(`/api/favorite-bundles/${id}`, {
    method: 'DELETE',
  })
}

export async function syncBundle(id: string): Promise<BundleSyncResult> {
  return fetchWithAuth<BundleSyncResult>(`/api/favorite-bundles/${id}/sync`, {
    method: 'POST',
  })
}

export async function unsubscribeBundle(id: string): Promise<void> {
  await fetchWithAuth<null>(`/api/favorite-bundles/${id}/unsubscribe`, {
    method: 'POST',
  })
}
