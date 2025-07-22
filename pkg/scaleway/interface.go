package scaleway

import (
	"context"

	block "github.com/scaleway/scaleway-sdk-go/api/block/v1"
	"github.com/scaleway/scaleway-sdk-go/api/instance/v1"
	"github.com/scaleway/scaleway-sdk-go/scw"
)

// Interface is the Scaleway wrapper interface.
type Interface interface {
	AttachVolume(ctx context.Context, serverID string, volumeID string, zone scw.Zone) error
	CreateSnapshot(ctx context.Context, name string, volumeID string, zone scw.Zone) (*block.Snapshot, error)
	CreateVolume(ctx context.Context, name string, snapshotID string, size int64, perfIOPS *uint32, zone scw.Zone) (*block.Volume, error)
	DeleteSnapshot(ctx context.Context, snapshotID string, zone scw.Zone) error
	DeleteVolume(ctx context.Context, volumeID string, zone scw.Zone) error
	DetachVolume(ctx context.Context, volumeID string, zone scw.Zone) error
	GetServer(ctx context.Context, serverID string, zone scw.Zone) (*instance.Server, error)
	GetSnapshot(ctx context.Context, snapshotID string, zone scw.Zone) (*block.Snapshot, error)
	GetSnapshotByName(ctx context.Context, name string, sourceVolumeID string, zone scw.Zone) (*block.Snapshot, error)
	GetVolume(ctx context.Context, volumeID string, zone scw.Zone) (*block.Volume, error)
	GetVolumeByName(ctx context.Context, name string, size scw.Size, zone scw.Zone) (*block.Volume, error)
	ListSnapshots(ctx context.Context, start uint32, max uint32) ([]*block.Snapshot, string, error)
	ListSnapshotsBySourceVolume(ctx context.Context, start uint32, max uint32, sourceVolumeID string, sourceVolumeZone scw.Zone) ([]*block.Snapshot, string, error)
	ListVolumes(ctx context.Context, start uint32, max uint32) ([]*block.Volume, string, error)
	ResizeVolume(ctx context.Context, volumeID string, zone scw.Zone, size int64) error
	WaitForSnapshot(ctx context.Context, snapshotID string, zone scw.Zone) (*block.Snapshot, error)
}
