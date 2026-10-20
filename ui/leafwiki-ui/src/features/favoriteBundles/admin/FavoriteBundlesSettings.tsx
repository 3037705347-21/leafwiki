import { Button } from '@/components/ui/button'
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import { mapApiError } from '@/lib/api/errors'
import type { FavoriteBundle } from '@/lib/api/favoriteBundles'
import { useDateTimeFormat } from '@/lib/useDateTimeFormat'
import { useFavoriteBundlesStore } from '@/stores/favoriteBundles'
import { useTreeStore } from '@/stores/tree'
import { Pencil, Plus, Trash2 } from 'lucide-react'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { useSetTitle } from '../../viewer/setTitle'
import { BundleEditDialog } from './BundleEditDialog'

export default function FavoriteBundlesSettings() {
  const { t } = useTranslation('favoriteBundles')
  const { formatDateTime } = useDateTimeFormat()
  const bundles = useFavoriteBundlesStore((s) => s.bundles)
  const loadBundles = useFavoriteBundlesStore((s) => s.loadBundles)
  const deleteBundle = useFavoriteBundlesStore((s) => s.deleteBundle)
  const byId = useTreeStore((s) => s.byId)
  const [loading, setLoading] = useState(true)
  const [editTarget, setEditTarget] = useState<FavoriteBundle | undefined>()
  const [dialogOpen, setDialogOpen] = useState(false)
  const [deleteTarget, setDeleteTarget] = useState<FavoriteBundle | null>(null)
  const [deleting, setDeleting] = useState(false)
  useSetTitle({ title: t('admin.page.title') })

  useEffect(() => {
    loadBundles()
      .catch((err) => {
        console.warn(err)
        toast.error(mapApiError(err, t('admin.page.loadErrorFallback')).message)
      })
      .finally(() => setLoading(false))
  }, [loadBundles, t])

  const pageTitle = (id: string) => byId[id]?.title ?? id

  const openCreate = () => {
    setEditTarget(undefined)
    setDialogOpen(true)
  }
  const openEdit = (bundle: FavoriteBundle) => {
    setEditTarget(bundle)
    setDialogOpen(true)
  }

  const confirmDelete = async () => {
    if (!deleteTarget) return
    setDeleting(true)
    try {
      await deleteBundle(deleteTarget.id)
      setDeleteTarget(null)
      toast.success(t('admin.page.deleteSuccess'))
    } catch (err) {
      console.warn(err)
      toast.error(mapApiError(err, t('admin.page.deleteErrorFallback')).message)
    } finally {
      setDeleting(false)
    }
  }

  return (
    <div className="settings">
      <h1 className="settings__title">{t('admin.page.heading')}</h1>
      <p className="text-muted-foreground text-sm">
        {t('admin.page.headingDescription')}
      </p>

      <div className="settings__header-actions">
        <Button onClick={openCreate} data-testid="bundle-create-button">
          <Plus size={16} />
          {t('admin.page.createButton')}
        </Button>
      </div>

      <div className="settings__table-card">
        <div className="settings__table-scroll">
          <table className="settings__table">
            <thead className="settings__table-head">
              <tr>
                <th className="settings__table-header-cell">
                  {t('admin.table.name')}
                </th>
                <th className="settings__table-header-cell">
                  {t('admin.table.pages')}
                </th>
                <th className="settings__table-header-cell">
                  {t('admin.table.updated')}
                </th>
                <th className="settings__table-header-cell">
                  {t('admin.table.actions')}
                </th>
              </tr>
            </thead>
            <tbody>
              {loading && (
                <tr>
                  <td colSpan={4} className="settings__table-body-message">
                    {t('admin.table.loading')}
                  </td>
                </tr>
              )}
              {!loading && bundles.length === 0 && (
                <tr>
                  <td colSpan={4} className="settings__table-body-message">
                    {t('admin.table.empty')}
                  </td>
                </tr>
              )}
              {!loading &&
                bundles.map((bundle) => (
                  <tr key={bundle.id} className="settings__table-row">
                    <td className="settings__table-cell">
                      <div className="font-medium">{bundle.name}</div>
                      {bundle.description && (
                        <div className="text-muted-foreground text-xs">
                          {bundle.description}
                        </div>
                      )}
                    </td>
                    <td className="settings__table-cell">
                      {bundle.pageIds.length === 0
                        ? '—'
                        : bundle.pageIds.map(pageTitle).join(', ')}
                    </td>
                    <td className="settings__table-cell">
                      {formatDateTime(bundle.updatedAt) || '—'}
                    </td>
                    <td className="settings__actions-cell">
                      <div className="settings__actions">
                        <Button
                          variant="ghost"
                          size="icon"
                          aria-label={t('admin.table.editAriaLabel', {
                            name: bundle.name,
                          })}
                          data-testid={`bundle-edit-${bundle.id}`}
                          onClick={() => openEdit(bundle)}
                        >
                          <Pencil size={16} />
                        </Button>
                        <Button
                          variant="ghost"
                          size="icon"
                          aria-label={t('admin.table.deleteAriaLabel', {
                            name: bundle.name,
                          })}
                          data-testid={`bundle-delete-${bundle.id}`}
                          onClick={() => setDeleteTarget(bundle)}
                        >
                          <Trash2 size={16} />
                        </Button>
                      </div>
                    </td>
                  </tr>
                ))}
            </tbody>
          </table>
        </div>
      </div>

      <BundleEditDialog
        open={dialogOpen}
        onOpenChange={setDialogOpen}
        bundle={editTarget}
      />

      <AlertDialog
        open={deleteTarget !== null}
        onOpenChange={(open) => {
          if (!open) setDeleteTarget(null)
        }}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t('admin.delete.title')}</AlertDialogTitle>
            <AlertDialogDescription>
              {t('admin.delete.description', {
                name: deleteTarget?.name ?? '',
              })}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel disabled={deleting}>
              {t('admin.delete.cancel')}
            </AlertDialogCancel>
            <AlertDialogAction
              onClick={confirmDelete}
              disabled={deleting}
              data-testid="bundle-delete-confirm"
            >
              {t('admin.delete.confirm')}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  )
}
