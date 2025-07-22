package driver

import (
	"context"
	"strconv"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/scaleway/scaleway-csi/pkg/scaleway"
	block "github.com/scaleway/scaleway-sdk-go/api/block/v1"
	"github.com/scaleway/scaleway-sdk-go/scw"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"k8s.io/klog/v2"
	"oya.to/namedlocker"
)

const (
	// scwVolumeIDKey is the key for the volumeID in the publishContext.
	scwVolumeIDKey = DriverName + "/volume-id"
	// scwVolumeNameKey is the key for the volumeName in the publishContext.
	scwVolumeNameKey = DriverName + "/volume-name"
	// scwVolumeZoneKey is the key for the volumeZone in the publishContext.
	scwVolumeZoneKey = DriverName + "/volume-zone"

	// volumeTypeKey is the key of the volume type parameter.
	volumeTypeKey = "type"
	// encryptedKey is the key of the encrypted parameter.
	encryptedKey = "encrypted"
	// volumeIOPSKey is the key of the iops parameter.
	volumeIOPSKey = "iops"
)

var (
	// controllerCapabilities represents the capabilites of the controller.
	controllerCapabilities = []csi.ControllerServiceCapability_RPC_Type{
		csi.ControllerServiceCapability_RPC_CREATE_DELETE_VOLUME,
		csi.ControllerServiceCapability_RPC_PUBLISH_UNPUBLISH_VOLUME,
		csi.ControllerServiceCapability_RPC_LIST_VOLUMES,
		csi.ControllerServiceCapability_RPC_CREATE_DELETE_SNAPSHOT,
		csi.ControllerServiceCapability_RPC_LIST_SNAPSHOTS,
		csi.ControllerServiceCapability_RPC_EXPAND_VOLUME,
		csi.ControllerServiceCapability_RPC_GET_VOLUME,
		csi.ControllerServiceCapability_RPC_SINGLE_NODE_MULTI_WRITER,
		csi.ControllerServiceCapability_RPC_LIST_VOLUMES_PUBLISHED_NODES,
	}

	// supportedAccessModes represents the supported access modes for the Scaleway Block Volumes.
	supportedAccessModes = []csi.VolumeCapability_AccessMode_Mode{
		csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
		csi.VolumeCapability_AccessMode_SINGLE_NODE_READER_ONLY,
		csi.VolumeCapability_AccessMode_SINGLE_NODE_SINGLE_WRITER,
		csi.VolumeCapability_AccessMode_SINGLE_NODE_MULTI_WRITER,
	}
)

// controllerService implements csi.ControllerServer.
type controllerService struct {
	csi.UnimplementedControllerServer

	scaleway scaleway.Interface
	config   *DriverConfig
	// Volume locks ensures we don't run parallel operations on volumes (e.g.
	// detaching a volume and taking a snapshot).
	locks namedlocker.Store
}

func newControllerService(config *DriverConfig) (*controllerService, error) {
	scw, err := scaleway.New(UserAgent())
	if err != nil {
		return nil, err //nolint:wrapcheck
	}

	return &controllerService{
		config:   config,
		scaleway: scw,
	}, nil
}

// CreateVolume creates a new volume with the given CreateVolumeRequest.
// This function is idempotent
func (d *controllerService) CreateVolume(ctx context.Context, req *csi.CreateVolumeRequest) (*csi.CreateVolumeResponse, error) {
	klog.V(4).Infof("CreateVolume: called with %s", stripSecretFromReq(req))

	volumeName := req.GetName()
	if volumeName == "" {
		return nil, status.Error(codes.InvalidArgument, "name not provided")
	}

	if err := validateVolumeCapabilities(req.GetVolumeCapabilities(), false); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "volumeCapabilities not supported: %s", err)
	}

	perfIOPS, encrypted, err := parseCreateVolumeParams(req.GetParameters())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid parameters: %s:", err)
	}

	size, err := getVolumeRequestCapacity(req.GetCapacityRange())
	if err != nil {
		return nil, status.Errorf(codes.OutOfRange, "capacityRange invalid: %s", err)
	}

	scwVolumeName := d.config.Prefix + volumeName

	var snapshotID string
	var snapshotZone scw.Zone
	if req.GetVolumeContentSource() != nil {
		if _, ok := req.GetVolumeContentSource().GetType().(*csi.VolumeContentSource_Snapshot); !ok {
			return nil, status.Error(codes.InvalidArgument, "unsupported volumeContentSource type")
		}
		sourceSnapshot := req.GetVolumeContentSource().GetSnapshot()
		if sourceSnapshot == nil {
			return nil, status.Error(codes.Internal, "error retrieving snapshot from the volumeContentSource")
		}

		snapshotID, snapshotZone, err = ExtractIDAndZone(sourceSnapshot.GetSnapshotId())
		if err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "invalid parameter snapshotID: %s", err)
		}
	}

	chosenZones, err := chooseZones(req.GetAccessibilityRequirements(), snapshotZone)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "unable to choose zone from accessibilityRequirements: %s", err)
	}

	volume, err := d.getOrCreateVolume(ctx, scwVolumeName, snapshotID, size, perfIOPS, chosenZones)
	if err != nil {
		return nil, status.Errorf(codeFromScalewayError(err), "could not get or create volume: %s", err)
	}

	cv := csiVolume(volume)
	cv.VolumeContext = map[string]string{
		encryptedKey: strconv.FormatBool(encrypted),
	}

	return &csi.CreateVolumeResponse{
		Volume: cv,
	}, nil
}

