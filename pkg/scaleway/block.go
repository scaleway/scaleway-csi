package scaleway

import (
	"context"
	"fmt"

	block "github.com/scaleway/scaleway-sdk-go/api/block/v1alpha1"
	"github.com/scaleway/scaleway-sdk-go/scw"
)

// InstanceServerProductResourceType is the ProductResourceType of an instance
// in the reference of a volume.
const InstanceServerProductResourceType = "instance_server"

// GetVolumeByName is a helper to find a volume by its name and size in the provided zone.
// It returns ErrVolumeDifferentSize if the volume does not have the expected size.
func (s *Scaleway) GetVolumeByName(ctx context.Context, name string, size scw.Size, zone scw.Zone) (*block.Volume, error) {
	volumesResp, err := s.block.ListVolumes(&block.ListVolumesRequest{
		Name: scw.StringPtr(name),
		Zone: zone,
	}, scw.WithContext(ctx), scw.WithAllPages())
	if err != nil {
		return nil, fmt.Errorf("failed to list volumes with provided name: %w", err)
	}

	for _, vol := range volumesResp.Volumes {
		if vol.Size != size {
			return nil, fmt.Errorf("%w: %s", ErrVolumeDifferentSize, vol.ID)
		}

		if vol.Name == name {
			return vol, nil
		}
	}

	return nil, ErrVolumeNotFound
}

// GetSnapshotByName is a helper to find a snapshot by its name, its sourceVolumeID and zone.
func (s *Scaleway) GetSnapshotByName(ctx context.Context, name string, sourceVolumeID string, zone scw.Zone) (*block.Snapshot, error) {
	snapshotsResp, err := s.block.ListSnapshots(&block.ListSnapshotsRequest{
		Name: scw.StringPtr(name),
		Zone: zone,
	}, scw.WithContext(ctx), scw.WithAllPages())
	if err != nil {
		return nil, fmt.Errorf("failed to list snapshots with provided name: %w", err)
	}

	for _, snap := range snapshotsResp.Snapshots {
		if snap.Name == name {
			if snap.ParentVolume != nil && snap.ParentVolume.ID != sourceVolumeID {
				return nil, ErrSnapshotExists
			}
			return snap, nil
		}
	}

	return nil, ErrSnapshotNotFound
}

// ListVolumes lists all volumes in all zones of the region where the driver is
// deployed. Results are paginated, use the returned token to fetch the next results.
func (s *Scaleway) ListVolumes(ctx context.Context, start, max uint32) ([]*block.Volume, string, error) {
	return paginatedList(func(page int32, pageSize uint32) ([]*block.Volume, error) {
		volumesResp, err := s.block.ListVolumes(&block.ListVolumesRequest{
			Page:     scw.Int32Ptr(page),
			PageSize: scw.Uint32Ptr(pageSize),
			Zone:     scw.ZoneFrPar1, // Do not remove this, it's needed for zones that are not part of the SDK.
		}, scw.WithContext(ctx), scw.WithZones(s.zones...))
		if err != nil {
			return nil, fmt.Errorf("failed to list volumes: %w", err)
		}

		return volumesResp.Volumes, nil
	}, start, max)
}

// ListVolumes lists all snapshots in all zones of the region where the driver is
// deployed. Results are paginated, use the returned token to fetch the next results.
func (s *Scaleway) ListSnapshots(ctx context.Context, start, max uint32) ([]*block.Snapshot, string, error) {
	return paginatedList(func(page int32, pageSize uint32) ([]*block.Snapshot, error) {
		snapshotsResp, err := s.block.ListSnapshots(&block.ListSnapshotsRequest{
			Page:     scw.Int32Ptr(page),
			PageSize: scw.Uint32Ptr(pageSize),
			Zone:     scw.ZoneFrPar1, // Do not remove this, it's needed for zones that are not part of the SDK.
		}, scw.WithContext(ctx), scw.WithZones(s.zones...))
		if err != nil {
			return nil, fmt.Errorf("failed to list snapshots: %w", err)
		}

		return snapshotsResp.Snapshots, nil
	}, start, max)
}

