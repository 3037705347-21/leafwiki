package favoritebundles

import (
	"context"
	"errors"
	"strings"
	"time"

	sharederrors "github.com/perber/wiki/internal/core/shared/errors"
	"github.com/perber/wiki/internal/core/tree"
	"github.com/perber/wiki/internal/favorites"
)

// validatePagesExist verifies every page id resolves in the tree. A missing
// page becomes a localized error carrying missingCode (400 when editing a
// bundle, 409 when syncing); any other lookup error passes through as a 5xx.
// The store layer cannot see the tree (architecture layers), so existence
// checks live here, the same way AddFavoriteUseCase validates before writing.
func validatePagesExist(treeService *tree.TreeService, pageIDs []string, missingCode string) error {
	if len(pageIDs) == 0 {
		return nil
	}
	_, errs := treeService.GetPages(pageIDs)
	for _, err := range errs {
		if err == nil {
			continue
		}
		if errors.Is(err, tree.ErrPageNotFound) {
			return sharederrors.NewLocalizedError(
				missingCode,
				"The reading list references a page that no longer exists",
				"the reading list references a page that no longer exists",
				err,
			)
		}
		return err
	}
	return nil
}

func hasDuplicatePageID(pageIDs []string) bool {
	seen := make(map[string]struct{}, len(pageIDs))
	for _, id := range pageIDs {
		if _, ok := seen[id]; ok {
			return true
		}
		seen[id] = struct{}{}
	}
	return false
}

// ─── CreateBundleUseCase ─────────────────────────────────────────────────────

type CreateBundleInput struct {
	Name        string
	Description string
	PageIDs     []string
	CreatedBy   string
}

type CreateBundleUseCase struct {
	treeService *tree.TreeService
	store       *favorites.FavoritesStore
}

func NewCreateBundleUseCase(treeService *tree.TreeService, store *favorites.FavoritesStore) *CreateBundleUseCase {
	return &CreateBundleUseCase{treeService: treeService, store: store}
}

func (uc *CreateBundleUseCase) Execute(_ context.Context, in CreateBundleInput) (*favorites.Bundle, error) {
	pageIDs := normalizePageIDs(in.PageIDs)
	ve := sharederrors.NewValidationErrors()
	if strings.TrimSpace(in.Name) == "" {
		ve.Add("name", "Name must not be empty")
	}
	for _, id := range in.PageIDs {
		if strings.TrimSpace(id) == "" {
			ve.Add("pageIds", "Page ids must not be empty")
			break
		}
	}
	if ve.HasErrors() {
		return nil, ve
	}
	if hasDuplicatePageID(pageIDs) {
		return nil, favorites.ErrDuplicateBundleEntry
	}
	if err := validatePagesExist(uc.treeService, pageIDs, ErrCodeBundleEntryPageMissing); err != nil {
		return nil, err
	}
	bundle, err := uc.store.CreateBundle(strings.TrimSpace(in.Name), in.Description, pageIDs, in.CreatedBy)
	if err != nil {
		return nil, err
	}
	return &bundle, nil
}

// ─── UpdateBundleUseCase ─────────────────────────────────────────────────────

type UpdateBundleInput struct {
	BundleID    string
	Name        string
	Description string
	PageIDs     []string
}

type UpdateBundleUseCase struct {
	treeService *tree.TreeService
	store       *favorites.FavoritesStore
}

func NewUpdateBundleUseCase(treeService *tree.TreeService, store *favorites.FavoritesStore) *UpdateBundleUseCase {
	return &UpdateBundleUseCase{treeService: treeService, store: store}
}

func (uc *UpdateBundleUseCase) Execute(_ context.Context, in UpdateBundleInput) error {
	pageIDs := normalizePageIDs(in.PageIDs)
	ve := sharederrors.NewValidationErrors()
	if strings.TrimSpace(in.BundleID) == "" {
		ve.Add("id", "Bundle id is required")
	}
	if strings.TrimSpace(in.Name) == "" {
		ve.Add("name", "Name must not be empty")
	}
	for _, id := range in.PageIDs {
		if strings.TrimSpace(id) == "" {
			ve.Add("pageIds", "Page ids must not be empty")
			break
		}
	}
	if ve.HasErrors() {
		return ve
	}
	if hasDuplicatePageID(pageIDs) {
		return favorites.ErrDuplicateBundleEntry
	}
	if err := validatePagesExist(uc.treeService, pageIDs, ErrCodeBundleEntryPageMissing); err != nil {
		return err
	}
	return uc.store.UpdateBundle(in.BundleID, strings.TrimSpace(in.Name), in.Description, pageIDs)
}

// ─── DeleteBundleUseCase ─────────────────────────────────────────────────────

type DeleteBundleInput struct {
	BundleID string
}

type DeleteBundleUseCase struct {
	store *favorites.FavoritesStore
}

func NewDeleteBundleUseCase(store *favorites.FavoritesStore) *DeleteBundleUseCase {
	return &DeleteBundleUseCase{store: store}
}

func (uc *DeleteBundleUseCase) Execute(_ context.Context, in DeleteBundleInput) error {
	return uc.store.DeleteBundle(in.BundleID)
}

// ─── queries ─────────────────────────────────────────────────────────────────

