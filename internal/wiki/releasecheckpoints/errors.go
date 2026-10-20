package wikireleasecheckpoints

import (
	"net/http"

	"github.com/gin-gonic/gin"
	sharederrors "github.com/perber/wiki/internal/core/shared/errors"
	"github.com/perber/wiki/internal/releasecheckpoints"
)

// ErrorResponse is the structured JSON error body, mirroring the revisions
// slice's error shape.
type ErrorResponse struct {
	Error ErrorDetail `json:"error"`
}

type ErrorDetail struct {
	Code     string   `json:"code"`
	Message  string   `json:"message"`
	Template string   `json:"template"`
	Args     []string `json:"args,omitempty"`
}

func respondWithStatusError(c *gin.Context, status int, code, message, template string, args ...string) {
	c.JSON(status, ErrorResponse{
		Error: ErrorDetail{
			Code:     code,
			Message:  message,
			Template: template,
			Args:     append([]string(nil), args...),
		},
	})
}

func respondWithError(c *gin.Context, err error) {
	if localized, ok := sharederrors.AsLocalizedError(err); ok {
		respondWithStatusError(c, errorStatus(localized.Code), localized.Code, localized.Message, localized.Template, localized.Args...)
		return
	}
	respondWithStatusError(c, http.StatusInternalServerError, releasecheckpoints.ErrCodeInternal,
		"Release checkpoint request failed", "release checkpoint request failed")
}

func errorStatus(code string) int {
	switch code {
	case releasecheckpoints.ErrCodeInvalidPayload,
		releasecheckpoints.ErrCodeInvalidVersionName,
		releasecheckpoints.ErrCodeInvalidPagePath,
		releasecheckpoints.ErrCodeNameTooLong,
		releasecheckpoints.ErrCodePathTooLong,
		releasecheckpoints.ErrCodeDuplicateVersion,
		releasecheckpoints.ErrCodeDuplicatePage,
		releasecheckpoints.ErrCodeTooManyEntries,
		releasecheckpoints.ErrCodePageNotFound,
		releasecheckpoints.ErrCodeRevisionMissing:
		return http.StatusBadRequest
	case releasecheckpoints.ErrCodeDrift, releasecheckpoints.ErrCodeRevisionDisabled:
		return http.StatusConflict
	case releasecheckpoints.ErrCodeStoreUnavailable:
		return http.StatusServiceUnavailable
	default:
		return http.StatusInternalServerError
	}
}
