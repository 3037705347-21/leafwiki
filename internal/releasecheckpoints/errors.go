package releasecheckpoints

import (
	"fmt"

	sharederrors "github.com/perber/wiki/internal/core/shared/errors"
)

const (
	ErrCodeInvalidPayload     = "release_checkpoints_invalid_payload"
	ErrCodeInvalidVersionName = "release_checkpoints_invalid_version_name"
	ErrCodeInvalidPagePath    = "release_checkpoints_invalid_page_path"
	ErrCodeNameTooLong        = "release_checkpoints_name_too_long"
	ErrCodePathTooLong        = "release_checkpoints_path_too_long"
	ErrCodeDuplicateVersion   = "release_checkpoints_duplicate_version"
	ErrCodeDuplicatePage      = "release_checkpoints_duplicate_page"
	ErrCodeTooManyEntries     = "release_checkpoints_too_many_entries"
	ErrCodePageNotFound       = "release_checkpoints_page_not_found"
	ErrCodeRevisionMissing    = "release_checkpoints_revision_missing"
	ErrCodeRevisionDisabled   = "release_checkpoints_revision_disabled"
	ErrCodeDrift              = "release_checkpoints_drift"
	ErrCodeStoreUnavailable   = "release_checkpoints_store_unavailable"
	ErrCodeInternal           = "release_checkpoints_internal_error"
)

func errInvalidVersionName(index string) error {
	return sharederrors.NewLocalizedError(
		ErrCodeInvalidVersionName,
		"A release version name is required",
		"release version name at index %s is empty",
		fmt.Errorf("empty version name at index %s", index),
		index,
	)
}

func errNameTooLong(name string) error {
	return sharederrors.NewLocalizedError(
		ErrCodeNameTooLong,
		"The release version name is too long",
		"release version name %q exceeds %d characters",
		fmt.Errorf("version name exceeds %d characters: %q", MaxVersionNameLen, name),
		name, fmt.Sprint(MaxVersionNameLen),
	)
}

func errInvalidPagePath(version, path string) error {
	return sharederrors.NewLocalizedError(
		ErrCodeInvalidPagePath,
		"A page path is invalid",
		"page path %q in release %q is invalid",
		fmt.Errorf("invalid page path %q in version %q", path, version),
		version, path,
	)
}

func errPathTooLong(version, path string) error {
	return sharederrors.NewLocalizedError(
		ErrCodePathTooLong,
		"A page path is too long",
		"page path %q in release %q exceeds %d characters",
		fmt.Errorf("page path exceeds %d characters: %q", MaxPagePathLen, path),
		version, path, fmt.Sprint(MaxPagePathLen),
	)
}

func errDuplicateVersion(name string) error {
	return sharederrors.NewLocalizedError(
		ErrCodeDuplicateVersion,
		"The release version is listed more than once",
		"release version %q is listed more than once in the manifest",
		fmt.Errorf("duplicate version name %q", name),
		name,
	)
}

func errDuplicatePage(version, path string) error {
	return sharederrors.NewLocalizedError(
		ErrCodeDuplicatePage,
		"A page is listed more than once in a release",
		"page %q is listed more than once in release %q",
		fmt.Errorf("duplicate page %q in version %q", path, version),
		version, path,
	)
}

func errTooManyEntries() error {
	return sharederrors.NewLocalizedError(
		ErrCodeTooManyEntries,
		"The release checklist is too large",
		"release checklist exceeds %d versions or %d page entries",
		fmt.Errorf("manifest exceeds limits: max %d versions, %d entries", MaxVersions, MaxTotalEntries),
		fmt.Sprint(MaxVersions), fmt.Sprint(MaxTotalEntries),
	)
}

func errPageNotFound(version, path string) error {
	return sharederrors.NewLocalizedError(
		ErrCodePageNotFound,
		"A page in the release checklist does not exist",
		"page %q in release %q does not exist",
		fmt.Errorf("page %q not found for version %q", path, version),
		version, path,
	)
}

func errRevisionMissing(version, path string) error {
	return sharederrors.NewLocalizedError(
		ErrCodeRevisionMissing,
		"A page in the release checklist has no revision history",
		"page %q in release %q has no saved revision to checkpoint",
		fmt.Errorf("no revision for page %q in version %q", path, version),
		version, path,
	)
}

func errRevisionDisabled() error {
	return sharederrors.NewLocalizedError(
		ErrCodeRevisionDisabled,
		"Revision history is disabled on this instance",
		"release checkpoints require revision history, which is disabled",
		fmt.Errorf("revision lookup is unavailable"),
	)
}

// errDrift is raised inside the reconcile transaction when a stored
// checkpoint pins a revision that is no longer the page's latest one.
func errDrift(version, path string) error {
	return sharederrors.NewLocalizedError(
		ErrCodeDrift,
		"The page has changed since this release was checkpointed",
		"page %q in release %q no longer matches the checkpointed revision; publish the changed content under a new release name",
		fmt.Errorf("drift for version %q page %q", version, path),
		version, path,
	)
}

func errStoreUnavailable() error {
	return sharederrors.NewLocalizedError(
		ErrCodeStoreUnavailable,
		"The server is restoring from a backup — please try again in a moment",
		"release checkpoint store is suspended for an in-progress restore",
		nil,
	)
}

func errInternal(cause error) error {
	return sharederrors.NewLocalizedError(
		ErrCodeInternal,
		"Release checkpoint request failed",
		"release checkpoint operation failed: %s",
		cause,
		errCauseString(cause),
	)
}

func errCauseString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