// BundleSummary is a bundle with its entries and the requesting user's
// subscription state — the single shape returned by the list/get endpoints.
type BundleSummary struct {
	Bundle       favorites.Bundle
	PageIDs      []string
	Subscribed   bool
	SubscribedAt *time.Time
	LastSyncedAt *time.Time
}

type ListBundlesInput struct {
	UserID string
}

type ListBundlesOutput struct {
	Bundles []BundleSummary
}

type ListBundlesUseCase struct {
	store *favorites.FavoritesStore
}

func NewListBundlesUseCase(store *favorites.FavoritesStore) *ListBundlesUseCase {
	return &ListBundlesUseCase{store: store}
}

func (uc *ListBundlesUseCase) Execute(_ context.Context, in ListBundlesInput) (*ListBundlesOutput, error) {
	bundles, err := uc.store.ListBundles()
	if err != nil {
		return nil, err
	}
	entries, err := uc.store.ListAllEntries()
	if err != nil {
		return nil, err
	}
	subs, err := uc.store.ListSubscriptionStates(in.UserID)
	if err != nil {
		return nil, err
	}

	entriesByBundle := make(map[string][]string, len(bundles))
	for _, e := range entries {
		entriesByBundle[e.BundleID] = append(entriesByBundle[e.BundleID], e.PageID)
	}

	out := &ListBundlesOutput{Bundles: make([]BundleSummary, 0, len(bundles))}
	for _, b := range bundles {
		pageIDs := entriesByBundle[b.ID]
		if pageIDs == nil {
			pageIDs = []string{}
		}
		summary := BundleSummary{Bundle: b, PageIDs: pageIDs}
		if sub, ok := subs[b.ID]; ok {
			summary.Subscribed = true
			subscribedAt := sub.SubscribedAt
			summary.SubscribedAt = &subscribedAt
			summary.LastSyncedAt = sub.LastSyncedAt
		}
		out.Bundles = append(out.Bundles, summary)
	}
	return out, nil
}

type GetBundleInput struct {
	UserID   string
	BundleID string
}

type GetBundleUseCase struct {
	store *favorites.FavoritesStore
}

func NewGetBundleUseCase(store *favorites.FavoritesStore) *GetBundleUseCase {
	return &GetBundleUseCase{store: store}
}

func (uc *GetBundleUseCase) Execute(_ context.Context, in GetBundleInput) (*BundleSummary, error) {
	bundle, entries, err := uc.store.GetBundle(in.BundleID)
	if err != nil {
		return nil, err
	}
	pageIDs := make([]string, 0, len(entries))
	for _, e := range entries {
		pageIDs = append(pageIDs, e.PageID)
	}
	summary := BundleSummary{Bundle: bundle, PageIDs: pageIDs}
	subs, err := uc.store.ListSubscriptionStates(in.UserID)
	if err != nil {
		return nil, err
	}
	if sub, ok := subs[in.BundleID]; ok {
		summary.Subscribed = true
		subscribedAt := sub.SubscribedAt
		summary.SubscribedAt = &subscribedAt
		summary.LastSyncedAt = sub.LastSyncedAt
	}
	return &summary, nil
}

// ─── sync / unsubscribe ──────────────────────────────────────────────────────

type SyncBundleInput struct {
	UserID   string
	BundleID string
}

type SyncBundleUseCase struct {
	treeService *tree.TreeService
	store       *favorites.FavoritesStore
}

func NewSyncBundleUseCase(treeService *tree.TreeService, store *favorites.FavoritesStore) *SyncBundleUseCase {
	return &SyncBundleUseCase{treeService: treeService, store: store}
}

// Execute subscribes the user to the bundle (implicit on first sync) and
// reconciles their favorites to the bundle's current entries. Every entry is
// validated against the tree first; one missing page fails the whole sync
// before the store transaction begins.
func (uc *SyncBundleUseCase) Execute(_ context.Context, in SyncBundleInput) (*favorites.SyncResult, error) {
	_, entries, err := uc.store.GetBundle(in.BundleID)
	if err != nil {
		return nil, err
	}
	pageIDs := make([]string, 0, len(entries))
	for _, e := range entries {
		pageIDs = append(pageIDs, e.PageID)
	}
	if err := validatePagesExist(uc.treeService, pageIDs, ErrCodeBundleSyncPageMissing); err != nil {
		return nil, err
	}
	result, err := uc.store.ReconcileBundleMembership(in.UserID, in.BundleID, pageIDs)
	if err != nil {
		return nil, err
	}
	return &result, nil
}

type UnsubscribeBundleInput struct {
	UserID   string
	BundleID string
}

type UnsubscribeBundleUseCase struct {
	store *favorites.FavoritesStore
}

func NewUnsubscribeBundleUseCase(store *favorites.FavoritesStore) *UnsubscribeBundleUseCase {
	return &UnsubscribeBundleUseCase{store: store}
}

func (uc *UnsubscribeBundleUseCase) Execute(_ context.Context, in UnsubscribeBundleInput) error {
	return uc.store.Unsubscribe(in.UserID, in.BundleID)
}

// normalizePageIDs trims whitespace and drops empties (the request binder
// rejects empties as validation errors; this also keeps store writes tidy).
func normalizePageIDs(ids []string) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		trimmed := strings.TrimSpace(id)
		if trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}