// DeleteVolume deprovisions a volume.
// This operation MUST be idempotent.
func (d *controllerService) DeleteVolume(ctx context.Context, req *csi.DeleteVolumeRequest) (*csi.DeleteVolumeResponse, error) {
	klog.V(4).Infof("DeleteVolume called with %s", stripSecretFromReq(req))
	volumeID, volumeZone, err := ExtractIDAndZone(req.GetVolumeId())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid parameter volumeID: %s", err)
	}

	klog.V(4).Infof("Deleting volume with ID %s", volumeID)
	if err := d.scaleway.DeleteVolume(ctx, volumeID, volumeZone); err != nil {
		code := codeFromScalewayError(err)
		if code == codes.NotFound {
			klog.V(4).Infof("volume with ID %s not found", volumeID)
			return &csi.DeleteVolumeResponse{}, nil
		}

		return nil, status.Errorf(code, "failed to delete volume: %s", err)
	}

	klog.V(4).Infof("volume with ID %s deleted", volumeID)
	return &csi.DeleteVolumeResponse{}, nil
}

// ControllerPublishVolume perform the work that is necessary for making the volume available on the given node.
// This operation MUST be idempotent.
func (d *controllerService) ControllerPublishVolume(ctx context.Context, req *csi.ControllerPublishVolumeRequest) (*csi.ControllerPublishVolumeResponse, error) {
	klog.V(4).Infof("ControllerPublishVolume called with %s", stripSecretFromReq(req))

	volumeID, volumeZone, err := ExtractIDAndZone(req.GetVolumeId())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid parameter volumeID: %s", err)
	}

	nodeID, nodeZone, err := ExtractIDAndZone(req.GetNodeId())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid parameter nodeID: %s", err)
	}

	d.locks.Lock(volumeID)
	defer d.locks.Unlock(volumeID)

	if err := validateVolumeCapabilities([]*csi.VolumeCapability{req.GetVolumeCapability()}, false); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "volumeCapability not supported: %s", err)
	}

	volumeResp, err := d.scaleway.GetVolume(ctx, volumeID, volumeZone)
	if err != nil {
		return nil, status.Errorf(codeFromScalewayError(err), "unable to get volume to publish: %s", err)
	}

	server, err := d.scaleway.GetServer(ctx, nodeID, nodeZone)
	if err != nil {
		return nil, status.Errorf(codeFromScalewayError(err), "unable to get server where to publish the volume: %s", err)
	}

	publishContext := map[string]string{
		scwVolumeNameKey: volumeResp.Name,
		scwVolumeIDKey:   volumeResp.ID,
		scwVolumeZoneKey: volumeResp.Zone.String(),
	}

	// Is the volume already attached?
	if volumeServerIDs := publishedNodeIDs(volumeResp); len(volumeServerIDs) != 0 {
		if volumeServerIDs[0] == expandZonalID(server.ID, server.Zone) {
			return &csi.ControllerPublishVolumeResponse{
				PublishContext: publishContext,
			}, nil
		}

		return nil, status.Errorf(codes.FailedPrecondition, "volume %s already attached to another node %s", volumeID, volumeServerIDs[0])
	}

	if len(server.Volumes) == scaleway.MaxVolumesPerNode {
		return nil, status.Errorf(codes.ResourceExhausted, "max number of volumes reached for instance %s", nodeID)
	}

	if err := d.scaleway.AttachVolume(ctx, nodeID, volumeID, volumeZone); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to attach volume to instance: %s", err)
	}

	return &csi.ControllerPublishVolumeResponse{
		PublishContext: publishContext,
	}, nil
}

