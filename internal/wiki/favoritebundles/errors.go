package favoritebundles

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	sharederrors "github.com/perber/wiki/internal/core/shared/errors"
	"github.com/perber/wiki/internal/favorites"
)

const (
	ErrCodeBundleInvalidRequest   = "favorite_bundle_invalid_request"
	ErrCodeBundleNotFound         = "favorite_bundle_not_found"
	ErrCodeBundleDuplicateEntry   = "favorite_bundle_duplicate_entry"
	ErrCodeBundleEntryPageMissing = "favorite_bundle_entry_page_missing"
	ErrCodeBundleSyncPageMissing  = "favorite_bundle_sync_page_missing"
	ErrCodeBundlePersonalConflict = "favorite_bundle_personal_conflict"
	ErrCodeBundleModified         = "favorite_bundle_modified"
	ErrCodeBundleInternalError    = "favorite_bundle_internal_error"
)

// BundleErrorResponse is the structured JSON error body returned by
// favorite-bundle endpoints, shaped like the api-key error response.
type BundleErrorResponse struct {
	Error BundleErrorDetail `json:"error"`
}

type BundleErrorDetail struct {
	Code     string   `json:"code"`
	Message  string   `json:"message"`
	Template string   `json:"template"`
	Args     []string `json:"args,omitempty"`
}

func respondWithBundleStatusError(c *gin.Context, status int, code, message, template string, args ...string) {
	c.JSON(status, BundleErrorResponse{
		Error: BundleErrorDetail{
			Code:     code,
			Message:  message,
			Template: template,
			Args:     append([]string(nil), args...),
		},
	})
}

// respondWithBundleError is the central error handler for favorite-bundle
// endpoints. Store-localized errors (e.g. favorites_store_unavailable) keep
// their code; store sentinels and use-case errors map below.
func respondWithBundleError(c *gin.Context, err error) {
	if loc, ok := sharederrors.AsLocalizedError(err); ok {
		respondWithBundleStatusError(c, bundleErrorStatus(loc.Code), loc.Code, loc.Message, loc.Template, loc.Args...)
		return
	}

	var vErr *sharederrors.ValidationErrors
	if errors.As(err, &vErr) {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":  "validation_error",
			"fields": vErr.Errors,
		})
		return
	}

	switch {
	case errors.Is(err, favorites.ErrBundleNotFound):
		respondWithBundleStatusError(c, http.StatusNotFound, ErrCodeBundleNotFound, "Reading list not found", "reading list not found")
	case errors.Is(err, favorites.ErrDuplicateBundleEntry):
		respondWithBundleStatusError(c, http.StatusConflict, ErrCodeBundleDuplicateEntry, "The same page appears more than once in the reading list", "the same page appears more than once in the reading list")
	case errors.Is(err, favorites.ErrPersonalFavoriteConflict):
		respondWithBundleStatusError(c, http.StatusConflict, ErrCodeBundlePersonalConflict, "One of the pages is already in your personal favorites; remove it there before syncing this list", "a listed page is already a personal favorite")
	case errors.Is(err, favorites.ErrBundleModified):
		respondWithBundleStatusError(c, http.StatusConflict, ErrCodeBundleModified, "The reading list changed while syncing; please try again", "the reading list changed while syncing, please retry")
	default:
		respondWithBundleStatusError(c, http.StatusInternalServerError, ErrCodeBundleInternalError, "Reading list request failed", "reading list request failed")
	}
}

func bundleErrorStatus(code string) int {
	switch code {
	case ErrCodeBundleNotFound:
		return http.StatusNotFound
	case ErrCodeBundleInvalidRequest, ErrCodeBundleEntryPageMissing:
		return http.StatusBadRequest
	case ErrCodeBundleDuplicateEntry, ErrCodeBundleSyncPageMissing, ErrCodeBundlePersonalConflict, ErrCodeBundleModified:
		return http.StatusConflict
	case favorites.ErrCodeFavoritesStoreUnavailable:
		return http.StatusServiceUnavailable
	default:
		return http.StatusInternalServerError
	}
}
