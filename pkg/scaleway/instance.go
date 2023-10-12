package scaleway

import (
	"context"
	"fmt"

	block "github.com/scaleway/scaleway-sdk-go/api/block/v1alpha1"
	"github.com/scaleway/scaleway-sdk-go/api/instance/v1"
	"github.com/scaleway/scaleway-sdk-go/scw"
)

// GetServer returns the server associated to the provided serverID.
func (s *Scaleway) GetServer(ctx context.Context, serverID string, zone scw.Zone) (*instance.Server, error) {
	if !isValidUUID(serverID) {
		return nil, &scw.ResourceNotFoundError{Resource: serverResource, ResourceID: serverID}
	}

	resp, err := s.instance.GetServer(&instance.GetServerRequest{
		ServerID: serverID,
		Zone:     zone,
	}, scw.WithContext(ctx))
	if err != nil {
		return nil, fmt.Errorf("failed to get server: %w", err)
	}

	return resp.Server, nil
}

// AttachVolume attaches the provided volume to the specified server. It then
// waits for the volume to be effectively attached.
func (s *Scaleway) AttachVolume(ctx context.Context, serverID, volumeID string, zone scw.Zone) error {
	if !isValidUUID(serverID) {
		return &scw.ResourceNotFoundError{Resource: "server", ResourceID: serverID}
	}
	if !isValidUUID(volumeID) {
		return &scw.ResourceNotFoundError{Resource: "volume", ResourceID: volumeID}
	}

	if _, err := s.instance.AttachVolume(&instance.AttachVolumeRequest{
		Zone:     zone,
		ServerID: serverID,
		VolumeID: volumeID,
	}, scw.WithContext(ctx)); err != nil {
		return fmt.Errorf("failed to attach volume: %w", err)
	}

	if err := s.waitForVolumeAndReferences(
		ctx, volumeID, zone, block.VolumeStatusInUse, block.ReferenceStatusAttached,
	); err != nil {
		return err
	}

	return nil
}

// DetachVolume detaches the provided volume. It then waits for the volume to be
// effectively detached.
func (s *Scaleway) DetachVolume(ctx context.Context, volumeID string, zone scw.Zone) error {
	if _, err := s.instance.DetachVolume(&instance.DetachVolumeRequest{
		Zone:          zone,
		VolumeID:      volumeID,
		IsBlockVolume: scw.BoolPtr(true),
	}, scw.WithContext(ctx)); err != nil {
		return fmt.Errorf("failed to detach volume: %w", err)
	}

	if err := s.waitForVolumeAndReferences(
		ctx, volumeID, zone, block.VolumeStatusAvailable, block.ReferenceStatusDetached,
	); err != nil {
		return err
	}

	return nil

}

func (s *Scaleway) waitForVolumeAndReferences(
	ctx context.Context,
	volumeID string,
	zone scw.Zone,
	volumeStatus block.VolumeStatus,
	referenceStatus block.ReferenceStatus,
) error {
	volume, err := s.block.WaitForVolumeAndReferences(&block.WaitForVolumeAndReferencesRequest{
		VolumeID:                volumeID,
		Zone:                    zone,
		VolumeTerminalStatus:    &volumeStatus,
		ReferenceTerminalStatus: &referenceStatus,
	}, scw.WithContext(ctx))
	if err != nil {
		return fmt.Errorf("failed to wait for volume: %w", err)
	}

	if volume.Status != volumeStatus {
		return fmt.Errorf("volume is in state %s which is not expected", volume.Status)
	}

	for _, ref := range volume.References {
		if ref.Status != referenceStatus {
			return fmt.Errorf("volume reference %s is in state %s which is not expected", ref.ID, ref.Status)
		}
	}

	return nil
}

// GetLegacyVolume gets an Instance volume by its ID and zone.
func (s *Scaleway) GetLegacyVolume(ctx context.Context, volumeID string, zone scw.Zone) (*instance.Volume, error) {
	resp, err := s.instance.GetVolume(&instance.GetVolumeRequest{
		VolumeID: volumeID,
		Zone:     zone,
	}, scw.WithContext(ctx))
	if err != nil {
		return nil, fmt.Errorf("failed to get legacy volume: %w", err)
	}

	return resp.Volume, nil
}
