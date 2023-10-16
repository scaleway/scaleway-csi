package scaleway

import (
	"errors"
	"net/http"

	"github.com/scaleway/scaleway-sdk-go/scw"
)

var (
	// ErrVolumeNotFound is the error returned when the volume was not found when
	// getting by name.
	ErrVolumeNotFound = errors.New("volume not found")

	// ErrSnapshotNotFound is the error returned when the snapshot was not found
	// when getting by name.
	ErrSnapshotNotFound = errors.New("snapshot not found")

	// ErrSnapshotExists is returned when there is already a snapshot with the same
	// name but it has a different sourceVolumeID.
	ErrSnapshotExists = errors.New("snapshot exists but has a different sourceVolumeID")

	// ErrVolumeDifferentSize is returned when there is already a volume with the
	// same name but it has a different size.
	ErrVolumeDifferentSize = errors.New("volume already exists with a different size")
)

// IsNotFoundError returns true if the provided error is a ResourceNotFoundError.
func IsNotFoundError(err error) bool {
	var notFound *scw.ResourceNotFoundError
	return errors.As(err, &notFound)
}

// IsPreconditionFailedError returns true if the provided error is a PreconditionFailedError.
func IsPreconditionFailedError(err error) bool {
	var precondition *scw.PreconditionFailedError
	return errors.As(err, &precondition)
}

// IsGoneError returns true if the provided is an HTTP Gone error. Scaleway Block
// API returns this error when trying to get a resource that was deleted.
func IsGoneError(err error) bool {
	var internal *scw.ResponseError
	return errors.As(err, &internal) && internal.StatusCode == http.StatusGone
}
