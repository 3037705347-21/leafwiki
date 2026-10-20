import { Checkbox } from '@/components/ui/checkbox'
import { Input } from '@/components/ui/input'
import { NODE_KIND_PAGE, NODE_KIND_SECTION, PageNode } from '@/lib/api/pages'
import { cn } from '@/lib/utils'
import { useTreeStore } from '@/stores/tree'
import { File, FolderTree } from 'lucide-react'
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

type FlatNode = {
  id: string
  title: string
  depth: number
  kind: 'page' | 'section'
}

// flattenTree walks the tree in display order. Sections are kept (indented
// context for the admin) but are not selectable — reading lists contain
// pages only.
function flattenTree(node: PageNode, depth = 0): FlatNode[] {
  const result: FlatNode[] = [
    { id: node.id, title: node.title, depth, kind: node.kind },
  ]
  for (const child of node.children || []) {
    result.push(...flattenTree(child, depth + 1))
  }
  return result
}

type BundlePagePickerProps = {
  selectedIds: string[]
  onChange: (ids: string[]) => void
}

export function BundlePagePicker({
  selectedIds,
  onChange,
}: BundlePagePickerProps) {
  const { t } = useTranslation('favoriteBundles')
  const tree = useTreeStore((s) => s.tree)
  const [search, setSearch] = useState('')

  const flatNodes = useMemo<FlatNode[]>(() => {
    if (!tree) return []
    const nodes: FlatNode[] = []
    for (const child of tree.children || []) {
      nodes.push(...flattenTree(child))
    }
    return nodes
  }, [tree])

  const filtered = useMemo(() => {
    const query = search.trim().toLowerCase()
    if (!query) return flatNodes
    return flatNodes.filter((n) => n.title.toLowerCase().includes(query))
  }, [flatNodes, search])

  const selected = useMemo(() => new Set(selectedIds), [selectedIds])

  const toggle = (id: string) => {
    if (selected.has(id)) {
      onChange(selectedIds.filter((existing) => existing !== id))
    } else {
      // Keep display order (tree order), not click order.
      onChange(
        flatNodes
          .filter(
            (n) =>
              n.kind === NODE_KIND_PAGE && (selected.has(n.id) || n.id === id),
          )
          .map((n) => n.id),
      )
    }
  }

  return (
    <div className="flex flex-col gap-2">
      <Input
        placeholder={t('admin.picker.searchPlaceholder')}
        value={search}
        aria-label={t('admin.picker.searchAriaLabel')}
        onChange={(e) => setSearch(e.target.value)}
      />
      <div className="custom-scrollbar max-h-56 overflow-y-auto rounded-md border">
        {filtered.length === 0 && (
          <p className="text-muted-foreground px-3 py-2 text-sm">
            {t('admin.picker.noPagesFound')}
          </p>
        )}
        <ul>
          {filtered.map((node) => {
            const isSection = node.kind === NODE_KIND_SECTION
            const Icon = isSection ? FolderTree : File
            return (
              <li key={node.id}>
                <label
                  className={cn(
                    'flex w-full items-center gap-2 py-2 pr-3 text-sm',
                    isSection
                      ? 'text-muted-foreground cursor-default'
                      : 'hover:bg-accent/60 cursor-pointer',
                  )}
                  style={{ paddingLeft: `${node.depth * 14 + 12}px` }}
                >
                  <Checkbox
                    checked={selected.has(node.id)}
                    disabled={isSection}
                    onCheckedChange={() => {
                      if (!isSection) toggle(node.id)
                    }}
                    aria-label={node.title}
                    data-testid={`bundle-page-picker-checkbox-${node.id}`}
                  />
                  <Icon className="h-4 w-4 shrink-0" />
                  <span>{node.title}</span>
                </label>
              </li>
            )
          })}
        </ul>
      </div>
      <p className="text-muted-foreground text-xs">
        {t('admin.picker.selectedCount', { count: selectedIds.length })}
      </p>
    </div>
  )
}
