import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'
import { handleFieldErrors } from '@/lib/handleFieldErrors'
import type { FavoriteBundle } from '@/lib/api/favoriteBundles'
import { useFavoriteBundlesStore } from '@/stores/favoriteBundles'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { BundlePagePicker } from './BundlePagePicker'

type BundleEditDialogProps = {
  open: boolean
  onOpenChange: (open: boolean) => void
  bundle?: FavoriteBundle // undefined = create mode
}

export function BundleEditDialog({
  open,
  onOpenChange,
  bundle,
}: BundleEditDialogProps) {
  const { t } = useTranslation('favoriteBundles')
  const createBundle = useFavoriteBundlesStore((s) => s.createBundle)
  const updateBundle = useFavoriteBundlesStore((s) => s.updateBundle)

  const [name, setName] = useState('')
  const [description, setDescription] = useState('')
  const [pageIds, setPageIds] = useState<string[]>([])
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({})
  const [saving, setSaving] = useState(false)

  const isEdit = bundle !== undefined

  useEffect(() => {
    if (!open) return
    setName(bundle?.name ?? '')
    setDescription(bundle?.description ?? '')
    setPageIds(bundle?.pageIds ?? [])
    setFieldErrors({})
  }, [open, bundle])

  const handleSave = async () => {
    setSaving(true)
    try {
      const payload = {
        name: name.trim(),
        description: description.trim(),
        pageIds,
      }
      if (isEdit && bundle) {
        await updateBundle(bundle.id, payload)
      } else {
        await createBundle(payload)
      }
      onOpenChange(false)
    } catch (err) {
      handleFieldErrors(err, setFieldErrors, t('admin.edit.saveErrorFallback'))
    } finally {
      setSaving(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent data-testid="bundle-edit-dialog">
        <DialogHeader>
          <DialogTitle>
            {isEdit ? t('admin.edit.titleEdit') : t('admin.edit.titleCreate')}
          </DialogTitle>
          <DialogDescription>
            {isEdit
              ? t('admin.edit.descriptionEdit')
              : t('admin.edit.descriptionCreate')}
          </DialogDescription>
        </DialogHeader>

        <div className="flex flex-col gap-4">
          <div className="flex flex-col gap-2">
            <Label htmlFor="bundle-name">{t('admin.edit.nameLabel')}</Label>
            <Input
              id="bundle-name"
              value={name}
              autoFocus
              data-testid="bundle-name-input"
              aria-invalid={!!fieldErrors.name}
              onChange={(e) => {
                setName(e.target.value)
                setFieldErrors((prev) => ({ ...prev, name: '' }))
              }}
            />
            {fieldErrors.name && (
              <p role="alert" className="text-destructive text-sm">
                {fieldErrors.name}
              </p>
            )}
          </div>

          <div className="flex flex-col gap-2">
            <Label htmlFor="bundle-description">
              {t('admin.edit.descriptionLabel')}
            </Label>
            <Textarea
              id="bundle-description"
              value={description}
              rows={2}
              data-testid="bundle-description-input"
              onChange={(e) => setDescription(e.target.value)}
            />
          </div>

          <div className="flex flex-col gap-2">
            <Label>{t('admin.edit.pagesLabel')}</Label>
            {fieldErrors.pageIds && (
              <p role="alert" className="text-destructive text-sm">
                {fieldErrors.pageIds}
              </p>
            )}
            <BundlePagePicker selectedIds={pageIds} onChange={setPageIds} />
          </div>
        </div>

        <DialogFooter>
          <Button
            variant="outline"
            onClick={() => onOpenChange(false)}
            disabled={saving}
          >
            {t('admin.edit.cancel')}
          </Button>
          <Button
            onClick={handleSave}
            disabled={saving || !name.trim()}
            data-testid="bundle-save-button"
          >
            {isEdit ? t('admin.edit.saveEdit') : t('admin.edit.saveCreate')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
