package importer

import (
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/perber/wiki/internal/core/tree"
)

// ErrManagedConflictDuringExecution aborts a managed run when the pre-write
// re-check finds that the target page changed after the plan was produced
// (e.g. someone edited it during a long upload). Nothing is journaled for the
// item, so re-planning and re-running converges.
var ErrManagedConflictDuringExecution = errors.New("managed target changed between plan and execution")

// managedExecutor reconciles one collection against the plan: every item is
// idempotent and journaled in the collection manifest, so killing the process
// mid-batch and resuming converges to the same final state.
type managedExecutor struct {
	plan          *PlanResult
	planOptions   *PlanOptions
	assetMaxBytes int64
	wiki          ImporterWiki
	collections   *CollectionStore
	logger        *slog.Logger
	startIndex    int
	initialResult *ExecutionResult
	progressFn    func(ExecutionProgress, *ExecutionResult)
	cancelFn      func() bool
}

func (e *managedExecutor) WithProgressCallback(progressFn func(ExecutionProgress, *ExecutionResult)) PlanExecutor {
	e.progressFn = progressFn
	return e
}

func (e *managedExecutor) WithCancelCheck(cancelFn func() bool) PlanExecutor {
	e.cancelFn = cancelFn
	return e
}

func (e *managedExecutor) WithResumeState(startIndex int, initialResult *ExecutionResult) PlanExecutor {
	e.startIndex = startIndex
	e.initialResult = cloneExecutionResult(initialResult)
	return e
}

func (e *managedExecutor) Execute(userID string) (*ExecutionResult, error) {
	if e.plan.Blocked {
		return nil, ErrImportPlanBlocked
	}

	beforeExecution := e.wiki.TreeHash()
	expectedTreeHash := e.plan.TreeHash
	if e.startIndex > 0 {
		if e.initialResult == nil || e.initialResult.TreeHash == "" {
			return nil, fmt.Errorf("resume state missing tree hash")
		}
		expectedTreeHash = e.initialResult.TreeHash
	}
	if beforeExecution != expectedTreeHash {
		return nil, fmt.Errorf("plan is stale: expected tree_hash %s but got %s", expectedTreeHash, beforeExecution)
	}

	manifest, err := e.collections.BeginGeneration(
		e.planOptions.Collection, e.plan.ID, e.planOptions.TargetBasePath)
	if err != nil {
		return nil, err
	}

	result := e.initialResult
	if result == nil {
		result = &ExecutionResult{
			TreeHashBefore: beforeExecution,
			Items:          []ExecutionItemResult{},
		}
	}
	content := newManagedContent(e.plan, e.planOptions.SourceBasePath, e.assetMaxBytes, e.wiki)

	startedAt := time.Now().UTC()
	e.reportProgress(0, result, startedAt)

	for index := e.startIndex; index < len(e.plan.Items); index++ {
		if e.cancelFn != nil && e.cancelFn() {
			return result, ErrImportCanceled
		}
		item := e.plan.Items[index]
		e.reportProgress(index, result, startedAt)

		key := normalizePlanSourcePath(item.SourcePath)
		if manifest.IsCompleted(key) {
			// Crash replay: the item already converged; do not count or
			// record it twice.
			e.reportProgress(index+1, result, startedAt)
			continue
		}

		execItem, err := e.applyItem(userID, item, manifest, content, key)
		if err != nil {
			return result, err
		}
		result.Items = append(result.Items, execItem)
		switch execItem.Action {
		case ExecutionActionCreated:
			result.ImportedCount++
		case ExecutionActionUpdated:
			result.UpdatedCount++
		case ExecutionActionDeleted:
			result.DeletedCount++
		default:
			result.SkippedCount++
		}
		result.TreeHash = e.wiki.TreeHash()
		e.reportProgress(index+1, result, startedAt)
	}

	if err := e.collections.FinishGeneration(manifest); err != nil {
		return result, err
	}
	result.TreeHash = e.wiki.TreeHash()
	e.reportProgress(len(e.plan.Items), result, startedAt)
	return result, nil
}

// applyItem performs and journals one plan item. Any error aborts the batch.
func (e *managedExecutor) applyItem(
	userID string,
	item PlanItem,
	manifest *CollectionManifest,
	content *managedContent,
	key string,
) (ExecutionItemResult, error) {
	result := ExecutionItemResult{SourcePath: item.SourcePath, TargetPath: item.TargetPath}

	switch item.Action {
	case PlanActionSkip:
		// Unchanged sources and already-missing deletions are no-ops.
		result.Action = ExecutionActionSkipped
		return result, nil

	case PlanActionDelete:
		return e.applyDelete(userID, item, manifest, key, result)

	case PlanActionCreate:
		return e.applyCreate(userID, item, manifest, content, key, result)

	case PlanActionUpdate:
		return e.applyUpdate(userID, item, manifest, content, key, result)

	default:
		return result, fmt.Errorf("managed executor: unknown plan action %q", item.Action)
	}
}

