package tagmerge

import (
	"net/http"

	"github.com/gin-gonic/gin"
	sharederrors "github.com/perber/wiki/internal/core/shared/errors"
)

const (
	// ErrCodeInvalidRules marks a syntactically invalid rules payload (400).
	ErrCodeInvalidRules = "tag_merge_invalid_rules"
	// ErrCodeConflict marks rules that do not compile (ambiguous alias or
	// cycle); the batch changes nothing (409).
	ErrCodeConflict = "tag_merge_conflict"
	// ErrCodeAlreadyRunning marks a second apply while one is in flight (409).
	ErrCodeAlreadyRunning = "tag_merge_already_running"
	// ErrCodeInternal marks an unexpected failure (500).
	ErrCodeInternal = "tag_merge_internal_error"
)

// ErrAlreadyRunning is returned when an apply is triggered while one runs.
var ErrAlreadyRunning = sharederrors.NewLocalizedError(
	ErrCodeAlreadyRunning,
	"A tag merge is already in progress",
	"a tag merge is already in progress",
	nil,
)

type tagMergeErrorResponse struct {
	Error tagMergeErrorDetail `json:"error"`
}

type tagMergeErrorDetail struct {
	Code     string   `json:"code"`
	Message  string   `json:"message"`
	Template string   `json:"template"`
	Args     []string `json:"args,omitempty"`
}

func respondWithTagMergeError(c *gin.Context, err error) {
	if loc, ok := sharederrors.AsLocalizedError(err); ok {
		c.JSON(tagMergeErrorStatus(loc.Code), tagMergeErrorResponse{
			Error: tagMergeErrorDetail{
				Code:     loc.Code,
				Message:  loc.Message,
				Template: loc.Template,
				Args:     loc.Args,
			},
		})
		return
	}
	c.JSON(http.StatusInternalServerError, tagMergeErrorResponse{
		Error: tagMergeErrorDetail{
			Code:     ErrCodeInternal,
			Message:  "Tag merge request failed",
			Template: "tag merge request failed",
		},
	})
}

func respondWithTagMergeBadRequest(c *gin.Context, message string) {
	c.JSON(http.StatusBadRequest, tagMergeErrorResponse{
		Error: tagMergeErrorDetail{
			Code:     ErrCodeInvalidRules,
			Message:  message,
			Template: "invalid tag merge rules",
		},
	})
}

func tagMergeErrorStatus(code string) int {
	switch code {
	case ErrCodeInvalidRules:
		return http.StatusBadRequest
	case ErrCodeConflict, ErrCodeAlreadyRunning:
		return http.StatusConflict
	default:
		return http.StatusInternalServerError
	}
}