// ControllerUnpublishVolume is the reverse operation of ControllerPublishVolume
// This operation MUST be idempotent.
func (d *controllerService) ControllerUnpublishVolume(ctx context.Context, req *csi.ControllerUnpublishVolumeRequest) (*csi.ControllerUnpublishVolumeResponse, error) {
	klog.V(4).Infof("ControllerUnpublishVolume called with %s", stripSecretFromReq(req))

	volumeID, volumeZone, err := ExtractIDAndZone(req.GetVolumeId())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid parameter volumeID: %s", err)
	}

	nodeID, nodeZone, err := ExtractIDAndZone(req.GetNodeId())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid parameter nodeID: %s", err)
	}

	d.locks.Lock(volumeID)
	defer d.locks.Unlock(volumeID)

	volume, err := d.scaleway.GetVolume(ctx, volumeID, volumeZone)
	if err != nil {
		code := codeFromScalewayError(err)
		if code == codes.NotFound {
			return &csi.ControllerUnpublishVolumeResponse{}, nil
		}

		return nil, status.Errorf(code, "failed to get volume to unpublish: %s", err)
	}

	// Skip if volume is not attached.
	if len(publishedNodeIDs(volume)) == 0 {
		return &csi.ControllerUnpublishVolumeResponse{}, nil
	}

	if _, err := d.scaleway.GetServer(ctx, nodeID, nodeZone); err != nil {
		code := codeFromScalewayError(err)
		if code == codes.NotFound {
			return &csi.ControllerUnpublishVolumeResponse{}, nil
		}

		return nil, status.Errorf(code, "failed to get server where to unpublish volume: %s", err)
	}

	if err := d.scaleway.DetachVolume(ctx, volumeID, volumeZone); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to detach volume: %s", err)
	}

	return &csi.ControllerUnpublishVolumeResponse{}, nil
}

// ValidateVolumeCapabilities check if a pre-provisioned volume has all the capabilities
// that the CO wants. This RPC call SHALL return confirmed only if all the
// volume capabilities specified in the request are supported.
// This operation MUST be idempotent.
func (d *controllerService) ValidateVolumeCapabilities(ctx context.Context, req *csi.ValidateVolumeCapabilitiesRequest) (*csi.ValidateVolumeCapabilitiesResponse, error) {
	klog.V(4).Infof("ValidateVolumeCapabilities called with %s", stripSecretFromReq(req))
	volumeID, volumeZone, err := ExtractIDAndZone(req.GetVolumeId())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid parameter volumeID: %s", err)
	}

	if _, err = d.scaleway.GetVolume(ctx, volumeID, volumeZone); err != nil {
		return nil, status.Errorf(codeFromScalewayError(err), "failed to get volume: %s", err)
	}

	if err := validateVolumeCapabilities(req.GetVolumeCapabilities(), false); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "unsupported capabilities: %s", err)
	}

	return &csi.ValidateVolumeCapabilitiesResponse{
		Confirmed: &csi.ValidateVolumeCapabilitiesResponse_Confirmed{
			VolumeCapabilities: req.GetVolumeCapabilities(),
		},
	}, nil
}

// ListVolumes returns the list of the requested volumes
func (d *controllerService) ListVolumes(ctx context.Context, req *csi.ListVolumesRequest) (*csi.ListVolumesResponse, error) {
	klog.V(4).Infof("ListVolumes called with %s", stripSecretFromReq(req))

	start, err := parseStartingToken(req.GetStartingToken())
	if err != nil {
		return nil, status.Errorf(codes.Aborted, "invalid startingToken: %s", err)
	}

	if req.MaxEntries < 0 {
		return nil, status.Error(codes.InvalidArgument, "maxEntries must be a positive number")
	}

	volumes, next, err := d.scaleway.ListVolumes(ctx, start, uint32(req.MaxEntries))
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to list volumes: %s", err)
	}

	volumesEntries := make([]*csi.ListVolumesResponse_Entry, 0, len(volumes))
	for _, volume := range volumes {
		volumesEntries = append(volumesEntries, &csi.ListVolumesResponse_Entry{
			Volume: csiVolume(volume),
			Status: &csi.ListVolumesResponse_VolumeStatus{
				PublishedNodeIds: publishedNodeIDs(volume),
			},
		})
	}

	return &csi.ListVolumesResponse{
		Entries:   volumesEntries,
		NextToken: next,
	}, nil
}

