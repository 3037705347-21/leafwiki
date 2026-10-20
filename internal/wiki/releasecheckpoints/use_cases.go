package wikireleasecheckpoints

import (
	"context"
	"time"

	"github.com/perber/wiki/internal/releasecheckpoints"
)

// ─── DTO types ──────────────────────────────────────────────────────────────

type ManifestEntryResponse struct {
	PagePath    string `json:"pagePath"`
	PageID      string `json:"pageId"`
	RevisionID  string `json:"revisionId"`
	ContentHash string `json:"contentHash"`
	CreatedAt   string `json:"createdAt"`
}

type ManifestVersionResponse struct {
	Name    string                  `json:"name"`
	Entries []ManifestEntryResponse `json:"entries"`
}

type GetManifestOutput struct {
	Versions []ManifestVersionResponse `json:"versions"`
}

type ReconcileVersionInput struct {
	Name  string   `json:"name"`
	Pages []string `json:"pages"`
}

type ReconcileInput struct {
	Versions []ReconcileVersionInput `json:"versions"`
}

// ─── GetManifestUseCase ─────────────────────────────────────────────────────

type GetManifestUseCase struct {
	service *releasecheckpoints.Service
}

func NewGetManifestUseCase(service *releasecheckpoints.Service) *GetManifestUseCase {
	return &GetManifestUseCase{service: service}
}

func (uc *GetManifestUseCase) Execute(_ context.Context) (*GetManifestOutput, error) {
	views, err := uc.service.GetManifest()
	if err != nil {
		return nil, err
	}
	out := &GetManifestOutput{Versions: make([]ManifestVersionResponse, 0, len(views))}
	for _, v := range views {
		resp := ManifestVersionResponse{Name: v.Name, Entries: make([]ManifestEntryResponse, 0, len(v.Entries))}
		for _, e := range v.Entries {
			resp.Entries = append(resp.Entries, ManifestEntryResponse{
				PagePath:    e.PagePath,
				PageID:      e.PageID,
				RevisionID:  e.RevisionID,
				ContentHash: e.ContentHash,
				CreatedAt:   formatTime(e.CreatedAt),
			})
		}
		out.Versions = append(out.Versions, resp)
	}
	return out, nil
}

// ─── ReconcileUseCase ───────────────────────────────────────────────────────

type ReconcileUseCase struct {
	service *releasecheckpoints.Service
}

func NewReconcileUseCase(service *releasecheckpoints.Service) *ReconcileUseCase {
	return &ReconcileUseCase{service: service}
}

func (uc *ReconcileUseCase) Execute(_ context.Context, in ReconcileInput) (releasecheckpoints.ReconcileResult, error) {
	manifest := releasecheckpoints.Manifest{
		Versions: make([]releasecheckpoints.ManifestVersion, 0, len(in.Versions)),
	}
	for _, v := range in.Versions {
		manifest.Versions = append(manifest.Versions, releasecheckpoints.ManifestVersion{
			Name:  v.Name,
			Pages: v.Pages,
		})
	}
	return uc.service.Reconcile(manifest)
}

func formatTime(ts time.Time) string {
	if ts.IsZero() {
		return ""
	}
	return ts.Format(time.RFC3339)
}
