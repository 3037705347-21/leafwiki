package wiki

import (
	"context"
	"errors"
	"fmt"

	sharederrors "github.com/perber/wiki/internal/core/shared/errors"
	coretagmerge "github.com/perber/wiki/internal/tagmerge"
	wikitagmerge "github.com/perber/wiki/internal/wiki/tagmerge"
)

// tagMergeChunkSize bounds how many pages are read into memory per batch.
const tagMergeChunkSize = 256

// launchTagMerge adds to reloadWG and starts runTagMerge in a goroutine.
// The caller is responsible for having already called tagMergeJob.Start().
func (w *Wiki) launchTagMerge() {
	w.reloadWG.Add(1)
	go func() {
		defer w.reloadWG.Done()
		w.runTagMerge(w.shutdownCtx)
	}()
}

// runTagMerge applies the current tag merge rules across all pages.
// reloadMu serializes it with a filesystem resync so the tags index is
// never rebuilt and rewritten concurrently.
//
// Guarantee: a rule set that does not compile (ambiguous alias or cycle)
// finishes before any file is touched. Per-file I/O errors are counted and
// the run continues; already-written pages are reindexed and a re-run
// converges from whatever point the failure left behind.
func (w *Wiki) runTagMerge(ctx context.Context) {
	job := w.tagMergeJob

	defer func() {
		if r := recover(); r != nil {
			w.log.Error("panic during tag merge", "panic", r)
			job.Finish(fmt.Errorf("panic during tag merge: %v", r))
		}
	}()

	w.reloadMu.Lock()
	defer w.reloadMu.Unlock()

	w.log.Info("tag merge started (async)")

	// Phase 1: compile. Conflict/cycle ⇒ finish without writing anything.
	job.SetPhase(wikitagmerge.PhaseCompile)
	cfg := w.tagMerge.Rules()
	compiled, err := coretagmerge.Compile(cfg)
	if err != nil {
		var ce *coretagmerge.CompileError
		if errors.As(err, &ce) {
			w.log.Warn("tag merge aborted: rules do not compile", "conflicts", len(ce.Conflicts), "cycles", len(ce.Cycles))
			job.FinishCompileError(ce)
			return
		}
		job.Finish(err)
		return
	}

	if err := ctx.Err(); err != nil {
		job.Finish(err)
		return
	}

	// Phase 2: scan, plan, write, reindex — all plans for a page complete in
	// memory before that page is written, and rule conflicts were eliminated
	// before the loop, so valid runs only ever move tags toward canonical.
	var ids []string
	if err := w.tree.WalkNodes(func(id string) error {
		ids = append(ids, id)
		return nil
	}); err != nil {
		job.Finish(err)
		return
	}
	job.SetPhase(wikitagmerge.PhaseApply)
	job.SetTotalPages(len(ids))

	for start := 0; start < len(ids); start += tagMergeChunkSize {
		if err := ctx.Err(); err != nil {
			job.Finish(err)
			return
		}
		end := start + tagMergeChunkSize
		if end > len(ids) {
			end = len(ids)
		}
		w.applyTagMergeChunk(ctx, job, compiled, ids[start:end])
	}

	if st := job.Status(); st.FailedPages > 0 {
		job.Finish(sharederrors.NewLocalizedError(
			wikitagmerge.ErrCodeInternal,
			fmt.Sprintf("Tag merge finished with %d failed page(s)", st.FailedPages),
			"tag merge finished with failed pages",
			fmt.Errorf("%d page(s) failed", st.FailedPages),
		))
		return
	}

	st := job.Status()
	w.log.Info("tag merge completed (async)", "scanned", st.ScannedPages, "changed", st.ChangedPages, "unsupported", st.UnsupportedPages)
	job.Finish(nil)
}

// applyTagMergeChunk reads, plans, writes and reindexes one page chunk.
func (w *Wiki) applyTagMergeChunk(ctx context.Context, job *wikitagmerge.MergeJob, compiled *coretagmerge.Compiled, ids []string) {
	pages, errs := w.tree.GetPages(ids)
	for i, page := range pages {
		if err := ctx.Err(); err != nil {
			job.Finish(err)
			return
		}
		if errs[i] != nil || page == nil {
			w.log.Warn("failed to load page during tag merge", "pageID", ids[i], "error", errs[i])
			job.AddScanned(1)
			job.AddFailed(1)
			continue
		}

		job.AddScanned(1)

		newRaw, changed, unsupported, err := coretagmerge.PlanTagsRewrite(page.RawContent, compiled)
		if err != nil {
			w.log.Error("tag merge planning failed", "pageID", page.ID, "error", err)
			job.AddFailed(1)
			continue
		}
		if unsupported {
			job.AddUnsupported(1)
			continue
		}
		if !changed {
			continue
		}

		if err := w.tree.WritePageRaw(page.ID, newRaw); err != nil {
			w.log.Warn("failed to write page during tag merge", "pageID", page.ID, "error", err)
			job.AddFailed(1)
			continue
		}
		// Reindex immediately so page tags and tag filters reflect the merge
		// as soon as the apply finishes. The excerpt derives from the body,
		// which the rewrite never touches.
		if err := w.tags.IndexPageContent(page.ID, newRaw); err != nil {
			w.log.Warn("failed to reindex tags after merge", "pageID", page.ID, "error", err)
			job.AddFailed(1)
			continue
		}
		job.AddChanged(1)
	}
}