// ListVolumes lists all snapshots that match the specified sourceVolumeID in all
// zones of the region where the driver is deployed. Results are paginated, use
// the returned token to fetch the next results.
func (s *Scaleway) ListSnapshotsBySourceVolume(
	ctx context.Context,
	start, max uint32,
	sourceVolumeID string,
	sourceVolumeZone scw.Zone,
) ([]*block.Snapshot, string, error) {
	// Return nothing if sourceVolumeID is not a valid UUID.
	if !isValidUUID(sourceVolumeID) {
		return nil, "", nil
	}

	return paginatedList(func(page int32, pageSize uint32) ([]*block.Snapshot, error) {
		snapshotsResp, err := s.block.ListSnapshots(&block.ListSnapshotsRequest{
			Page:     scw.Int32Ptr(page),
			PageSize: scw.Uint32Ptr(pageSize),
			VolumeID: scw.StringPtr(sourceVolumeID),
			Zone:     sourceVolumeZone,
		}, scw.WithContext(ctx))
		if err != nil {
			return nil, fmt.Errorf("failed to list snapshots: %w", err)
		}

		return snapshotsResp.Snapshots, nil
	}, start, max)
}

// DeleteVolume deletes a volume by ID and zone.
func (s *Scaleway) DeleteVolume(ctx context.Context, volumeID string, zone scw.Zone) error {
	// Return not found if volumeID is not a valid UUID.
	if !isValidUUID(volumeID) {
		return &scw.ResourceNotFoundError{Resource: volumeResource, ResourceID: volumeID}
	}

	if err := s.block.DeleteVolume(&block.DeleteVolumeRequest{
		VolumeID: volumeID,
		Zone:     zone,
	}, scw.WithContext(ctx)); err != nil {
		return fmt.Errorf("failed to delete volume: %w", err)
	}

	return nil
}

// GetVolume gets an existing volume by ID and zone.
func (s *Scaleway) GetVolume(ctx context.Context, volumeID string, zone scw.Zone) (*block.Volume, error) {
	// Return not found if volumeID is not a valid UUID.
	if !isValidUUID(volumeID) {
		return nil, &scw.ResourceNotFoundError{Resource: volumeResource, ResourceID: volumeID}
	}

	volume, err := s.block.GetVolume(&block.GetVolumeRequest{
		VolumeID: volumeID,
		Zone:     zone,
	}, scw.WithContext(ctx))
	if err != nil {
		return nil, fmt.Errorf("failed to get volume: %w", err)
	}

	return volume, nil
}

// DeleteSnapshot deletes a snapshot by ID and zone.
func (s *Scaleway) DeleteSnapshot(ctx context.Context, snapshotID string, zone scw.Zone) error {
	// Return not found if snapshotID is not a valid UUID.
	if !isValidUUID(snapshotID) {
		return &scw.ResourceNotFoundError{Resource: snapshotResource, ResourceID: snapshotID}
	}

	if err := s.block.DeleteSnapshot(&block.DeleteSnapshotRequest{
		SnapshotID: snapshotID,
		Zone:       zone,
	}, scw.WithContext(ctx)); err != nil {
		return fmt.Errorf("failed to delete snapshot: %w", err)
	}

	return nil
}

// GetSnapshot returns the snapshot that has the provided ID and zone.
func (s *Scaleway) GetSnapshot(ctx context.Context, snapshotID string, zone scw.Zone) (*block.Snapshot, error) {
	// Return not found if snapshotID is not a valid UUID.
	if !isValidUUID(snapshotID) {
		return nil, &scw.ResourceNotFoundError{Resource: snapshotResource, ResourceID: snapshotID}
	}

	snapshot, err := s.block.GetSnapshot(&block.GetSnapshotRequest{
		SnapshotID: snapshotID,
		Zone:       zone,
	}, scw.WithContext(ctx))
	if err != nil {
		return nil, fmt.Errorf("failed to get snapshot: %w", err)
	}

	return snapshot, nil
}