// ControllerGetCapabilities returns the supported capabilities of controller service provided by the Plugin.
func (d *controllerService) ControllerGetCapabilities(ctx context.Context, req *csi.ControllerGetCapabilitiesRequest) (*csi.ControllerGetCapabilitiesResponse, error) {
	klog.V(4).Infof("ControllerGetCapabilities called with %s", stripSecretFromReq(req))

	capabilities := make([]*csi.ControllerServiceCapability, 0, len(controllerCapabilities))
	for _, capability := range controllerCapabilities {
		capabilities = append(capabilities, &csi.ControllerServiceCapability{
			Type: &csi.ControllerServiceCapability_Rpc{
				Rpc: &csi.ControllerServiceCapability_RPC{
					Type: capability,
				},
			},
		})
	}

	return &csi.ControllerGetCapabilitiesResponse{Capabilities: capabilities}, nil
}

// CreateSnapshot creates a snapshot of the given volume
func (d *controllerService) CreateSnapshot(ctx context.Context, req *csi.CreateSnapshotRequest) (*csi.CreateSnapshotResponse, error) {
	klog.V(4).Infof("CreateSnapshot called with %s", stripSecretFromReq(req))
	sourceVolumeID, sourceVolumeZone, err := ExtractIDAndZone(req.GetSourceVolumeId())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid parameter sourceVolumeID: %s", err)
	}

	d.locks.Lock(sourceVolumeID)
	defer d.locks.Unlock(sourceVolumeID)

	name := req.GetName()
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "name not provided")
	}

	snapshot, err := d.getOrCreateSnapshot(ctx, name, sourceVolumeID, sourceVolumeZone)
	if err != nil {
		return nil, status.Errorf(codeFromScalewayError(err), "unable to get or create snapshot: %s", err)
	}

	// Fail if the snapshot is not in an expected state.
	if snapshot.Status != block.SnapshotStatusAvailable && snapshot.Status != block.SnapshotStatusInUse {
		return nil, status.Errorf(codes.Internal, "snapshot %s has an unexpected status: %s", snapshot.ID, snapshot.Status)
	}

	return &csi.CreateSnapshotResponse{
		Snapshot: csiSnapshot(snapshot),
	}, nil
}

// DeleteSnapshot deletes the given snapshot
func (d *controllerService) DeleteSnapshot(ctx context.Context, req *csi.DeleteSnapshotRequest) (*csi.DeleteSnapshotResponse, error) {
	klog.V(4).Infof("DeleteSnapshot called with %s", stripSecretFromReq(req))
	snapshotID, snapshotZone, err := ExtractIDAndZone(req.GetSnapshotId())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid parameter snapshotID: %s", err)
	}

	if err := d.scaleway.DeleteSnapshot(ctx, snapshotID, snapshotZone); err != nil {
		code := codeFromScalewayError(err)
		if code == codes.NotFound {
			klog.V(4).Infof("snapshot with ID %s not found", snapshotID)
			return &csi.DeleteSnapshotResponse{}, nil
		}

		return nil, status.Errorf(code, "unable to delete snapshot: %s", err)
	}

	return &csi.DeleteSnapshotResponse{}, nil
}

// ListSnapshots return the information about all snapshots on the
// storage system within the given parameters regardless of how
// they were created. ListSnapshots SHALL NOT list a snapshot that
// is being created but has not been cut successfully yet.
func (d *controllerService) ListSnapshots(ctx context.Context, req *csi.ListSnapshotsRequest) (*csi.ListSnapshotsResponse, error) {
	klog.V(4).Infof("ListSnapshots called with %s", stripSecretFromReq(req))

	start, err := parseStartingToken(req.GetStartingToken())
	if err != nil {
		return nil, status.Errorf(codes.Aborted, "invalid startingToken: %s", err)
	}

	if req.MaxEntries < 0 {
		return nil, status.Error(codes.InvalidArgument, "maxEntries must be a positive number")
	}

	var snapshots []*block.Snapshot
	var next string

	switch {
	case req.GetSnapshotId() != "":
		snapshotID, snapshotZone, err := ExtractIDAndZone(req.GetSnapshotId())
		if err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "invalid parameter snapshotID: %s", err)
		}

		snapshot, err := d.scaleway.GetSnapshot(ctx, snapshotID, snapshotZone)
		if err != nil {
			// If volume doesn't exist, simply return an empty list.
			if code := codeFromScalewayError(err); code != codes.NotFound {
				return nil, status.Errorf(code, "failed to get snapshot %q: %s", req.GetSnapshotId(), err)
			}
		}

		if snapshot != nil {
			snapshots = append(snapshots, snapshot)
		}
	case req.GetSourceVolumeId() != "":
		sourceVolumeID, sourceVolumeZone, err := ExtractIDAndZone(req.GetSourceVolumeId())
		if err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "invalid parameter sourceVolumeID: %s", err)
		}

		s, n, err := d.scaleway.ListSnapshotsBySourceVolume(ctx, start, uint32(req.MaxEntries), sourceVolumeID, sourceVolumeZone)
		if err != nil {
			return nil, status.Errorf(codeFromScalewayError(err), "failed to get snapshots for volume %q: %s", req.GetSourceVolumeId(), err)
		}

		snapshots = append(snapshots, s...)
		next = n
	default:
		s, n, err := d.scaleway.ListSnapshots(ctx, start, uint32(req.MaxEntries))
		if err != nil {
			return nil, status.Errorf(codeFromScalewayError(err), "failed to list snapshots: %s", err)
		}

		snapshots = append(snapshots, s...)
		next = n
	}

	snapshotsEntries := make([]*csi.ListSnapshotsResponse_Entry, 0, len(snapshots))
	for _, snap := range snapshots {
		snapshotsEntries = append(snapshotsEntries, &csi.ListSnapshotsResponse_Entry{
			Snapshot: csiSnapshot(snap),
		})
	}

	return &csi.ListSnapshotsResponse{
		Entries:   snapshotsEntries,
		NextToken: next,
	}, nil
}

