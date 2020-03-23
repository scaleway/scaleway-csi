package driver

import (
	"errors"
)

var (
	errSchemeNotSupported = errors.New("scheme not supported for endpoint")
	errRemovingSocket     = errors.New("error removing existing socket")
	errProjectIDNotSet    = errors.New("projectID must be set")

	errTargetPathEmpty               = errors.New("target path empty")
	errTargetNotSharedMounter        = errors.New("target is not shared mounter")
	errTargetNotMounterOnRightDevice = errors.New("target is not mounted on the right device")
	errFsTypeEmpty                   = errors.New("filesystem type is empty")
	errDevicePathIsNotDevice         = errors.New("device path does not point on a block device")

	errVolumeCapabilitiesIsNil = errors.New("volume capabilites is nil")
	errVolumeCapabilityIsNil   = errors.New("volume capability is nil")
	errBothMountBlockVolumes   = errors.New("both mount and block volume type specified")
	errAccessModeNotSupported  = errors.New("access mode not supported")

	errLimitBytesLessThanRequiredBytes = errors.New("limit size is less than required size")
	errRequiredBytesLessThanMinimun    = errors.New("required size is less than the minimun size")
	errLimitBytesLessThanMinimum       = errors.New("limit size is less than the minimun size")
	errRequiredBytesGreaterThanMaximun = errors.New("required size is greater than the maximum size")
	errLimitBytesGreaterThanMaximum    = errors.New("limit size is greater than the maximum size")
)