// ResizeVolume updates the size of a volume. It waits until the volume is successfully
// resized.
func (s *Scaleway) ResizeVolume(ctx context.Context, volumeID string, zone scw.Zone, size int64) error {
	// Return not found if volumeID is not a valid UUID.
	if !isValidUUID(volumeID) {
		return &scw.ResourceNotFoundError{Resource: snapshotResource, ResourceID: volumeID}
	}

	if _, err := s.block.UpdateVolume(&block.UpdateVolumeRequest{
		VolumeID: volumeID,
		Zone:     zone,
		Size:     scw.SizePtr(scw.Size(size)),
	}, scw.WithContext(ctx)); err != nil {
		return fmt.Errorf("failed to update volume with new size: %w", err)
	}

	vol, err := s.block.WaitForVolume(&block.WaitForVolumeRequest{
		VolumeID: volumeID,
		Zone:     zone,
	}, scw.WithContext(ctx))
	if err != nil {
		return fmt.Errorf("wait for volume ended with an error: %w", err)
	}

	if vol.Status != block.VolumeStatusAvailable && vol.Status != block.VolumeStatusInUse {
		return fmt.Errorf("volume %s is in state %s", volumeID, vol.Status)
	}

	return nil
}

// CreateVolume creates a volume with the given parameters. If snapshotID is not
// empty, the size parameter is ignored and the volume is created from the snapshot.
// If perfIOPS is nil, the block API will decide how many iops are associated to the volume.
func (s *Scaleway) CreateVolume(ctx context.Context, name, snapshotID string, size int64, perfIOPS *uint32, zone scw.Zone) (*block.Volume, error) {
	req := &block.CreateVolumeRequest{
		Name:     name,
		PerfIops: perfIOPS,
		Zone:     zone,
	}

	if snapshotID != "" {
		if !isValidUUID(snapshotID) {
			return nil, &scw.ResourceNotFoundError{Resource: snapshotResource, ResourceID: snapshotID}
		}

		req.FromSnapshot = &block.CreateVolumeRequestFromSnapshot{
			SnapshotID: snapshotID,
			Size:       scw.SizePtr(scw.Size(size)),
		}
	} else {
		req.FromEmpty = &block.CreateVolumeRequestFromEmpty{
			Size: scw.Size(size),
		}
	}

	volume, err := s.block.CreateVolume(req, scw.WithContext(ctx))
	if err != nil {
		return nil, fmt.Errorf("failed to create volume: %w", err)
	}

	vol, err := s.block.WaitForVolume(&block.WaitForVolumeRequest{
		VolumeID: volume.ID,
		Zone:     zone,
	}, scw.WithContext(ctx))
	if err != nil {
		return nil, fmt.Errorf("wait for volume ended with an error: %w", err)
	}

	if vol.Status != block.VolumeStatusAvailable {
		return nil, fmt.Errorf("volume %s is in state %s", volume.ID, vol.Status)
	}

	return volume, nil
}

// CreateSnapshot creates a snapshot with the given parameters.
func (s *Scaleway) CreateSnapshot(ctx context.Context, name, volumeID string, zone scw.Zone) (*block.Snapshot, error) {
	// Return not found if volumeID is not a valid UUID.
	if !isValidUUID(volumeID) {
		return nil, &scw.ResourceNotFoundError{Resource: volumeResource, ResourceID: volumeID}
	}

	snapshot, err := s.block.CreateSnapshot(&block.CreateSnapshotRequest{
		Name:     name,
		VolumeID: volumeID,
		Zone:     zone,
	}, scw.WithContext(ctx))
	if err != nil {
		return nil, fmt.Errorf("failed to create snapshot: %w", err)
	}

	return snapshot, nil
}

// WaitForSnapshot waits for a snapshot to be in a terminal state.
func (s *Scaleway) WaitForSnapshot(ctx context.Context, snapshotID string, zone scw.Zone) (*block.Snapshot, error) {
	// Return not found if volumeID is not a valid UUID.
	if !isValidUUID(snapshotID) {
		return nil, &scw.ResourceNotFoundError{Resource: snapshotResource, ResourceID: snapshotID}
	}

	snap, err := s.block.WaitForSnapshot(&block.WaitForSnapshotRequest{
		SnapshotID: snapshotID,
		Zone:       zone,
	}, scw.WithContext(ctx))
	if err != nil {
		return nil, fmt.Errorf("wait for snapshot error: %w", err)
	}

	return snap, nil
}