// ControllerExpandVolume expands the given volume
func (d *controllerService) ControllerExpandVolume(ctx context.Context, req *csi.ControllerExpandVolumeRequest) (*csi.ControllerExpandVolumeResponse, error) {
	klog.V(4).Infof("ControllerExpandVolume called with %s", stripSecretFromReq(req))
	volumeID, volumeZone, err := ExtractIDAndZone(req.GetVolumeId())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid parameter volumeID: %s", err)
	}

	d.locks.Lock(volumeID)
	defer d.locks.Unlock(volumeID)

	nodeExpansionRequired := true

	if volumeCapability := req.GetVolumeCapability(); volumeCapability != nil {
		block, _, err := validateVolumeCapability(volumeCapability)
		if err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "volumeCapabilities not supported: %s", err)
		}
		if block {
			nodeExpansionRequired = false
		}
	}

	volumeResp, err := d.scaleway.GetVolume(ctx, volumeID, volumeZone)
	if err != nil {
		return nil, status.Errorf(codeFromScalewayError(err), "failed to get volume that will be expanded: %s", err)
	}

	newSize, err := getVolumeRequestCapacity(req.GetCapacityRange())
	if err != nil {
		return nil, status.Errorf(codes.OutOfRange, "capacityRange invalid: %s", err)
	}

	if volumeSize := scwSizetoInt64(volumeResp.Size); volumeSize >= newSize {
		// Volume is already larger than or equal to the target capacity.
		return &csi.ControllerExpandVolumeResponse{CapacityBytes: volumeSize, NodeExpansionRequired: nodeExpansionRequired}, nil
	}

	if err = d.scaleway.ResizeVolume(ctx, volumeID, volumeZone, newSize); err != nil {
		return nil, status.Errorf(codeFromScalewayError(err), "failed to resize volume: %s", err)
	}

	return &csi.ControllerExpandVolumeResponse{CapacityBytes: newSize, NodeExpansionRequired: nodeExpansionRequired}, nil
}

// ControllerGetVolume gets a specific volume.
func (d *controllerService) ControllerGetVolume(ctx context.Context, req *csi.ControllerGetVolumeRequest) (*csi.ControllerGetVolumeResponse, error) {
	klog.V(4).Infof("ControllerGetVolume called with %s", stripSecretFromReq(req))
	volumeID, volumeZone, err := ExtractIDAndZone(req.GetVolumeId())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid parameter volumeID: %s", err)
	}

	volume, err := d.scaleway.GetVolume(ctx, volumeID, volumeZone)
	if err != nil {
		return nil, status.Errorf(codeFromScalewayError(err), "failed to get volume: %s", err)
	}

	return &csi.ControllerGetVolumeResponse{
		Volume: csiVolume(volume),
		Status: &csi.ControllerGetVolumeResponse_VolumeStatus{
			PublishedNodeIds: publishedNodeIDs(volume),
		},
	}, nil
}

// ControllerModifyVolume is not supported yet.
func (d *controllerService) ControllerModifyVolume(context.Context, *csi.ControllerModifyVolumeRequest) (*csi.ControllerModifyVolumeResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method ControllerModifyVolume not implemented")
}
