package favoritebundles

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	sharederrors "github.com/perber/wiki/internal/core/shared/errors"
	"github.com/perber/wiki/internal/favorites"
)

func TestBundleErrorStatus_Codes(t *testing.T) {
	t.Parallel()

	cases := []struct {
		code string
		want int
	}{
		{ErrCodeBundleInvalidRequest, http.StatusBadRequest},
		{ErrCodeBundleEntryPageMissing, http.StatusBadRequest},
		{ErrCodeBundleNotFound, http.StatusNotFound},
		{ErrCodeBundleDuplicateEntry, http.StatusConflict},
		{ErrCodeBundleSyncPageMissing, http.StatusConflict},
		{ErrCodeBundlePersonalConflict, http.StatusConflict},
		{ErrCodeBundleModified, http.StatusConflict},
		{favorites.ErrCodeFavoritesStoreUnavailable, http.StatusServiceUnavailable},
		{"anything_else", http.StatusInternalServerError},
	}
	for _, tc := range cases {
		if got := bundleErrorStatus(tc.code); got != tc.want {
			t.Errorf("bundleErrorStatus(%q) = %d, want %d", tc.code, got, tc.want)
		}
	}
}

func TestRespondWithBundleError_SentinelsAndUnknown(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		err  error
		want int
	}{
		{"not found", favorites.ErrBundleNotFound, http.StatusNotFound},
		{"duplicate", favorites.ErrDuplicateBundleEntry, http.StatusConflict},
		{"conflict", favorites.ErrPersonalFavoriteConflict, http.StatusConflict},
		{"modified", favorites.ErrBundleModified, http.StatusConflict},
		{"wrapped conflict", fmt.Errorf("sync failed: %w", favorites.ErrPersonalFavoriteConflict), http.StatusConflict},
		{"store unavailable", sharederrors.NewLocalizedError(favorites.ErrCodeFavoritesStoreUnavailable, "restoring", "suspended", nil), http.StatusServiceUnavailable},
		{"unknown", errors.New("boom"), http.StatusInternalServerError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)

			respondWithBundleError(c, tc.err)

			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d (body: %s)", rec.Code, tc.want, rec.Body.String())
			}
		})
	}
}
