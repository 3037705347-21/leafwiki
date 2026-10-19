package tree

import "fmt"

// WritePageRaw replaces the on-disk content of an existing page/section with
// raw markdown without going through frontmatter reserialization. Tree
// metadata (title, slug, order) is unaffected because node metadata lives
// outside the rewritten bytes. Used by batch maintenance that rewrites a
// surgically small span of the file.
func (t *TreeService) WritePageRaw(id, raw string) error {
	return t.withLockedTree(func() error {
		if t.tree == nil {
			return ErrTreeNotLoaded
		}
		node := t.getNodeByIDLocked(id)
		if node == nil {
			return ErrPageNotFound
		}
		if err := t.store.WriteRaw(node, raw); err != nil {
			return fmt.Errorf("could not write page raw content: %w", err)
		}
		return nil
	})
}
