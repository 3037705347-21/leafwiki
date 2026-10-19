package tree

import (
	"fmt"
	"os"

	"github.com/perber/wiki/internal/core/shared"
)

// WriteRaw replaces the content file of an existing node with raw bytes,
// without parsing, reserializing or touching managed frontmatter. It is the
// write seam for batch maintenance (e.g. tag merge) that must preserve every
// byte apart from the span it computed. The target must already exist; no
// directories are created and the existing file mode is preserved.
func (f *NodeStore) WriteRaw(entry *PageNode, raw string) error {
	if entry == nil {
		return &InvalidOpError{Op: "WriteRaw", Reason: errEntryRequired}
	}

	filePath, err := f.contentPathForNodeRead(entry)
	if err != nil {
		return err
	}
	if !fileExists(filePath) {
		return &DriftError{NodeID: entry.ID, Kind: entry.Kind, Path: filePath, Reason: errExpectedPageMissing}
	}

	mode := os.FileMode(0o644)
	if st, statErr := os.Stat(filePath); statErr == nil {
		mode = st.Mode()
	}

	if err := shared.WriteFileAtomic(filePath, []byte(raw), mode); err != nil {
		return fmt.Errorf("could not write raw content: %w", err)
	}
	return nil
}