func (e *managedExecutor) applyCreate(
	userID string,
	item PlanItem,
	manifest *CollectionManifest,
	content *managedContent,
	key string,
	result ExecutionItemResult,
) (ExecutionItemResult, error) {
	kind := item.Kind
	page, err := e.wiki.EnsurePath(userID, item.TargetPath, item.Title, &kind)
	if err != nil {
		return result, fmt.Errorf("ensure path %q: %w", item.TargetPath, err)
	}
	if page == nil {
		return result, fmt.Errorf("could not create page at %q", item.TargetPath)
	}

	transformed, err := content.WriteContent(userID, item.SourcePath, page)
	if err != nil {
		return result, err
	}
	updated, err := e.wiki.UpdatePage(userID, page.ID, item.Title, page.Slug, &transformed, &item.Kind)
	if err != nil {
		return result, fmt.Errorf("update created page %q: %w", item.TargetPath, err)
	}

	if err := e.verifyApplied(updated.ID, item, content, key); err != nil {
		return result, err
	}
	if err := e.journalApplied(manifest, key, item, updated.ID); err != nil {
		return result, err
	}
	result.Action = ExecutionActionCreated
	return result, nil
}

func (e *managedExecutor) applyUpdate(
	userID string,
	item PlanItem,
	manifest *CollectionManifest,
	content *managedContent,
	key string,
	result ExecutionItemResult,
) (ExecutionItemResult, error) {
	pageID := *item.ExistingID
	page, err := e.wiki.GetPageByID(pageID)
	if err != nil {
		// The page vanished after planning; refuse to recreate silently.
		return result, fmt.Errorf("%w: %s", ErrManagedConflictDuringExecution, item.TargetPath)
	}

	// Abort if the page was edited between plan and execution: the manifest
	// records the content the planner saw as clean; a different current
	// content means a concurrent edit could otherwise be overwritten.
	if applied, ok := manifest.Entries[key]; ok {
		current, fpErr := content.CurrentFingerprint(page, item.SourcePath)
		if fpErr != nil || current != applied.AppliedFingerprint {
			return result, fmt.Errorf("%w: %s", ErrManagedConflictDuringExecution, item.TargetPath)
		}
	}

	transformed, err := content.WriteContent(userID, item.SourcePath, page)
	if err != nil {
		return result, err
	}
	updated, err := e.wiki.UpdatePage(userID, page.ID, item.Title, page.Slug, &transformed, &item.Kind)
	if err != nil {
		return result, fmt.Errorf("update page %q: %w", item.TargetPath, err)
	}

	if err := e.verifyApplied(updated.ID, item, content, key); err != nil {
		return result, err
	}
	if err := e.journalApplied(manifest, key, item, updated.ID); err != nil {
		return result, err
	}
	result.Action = ExecutionActionUpdated
	return result, nil
}

func (e *managedExecutor) applyDelete(
	userID string,
	item PlanItem,
	manifest *CollectionManifest,
	key string,
	result ExecutionItemResult,
) (ExecutionItemResult, error) {
	page, err := e.wiki.GetPageByID(*item.ExistingID)
	if errors.Is(err, tree.ErrPageNotFound) {
		// Already gone (crash replay or concurrent removal): prune the
		// journal entry and report skip.
		if err := e.collections.MarkDeleted(manifest, key); err != nil {
			return result, err
		}
		result.Action = ExecutionActionSkipped
		return result, nil
	}
	if err != nil {
		return result, err
	}

	if err := e.wiki.DeleteManagedPage(userID, page.ID, page.Version()); err != nil {
		return result, fmt.Errorf("delete page %q: %w", item.TargetPath, err)
	}
	if err := e.collections.MarkDeleted(manifest, key); err != nil {
		return result, err
	}
	result.Action = ExecutionActionDeleted
	return result, nil
}

// verifyApplied re-reads the page and confirms the live fingerprint equals
// the planned desired fingerprint, so a silent transform/write mismatch is an
// error rather than a wrongly journaled page.
func (e *managedExecutor) verifyApplied(pageID string, item PlanItem, content *managedContent, _ string) error {
	page, err := e.wiki.GetPageByID(pageID)
	if err != nil {
		return fmt.Errorf("read back %q: %w", item.TargetPath, err)
	}
	current, err := content.CurrentFingerprint(page, item.SourcePath)
	if err != nil {
		return fmt.Errorf("fingerprint %q: %w", item.TargetPath, err)
	}
	if current != item.DesiredFingerprint {
		return fmt.Errorf("applied content for %q does not match planned fingerprint", item.TargetPath)
	}
	return nil
}

// journalApplied records the applied item and removes any stale entry that
// tracked the same page ID under another source key (source rename/adoption).
func (e *managedExecutor) journalApplied(manifest *CollectionManifest, key string, item PlanItem, pageID string) error {
	if oldKey, _, ok := manifestEntryByPageID(manifest, pageID); ok && oldKey != key {
		if err := e.collections.MarkDeleted(manifest, oldKey); err != nil {
			return err
		}
	}
	return e.collections.MarkCompleted(manifest, key, ManifestEntry{
		SourcePath:         item.SourcePath,
		TargetPath:         item.TargetPath,
		PageID:             pageID,
		Kind:               item.Kind,
		Title:              item.Title,
		AppliedFingerprint: item.DesiredFingerprint,
		AppliedAt:          time.Now().UTC(),
	})
}

func (e *managedExecutor) reportProgress(processed int, result *ExecutionResult, startedAt time.Time) {
	if e.progressFn == nil {
		return
	}
	started := startedAt
	e.progressFn(ExecutionProgress{
		ProcessedItems:        processed,
		TotalItems:            len(e.plan.Items),
		CurrentItemSourcePath: currentItemPath(e.plan, processed),
		StartedAt:             &started,
	}, result)
}

func currentItemPath(plan *PlanResult, processed int) *string {
	if processed < 0 || processed >= len(plan.Items) {
		return nil
	}
	p := plan.Items[processed].SourcePath
	return &p
}
