import { Button } from '@/components/ui/button'
import { mapApiError } from '@/lib/api/errors'
import type { FavoriteBundle } from '@/lib/api/favoriteBundles'
import { useDateTimeFormat } from '@/lib/useDateTimeFormat'
import { useFavoriteBundlesStore } from '@/stores/favoriteBundles'
import { ListChecks, RefreshCw, UserMinus } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

// BundlesSection is the sidebar entry for team reading lists. Every
// authenticated user sees the available lists and syncs them on demand;
// nothing here runs automatically. Hidden entirely when no list exists.
export function BundlesSection() {
  const { t } = useTranslation('favoriteBundles')
  const { formatDateTime } = useDateTimeFormat()
  const bundles = useFavoriteBundlesStore((s) => s.bundles)
  const loaded = useFavoriteBundlesStore((s) => s.loaded)
  const syncBundle = useFavoriteBundlesStore((s) => s.syncBundle)
  const unsubscribeBundle = useFavoriteBundlesStore((s) => s.unsubscribeBundle)
  const [busyId, setBusyId] = useState<string | null>(null)

  if (!loaded || bundles.length === 0) return null

  const run = async (
    id: string,
    fn: () => Promise<unknown>,
    errorFallback: string,
  ) => {
    setBusyId(id)
    try {
      await fn()
    } catch (err) {
      // Missing page / personal-favorite conflicts come back as localized
      // 409s with the human-readable message in the error body.
      toast.error(mapApiError(err, errorFallback).message)
    } finally {
      setBusyId(null)
    }
  }

  return (
    <div className="tree-view__bundles" data-testid="bundles-section">
      {bundles.map((bundle) => (
        <BundleRow
          key={bundle.id}
          bundle={bundle}
          busy={busyId === bundle.id}
          lastSyncedLabel={
            bundle.lastSyncedAt ? formatDateTime(bundle.lastSyncedAt) : null
          }
          onSync={() =>
            run(
              bundle.id,
              async () => {
                const result = await syncBundle(bundle.id)
                toast.success(
                  t('sidebar.syncSuccess', {
                    added: result.added.length,
                    removed: result.removed.length,
                  }),
                )
              },
              t('sidebar.syncErrorFallback'),
            )
          }
          onUnsubscribe={() =>
            run(
              bundle.id,
              () => unsubscribeBundle(bundle.id),
              t('sidebar.unsubscribeErrorFallback'),
            )
          }
          t={t}
        />
      ))}
    </div>
  )
}

type BundleRowProps = {
  bundle: FavoriteBundle
  busy: boolean
  lastSyncedLabel: string | null
  onSync: () => void
  onUnsubscribe: () => void
  t: ReturnType<typeof useTranslation>['t']
}

function BundleRow({
  bundle,
  busy,
  lastSyncedLabel,
  onSync,
  onUnsubscribe,
  t,
}: BundleRowProps) {
  return (
    <div className="tree-view__bundle" data-testid={`bundle-row-${bundle.id}`}>
      <div className="tree-view__bundle-info">
        <ListChecks size={12} className="shrink-0" />
        <div className="min-w-0">
          <div className="truncate text-sm font-medium">{bundle.name}</div>
          {bundle.subscribed && lastSyncedLabel && (
            <div className="text-muted-foreground truncate text-xs">
              {t('sidebar.lastSynced', { when: lastSyncedLabel })}
            </div>
          )}
        </div>
      </div>
      <div className="tree-view__bundle-actions">
        <Button
          variant="ghost"
          size="sm"
          onClick={onSync}
          disabled={busy}
          data-testid={`bundle-sync-${bundle.id}`}
        >
          <RefreshCw size={13} className={busy ? 'animate-spin' : undefined} />
          {bundle.subscribed
            ? t('sidebar.syncAgain')
            : t('sidebar.subscribeAndSync')}
        </Button>
        {bundle.subscribed && (
          <Button
            variant="ghost"
            size="icon"
            aria-label={t('sidebar.unsubscribeAriaLabel', {
              name: bundle.name,
            })}
            onClick={onUnsubscribe}
            disabled={busy}
            data-testid={`bundle-unsubscribe-${bundle.id}`}
          >
            <UserMinus size={14} />
          </Button>
        )}
      </div>
    </div>
  )
}
