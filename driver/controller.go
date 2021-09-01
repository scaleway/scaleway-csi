package driver

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/golang/protobuf/ptypes"
	"github.com/scaleway/scaleway-csi/scaleway"
	"github.com/scaleway/scaleway-sdk-go/api/instance/v1"
	"github.com/scaleway/scaleway-sdk-go/scw"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"k8s.io/klog"
)

var (
	// controllerCapabilities represents the capabilites of the Scaleway Block Volumes
	controllerCapabilities = []csi.ControllerServiceCapability_RPC_Type{
		csi.ControllerServiceCapability_RPC_CREATE_DELETE_VOLUME,
		csi.ControllerServiceCapability_RPC_PUBLISH_UNPUBLISH_VOLUME,
		csi.ControllerServiceCapability_RPC_LIST_VOLUMES,
		csi.ControllerServiceCapability_RPC_CREATE_DELETE_SNAPSHOT,
		csi.ControllerServiceCapability_RPC_LIST_SNAPSHOTS,
		csi.ControllerServiceCapability_RPC_EXPAND_VOLUME,
	}

	// supportedAccessModes represents the supported access modes for the Scaleway Block Volumes
	supportedAccessModes = []csi.VolumeCapability_AccessMode{
		csi.VolumeCapability_AccessMode{
			Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
		},
	}

	scwVolumeID   = DriverName + "/volume-id"
	scwVolumeName = DriverName + "/volume-name"
	scwVolumeZone = DriverName + "/volume-zone"

	volumeTypeKey = "type"
	encryptedKey  = "encrypted"
)

type controllerService struct {
	scaleway *scaleway.Scaleway
	config   *DriverConfig
	mux      sync.Mutex
}

func newControllerService(config *DriverConfig) controllerService {
	userAgent := fmt.Sprintf("%s %s (%s)", DriverName, driverVersion, gitCommit)
	if extraUA := os.Getenv(ExtraUserAgentEnv); extraUA != "" {
		userAgent = userAgent + " " + extraUA
	}

	return controllerService{
		config:   config,
		scaleway: scaleway.NewScaleway(userAgent),
	}
}

// CreateVolume creates a new volume with the given CreateVolumeRequest.
// This function is idempotent
func (d *controllerService) CreateVolume(ctx context.Context, req *csi.CreateVolumeRequest) (*csi.CreateVolumeResponse, error) {
	klog.V(4).Infof("CreateVolume: called with %s", stripSecretFromReq(*req))

	volumeName := req.GetName()
	if volumeName == "" {
		return nil, status.Error(codes.InvalidArgument, "name not provided")
	}

	volumeCapabilities := req.GetVolumeCapabilities()
	if len(volumeCapabilities) == 0 {
		return nil, status.Error(codes.InvalidArgument, "volumeCapabilities not provided")
	}

	err := validateVolumeCapabilities(volumeCapabilities)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "volumeCapabilities not supported: %s", err)
	}

	encrypted := false

	volumeType := scaleway.DefaultVolumeType
	for key, value := range req.GetParameters() {
		switch strings.ToLower(key) {
		case volumeTypeKey:
			volumeType = instance.VolumeVolumeType(value)
		case encryptedKey:
			encryptedValue, err := strconv.ParseBool(value)
			if err != nil {
				return nil, status.Errorf(codes.InvalidArgument, "invalid bool value (%s) for parameter %s: %v", value, key, err)
			}
			// TODO check if this value has changed?
			encrypted = encryptedValue
		default:
			return nil, status.Errorf(codes.InvalidArgument, "invalid parameter key %s", key)
		}
	}

	minSize, maxSize, err := d.scaleway.GetVolumeLimits(string(volumeType))
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	size, err := getVolumeRequestCapacity(minSize, maxSize, req.GetCapacityRange())
	if err != nil {
		return nil, status.Errorf(codes.OutOfRange, "capacityRange invalid: %s", err)
	}

	scwVolumeName := d.config.Prefix + volumeName
	// TODO check all zones
	volume, err := d.scaleway.GetVolumeByName(scwVolumeName, size, volumeType)
	if err != nil {
		switch err {
		case scaleway.ErrVolumeNotFound: // all good
		case scaleway.ErrDifferentSize:
			return nil, status.Error(codes.AlreadyExists, err.Error())
		case scaleway.ErrMultipleVolumes:
			return nil, status.Error(codes.Internal, err.Error())
		default:
			return nil, status.Error(codes.Internal, err.Error())
		}
	} else { // volume exists
		return &csi.CreateVolumeResponse{
			Volume: &csi.Volume{
				VolumeId:           volume.Zone.String() + "/" + volume.ID,
				CapacityBytes:      int64(volume.Size),
				AccessibleTopology: newAccessibleTopology(volume.Zone),
				VolumeContext: map[string]string{
					encryptedKey: strconv.FormatBool(encrypted),
				},
			},
		}, nil
	}

	var contentSource *csi.VolumeContentSource
	var snapshotID *string
	var snapshotZone scw.Zone
	if req.GetVolumeContentSource() != nil {
		if _, ok := req.GetVolumeContentSource().GetType().(*csi.VolumeContentSource_Snapshot); !ok {
			return nil, status.Error(codes.InvalidArgument, "unsupported volumeContentSource type")
		}
		sourceSnapshot := req.GetVolumeContentSource().GetSnapshot()
		if sourceSnapshot == nil {
			return nil, status.Error(codes.Internal, "error retrieving snapshot from the volumeContentSource")
		}

		sourceSnapshotID, sourceSnapshotZone, err := getSnapshotIDAndZone(sourceSnapshot.GetSnapshotId())
		if err != nil {
			return nil, err
		}

		// TODO check all zones
		snapshotResp, err := d.scaleway.GetSnapshot(&instance.GetSnapshotRequest{
			SnapshotID: sourceSnapshotID,
			Zone:       sourceSnapshotZone,
		})
		if err != nil {
			if _, ok := err.(*scw.ResourceNotFoundError); ok {
				return nil, status.Errorf(codes.NotFound, "snapshot %s not found", sourceSnapshotID)
			}
			return nil, status.Error(codes.Internal, err.Error())
		}
		snapshotID = &sourceSnapshotID
		snapshotZone = snapshotResp.Snapshot.Zone
		contentSource = &csi.VolumeContentSource{
			Type: &csi.VolumeContentSource_Snapshot{
				Snapshot: &csi.VolumeContentSource_SnapshotSource{
					SnapshotId: scaleway.ExpandSnapshotID(snapshotResp.Snapshot),
				},
			},
		}
	}

	chosenZones, err := chooseZones(req.GetAccessibilityRequirements(), snapshotZone)
	if err != nil {
		return nil, err
	}
	if len(chosenZones) == 0 {
		chosenZones = append(chosenZones, scw.Zone("")) // this will use the default zone of the client
	}

	volumeSize := scw.Size(size)
	volumeRequest := &instance.CreateVolumeRequest{
		Name:       scwVolumeName,
		VolumeType: volumeType,
	}
	if contentSource != nil {
		volumeRequest.BaseSnapshot = snapshotID
	} else {
		volumeRequest.Size = &volumeSize
	}

	if len(chosenZones) == 1 { // either it's with an empty zone, the snapshot zone, or just one classic zone
		if chosenZones[0] != scw.Zone("") {
			volumeRequest.Zone = chosenZones[0]
		}
		volumeResp, err := d.scaleway.CreateVolume(volumeRequest)
		if err != nil {
			if _, ok := err.(*scw.ResourceNotFoundError); ok {
				return nil, status.Error(codes.NotFound, err.Error())
			}
			return nil, status.Error(codes.Internal, err.Error())
		}
		segments := map[string]string{
			ZoneTopologyKey: string(volumeResp.Volume.Zone),
		}

		return &csi.CreateVolumeResponse{
			Volume: &csi.Volume{
				VolumeId:      volumeResp.Volume.Zone.String() + "/" + volumeResp.Volume.ID,
				ContentSource: contentSource,
				CapacityBytes: int64(volumeResp.Volume.Size),
				AccessibleTopology: []*csi.Topology{
					{
						Segments: segments,
					},
				},
				VolumeContext: map[string]string{
					encryptedKey: strconv.FormatBool(encrypted),
				},
			},
		}, nil
	}

	var errors []string
	for _, zone := range chosenZones { // if we multiple wanted zone, we try each one
		volumeRequest.Zone = zone
		volumeResp, err := d.scaleway.CreateVolume(volumeRequest)
		if err != nil {
			errors = append(errors, err.Error())
			continue
		}

		segments := map[string]string{
			ZoneTopologyKey: string(volumeResp.Volume.Zone),
		}

		return &csi.CreateVolumeResponse{
			Volume: &csi.Volume{
				VolumeId:      volumeResp.Volume.Zone.String() + "/" + volumeResp.Volume.ID,
				ContentSource: contentSource,
				CapacityBytes: int64(volumeResp.Volume.Size),
				AccessibleTopology: []*csi.Topology{
					{
						Segments: segments,
					},
				},
				VolumeContext: map[string]string{
					encryptedKey: strconv.FormatBool(encrypted),
				},
			},
		}, nil
	}

	// here errors is not empty
	return nil, status.Errorf(codes.Internal, "multiple error while trying different zones: %s", strings.Join(errors, "; "))
}

// DeleteVolume deprovision a volume.
// This operation MUST be idempotent.
func (d *controllerService) DeleteVolume(ctx context.Context, req *csi.DeleteVolumeRequest) (*csi.DeleteVolumeResponse, error) {
	klog.V(4).Infof("DeleteVolume called with %s", stripSecretFromReq(*req))
	volumeID, volumeZone, err := getVolumeIDAndZone(req.GetVolumeId())
	if err != nil {
		return nil, err
	}

	volumeResp, err := d.scaleway.GetVolume(&instance.GetVolumeRequest{
		VolumeID: volumeID,
		Zone:     volumeZone,
	})
	if err != nil {
		if _, ok := err.(*scw.ResourceNotFoundError); ok {
			klog.V(4).Infof("volume with ID %s not found", volumeID)
			return &csi.DeleteVolumeResponse{}, nil
		}

		return nil, status.Error(codes.Internal, err.Error())
	}
	if volumeResp.Volume.Server != nil {
		return nil, status.Error(codes.FailedPrecondition, "volume is still atached to a server")
	}

	klog.V(4).Infof("deleting volume with ID %s", volumeID)
	err = d.scaleway.DeleteVolume(&instance.DeleteVolumeRequest{
		VolumeID: volumeResp.Volume.ID,
		Zone:     volumeResp.Volume.Zone,
	})
	if err != nil {
		if _, ok := err.(*scw.ResourceNotFoundError); ok {
			klog.V(4).Infof("volume with ID %s not found", volumeID)
			return &csi.DeleteVolumeResponse{}, nil
		}

		return nil, status.Error(codes.Internal, err.Error())
	}
	klog.V(4).Infof("volume with ID %s deleted", volumeID)
	return &csi.DeleteVolumeResponse{}, nil
}

// ControllerPublishVolume perform the work that is necessary for making the volume available on the given node.
// This operation MUST be idempotent.
func (d *controllerService) ControllerPublishVolume(ctx context.Context, req *csi.ControllerPublishVolumeRequest) (*csi.ControllerPublishVolumeResponse, error) {
	klog.V(4).Infof("ControllerPublishVolume called with %s", stripSecretFromReq(*req))

	volumeID, volumeZone, err := getVolumeIDAndZone(req.GetVolumeId())
	if err != nil {
		return nil, err
	}

	nodeID, nodeZone, err := getNodeIDAndZone(req.GetNodeId())
	if err != nil {
		return nil, err
	}

	volumeCapability := req.GetVolumeCapability()
	if volumeCapability == nil {
		return nil, status.Error(codes.InvalidArgument, "volumeCapability is not provided")
	}

	err = validateVolumeCapabilities([]*csi.VolumeCapability{volumeCapability})
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "volumeCapability not supported: %s", err)
	}

	volumeResp, err := d.scaleway.GetVolume(&instance.GetVolumeRequest{
		VolumeID: volumeID,
		Zone:     volumeZone,
	})
	if err != nil {
		if _, ok := err.(*scw.ResourceNotFoundError); ok {
			return nil, status.Errorf(codes.NotFound, "volume %s not found", volumeID)
		}
		return nil, status.Error(codes.Internal, err.Error())
	}

	serverResp, err := d.scaleway.GetServer(&instance.GetServerRequest{
		ServerID: nodeID,
		Zone:     nodeZone,
	})
	if err != nil {
		if _, ok := err.(*scw.ResourceNotFoundError); ok {
			return nil, status.Errorf(codes.NotFound, "instance %s not found", volumeID)
		}
		return nil, status.Error(codes.Internal, err.Error())
	}

	if volumeResp.Volume.Server != nil && volumeResp.Volume.Server.ID != serverResp.Server.ID {
		return nil, status.Errorf(codes.FailedPrecondition, "volume %s already attached to another node %s", volumeID, volumeResp.Volume.Server.ID)
	} else if volumeResp.Volume.Server != nil {
		return &csi.ControllerPublishVolumeResponse{
			PublishContext: map[string]string{
				scwVolumeName: volumeResp.Volume.Name,
				scwVolumeID:   volumeResp.Volume.ID,
				scwVolumeZone: volumeResp.Volume.Zone.String(),
			},
		}, nil
	}

	volumesCount := len(serverResp.Server.Volumes)

	if volumesCount == maxVolumesPerNode {
		return nil, status.Error(codes.ResourceExhausted, "max number of volumes for this instance")
	}

	if volumeResp.Volume.Zone != serverResp.Server.Zone {
		return nil, status.Error(codes.InvalidArgument, "volume and node are not in the same zone")
	}

	d.mux.Lock()
	defer d.mux.Unlock()
	_, err = d.scaleway.AttachVolume(&instance.AttachVolumeRequest{
		ServerID: nodeID,
		VolumeID: volumeID,
		Zone:     volumeResp.Volume.Zone,
	})
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	return &csi.ControllerPublishVolumeResponse{
		PublishContext: map[string]string{
			scwVolumeName: volumeResp.Volume.Name,
			scwVolumeID:   volumeResp.Volume.ID,
			scwVolumeZone: volumeResp.Volume.Zone.String(),
		},
	}, nil
}

// ControllerUnpublishVolume is the reverse operation of ControllerPublishVolume
// This operation MUST be idempotent.
func (d *controllerService) ControllerUnpublishVolume(ctx context.Context, req *csi.ControllerUnpublishVolumeRequest) (*csi.ControllerUnpublishVolumeResponse, error) {
	klog.V(4).Infof("ControllerUnpublishVolume called with %s", stripSecretFromReq(*req))

	volumeID, volumeZone, err := getVolumeIDAndZone(req.GetVolumeId())
	if err != nil {
		return nil, err
	}

	nodeID, nodeZone, err := getNodeIDAndZone(req.GetNodeId())
	if err != nil {
		return nil, err
	}

	volumeResp, err := d.scaleway.GetVolume(&instance.GetVolumeRequest{
		VolumeID: volumeID,
		Zone:     volumeZone,
	})
	if err != nil {
		if _, ok := err.(*scw.ResourceNotFoundError); ok {
			return &csi.ControllerUnpublishVolumeResponse{}, nil
		}
		return nil, status.Error(codes.Internal, err.Error())
	}

	if volumeResp.Volume.Server == nil {
		return &csi.ControllerUnpublishVolumeResponse{}, nil
	}

	_, err = d.scaleway.GetServer(&instance.GetServerRequest{
		ServerID: nodeID,
		Zone:     nodeZone,
	})
	if err != nil {
		if _, ok := err.(*scw.ResourceNotFoundError); ok {
			return &csi.ControllerUnpublishVolumeResponse{}, nil
		}
		return nil, status.Error(codes.Internal, err.Error())
	}

	d.mux.Lock()
	defer d.mux.Unlock()
	_, err = d.scaleway.DetachVolume(&instance.DetachVolumeRequest{
		VolumeID: volumeID,
		Zone:     volumeResp.Volume.Zone,
	})
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	return &csi.ControllerUnpublishVolumeResponse{}, nil
}

// ValidateVolumeCapabilities check if a pre-provisioned volume has all the capabilities
// that the CO wants. This RPC call SHALL return confirmed only if all the
// volume capabilities specified in the request are supported.
//This operation MUST be idempotent.
func (d *controllerService) ValidateVolumeCapabilities(ctx context.Context, req *csi.ValidateVolumeCapabilitiesRequest) (*csi.ValidateVolumeCapabilitiesResponse, error) {
	klog.V(4).Infof("ValidateVolumeCapabilities called with %s", stripSecretFromReq(*req))
	volumeID, volumeZone, err := getVolumeIDAndZone(req.GetVolumeId())
	if err != nil {
		return nil, err
	}

	volumeCapabilities := req.GetVolumeCapabilities()
	if volumeCapabilities == nil {
		return nil, status.Error(codes.InvalidArgument, "volumeCapabilities is not provided")
	}

	_, err = d.scaleway.GetVolume(&instance.GetVolumeRequest{
		VolumeID: volumeID,
		Zone:     volumeZone,
	})
	if err != nil {
		if _, ok := err.(*scw.ResourceNotFoundError); ok {
			return nil, status.Errorf(codes.NotFound, "volume %s not found", volumeID)
		}

		return nil, status.Error(codes.Internal, err.Error())
	}
	// TODO check stuff
	return &csi.ValidateVolumeCapabilitiesResponse{
		Confirmed: &csi.ValidateVolumeCapabilitiesResponse_Confirmed{
			VolumeCapabilities: []*csi.VolumeCapability{
				{
					AccessMode: &supportedAccessModes[0], // TODO refactor
				},
			},
		},
	}, nil
}

// ListVolumes returns the list of the requested volumes
func (d *controllerService) ListVolumes(ctx context.Context, req *csi.ListVolumesRequest) (*csi.ListVolumesResponse, error) {
	klog.V(4).Infof("ListVolumes called with %s", stripSecretFromReq(*req))
	var numberResults int
	var err error

	startingToken := req.GetStartingToken()
	if startingToken != "" {
		numberResults, err = strconv.Atoi(startingToken)
		if err != nil {
			return nil, status.Error(codes.Aborted, "invalid startingToken")
		}
	}

	volumesResp, err := d.scaleway.ListVolumes(&instance.ListVolumesRequest{}, scw.WithAllPages())
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	volumes := volumesResp.Volumes

	nextPage := ""
	maxEntries := req.GetMaxEntries()
	if maxEntries == 0 {
		if numberResults != 0 {
			volumes = volumes[numberResults:]
		}
	} else {
		if int(maxEntries) > (len(volumes) - numberResults) {
			volumes = volumes[numberResults:]
		} else {
			volumes = volumes[numberResults : numberResults+int(maxEntries)]
			nextPage = strconv.Itoa(numberResults + int(maxEntries))
		}
	}

	var volumesEntries []*csi.ListVolumesResponse_Entry
	for _, volume := range volumes {
		var serversID []string
		if volume.Server != nil {
			serversID = append(serversID, volume.Zone.String()+"/"+volume.Server.ID)
		}
		volumesEntries = append(volumesEntries, &csi.ListVolumesResponse_Entry{
			Volume: &csi.Volume{
				VolumeId:      scaleway.ExpandVolumeID(volume),
				CapacityBytes: int64(volume.Size),
			},
			Status: &csi.ListVolumesResponse_VolumeStatus{
				PublishedNodeIds: serversID,
			},
		})
	}

	return &csi.ListVolumesResponse{
		Entries:   volumesEntries,
		NextToken: nextPage,
	}, nil
}

// GetCapacity returns the capacity of the storage pool from which the controller provisions volumes.
func (d *controllerService) GetCapacity(ctx context.Context, req *csi.GetCapacityRequest) (*csi.GetCapacityResponse, error) {
	klog.V(4).Infof("GetCapacity is not yet implemented")
	return nil, status.Error(codes.Unimplemented, "GetCapacity is not yet implemented")
}

// ControllerGetCapabilities returns  the supported capabilities of controller service provided by the Plugin.
func (d *controllerService) ControllerGetCapabilities(ctx context.Context, req *csi.ControllerGetCapabilitiesRequest) (*csi.ControllerGetCapabilitiesResponse, error) {
	klog.V(4).Infof("ControllerGetCapabilities called with %v", stripSecretFromReq(*req))
	var capabilities []*csi.ControllerServiceCapability
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
	klog.V(4).Infof("CreateSnapshot called with %v", stripSecretFromReq(*req))
	sourceVolumeID, sourceVolumeZone, err := getSourceVolumeIDAndZone(req.GetSourceVolumeId())
	if err != nil {
		return nil, err
	}

	name := req.GetName()
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "name not provided")
	}

	snapshot, err := d.scaleway.GetSnapshotByName(name, sourceVolumeID, sourceVolumeZone)
	if err != nil {
		switch err {
		case scaleway.ErrSnapshotNotFound: // all good
		case scaleway.ErrSnapshotSameName:
			return nil, status.Errorf(codes.AlreadyExists, "a snapshot with the name %s already exists", name)
		default:
			return nil, status.Error(codes.Internal, err.Error())
		}
	}

	if snapshot != nil {
		snapshotResp := &csi.Snapshot{
			SizeBytes:      int64(snapshot.Size), // TODO(pcyvoct) ugly cast
			SnapshotId:     scaleway.ExpandSnapshotID(snapshot),
			SourceVolumeId: sourceVolumeZone.String() + "/" + sourceVolumeID,
			ReadyToUse:     snapshot.State == instance.SnapshotStateAvailable,
		}

		if snapshot.CreationDate != nil {
			creationTime, err := ptypes.TimestampProto(*snapshot.CreationDate)
			if err != nil {
				return nil, status.Error(codes.Internal, err.Error())
			}
			snapshotResp.CreationTime = creationTime

		}

		return &csi.CreateSnapshotResponse{
			Snapshot: snapshotResp,
		}, nil

	}

	snapshotResp, err := d.scaleway.CreateSnapshot(&instance.CreateSnapshotRequest{
		VolumeID: sourceVolumeID,
		Name:     name,
		Zone:     sourceVolumeZone,
	})
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	snapshotProtoResp := &csi.Snapshot{
		SizeBytes:      int64(snapshotResp.Snapshot.Size), // TODO(pcyvoct) ugly cast
		SnapshotId:     scaleway.ExpandSnapshotID(snapshotResp.Snapshot),
		SourceVolumeId: sourceVolumeZone.String() + "/" + sourceVolumeID,
		ReadyToUse:     snapshotResp.Snapshot.State == instance.SnapshotStateAvailable,
	}

	if snapshotResp.Snapshot.CreationDate != nil {
		creationTime, err := ptypes.TimestampProto(*snapshotResp.Snapshot.CreationDate)
		if err != nil {
			return nil, status.Error(codes.Internal, err.Error())
		}
		snapshotProtoResp.CreationTime = creationTime
	}

	return &csi.CreateSnapshotResponse{
		Snapshot: snapshotProtoResp,
	}, nil
}

// DeleteSnapshot deletes the given snapshot
func (d *controllerService) DeleteSnapshot(ctx context.Context, req *csi.DeleteSnapshotRequest) (*csi.DeleteSnapshotResponse, error) {
	klog.V(4).Infof("DeleteSnapshot called with %s", stripSecretFromReq(*req))
	snapshotID, snapshotZone, err := getSnapshotIDAndZone(req.GetSnapshotId())
	if err != nil {
		return nil, err
	}

	err = d.scaleway.DeleteSnapshot(&instance.DeleteSnapshotRequest{
		SnapshotID: snapshotID,
		Zone:       snapshotZone,
	})
	if err != nil {
		if _, ok := err.(*scw.ResourceNotFoundError); ok {
			klog.V(4).Infof("snapshot with ID %s not found", snapshotID)
			return &csi.DeleteSnapshotResponse{}, nil
		}

		return nil, status.Error(codes.Internal, err.Error())
	}
	return &csi.DeleteSnapshotResponse{}, nil
}

// ListSnapshots return the information about all snapshots on the
// storage system within the given parameters regardless of how
// they were created. ListSnapshots SHALL NOT list a snapshot that
// is being created but has not been cut successfully yet.
func (d *controllerService) ListSnapshots(ctx context.Context, req *csi.ListSnapshotsRequest) (*csi.ListSnapshotsResponse, error) {
	klog.V(4).Infof("ListSnapshots called with %s", stripSecretFromReq(*req))
	var numberResults int
	var err error

	startingToken := req.GetStartingToken()
	if startingToken != "" {
		numberResults, err = strconv.Atoi(startingToken)
		if err != nil {
			return nil, status.Error(codes.Aborted, "invalid startingToken")
		}
	}

	// TODO fix zones
	snapshotID, _, _ := getSnapshotIDAndZone(req.GetSnapshotId())
	sourceVolumeID, _, _ := getSourceVolumeIDAndZone(req.GetSourceVolumeId())

	// TODO all zones
	snapshotsResp, err := d.scaleway.ListSnapshots(&instance.ListSnapshotsRequest{}, scw.WithAllPages())
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	snapshots := []*instance.Snapshot{}
	for _, snap := range snapshotsResp.Snapshots {
		if snapshotID != "" && snap.ID == snapshotID {
			snapshots = []*instance.Snapshot{snap}
			break
		}
		if sourceVolumeID != "" && snap.BaseVolume != nil && snap.BaseVolume.ID == sourceVolumeID || snapshotID == "" && sourceVolumeID == "" {
			snapshots = append(snapshots, snap)
		}
	}

	nextPage := ""
	maxEntries := req.GetMaxEntries()
	if maxEntries == 0 {
		if numberResults != 0 {
			snapshots = snapshots[numberResults:]
		}
	} else {
		if int(maxEntries) > (len(snapshots) - numberResults) {
			snapshots = snapshots[numberResults:]
		} else {
			snapshots = snapshots[numberResults : numberResults+int(maxEntries)]
			nextPage = strconv.Itoa(numberResults + int(maxEntries))
		}
	}

	var snapshotsEntries []*csi.ListSnapshotsResponse_Entry
	for _, snap := range snapshots {
		sourceID := ""
		if snap.BaseVolume != nil {
			sourceID = snap.Zone.String() + "/" + snap.BaseVolume.ID
		}

		snapshotProtoResp := &csi.Snapshot{
			SizeBytes:      int64(snap.Size), // TODO(pcyvoct) ugly cast
			SnapshotId:     scaleway.ExpandSnapshotID(snap),
			SourceVolumeId: sourceID,
			ReadyToUse:     snap.State == instance.SnapshotStateAvailable,
		}

		if snap.CreationDate != nil {
			creationTime, err := ptypes.TimestampProto(*snap.CreationDate)
			if err != nil {
				return nil, status.Error(codes.Internal, err.Error())
			}
			snapshotProtoResp.CreationTime = creationTime
		}

		snapshotsEntries = append(snapshotsEntries, &csi.ListSnapshotsResponse_Entry{
			Snapshot: snapshotProtoResp,
		})
	}

	return &csi.ListSnapshotsResponse{
		Entries:   snapshotsEntries,
		NextToken: nextPage,
	}, nil
}

// ControllerExpandVolume expands the given volume
func (d *controllerService) ControllerExpandVolume(ctx context.Context, req *csi.ControllerExpandVolumeRequest) (*csi.ControllerExpandVolumeResponse, error) {
	klog.V(4).Infof("ControllerExpandVolume called with %s", stripSecretFromReq(*req))
	volumeID, volumeZone, err := getVolumeIDAndZone(req.GetVolumeId())
	if err != nil {
		return nil, err
	}

	nodeExpansionRequired := true

	volumeCapability := req.GetVolumeCapability()
	if volumeCapability != nil {
		err := validateVolumeCapability(volumeCapability)
		if err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "volumeCapabilities not supported: %s", err)
		}
		switch volumeCapability.GetAccessType().(type) {
		case *csi.VolumeCapability_Block:
			nodeExpansionRequired = false
		}
	}

	volumeResp, err := d.scaleway.GetVolume(&instance.GetVolumeRequest{
		VolumeID: volumeID,
		Zone:     volumeZone,
	})
	if err != nil {
		if _, ok := err.(*scw.ResourceNotFoundError); ok {
			return nil, status.Errorf(codes.NotFound, "volume %s not found", volumeID)
		}
		return nil, status.Error(codes.Internal, err.Error())
	}

	minSize, maxSize, err := d.scaleway.GetVolumeLimits(string(volumeResp.Volume.VolumeType))
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	newSize, err := getVolumeRequestCapacity(minSize, maxSize, req.GetCapacityRange())
	if err != nil {
		return nil, status.Errorf(codes.OutOfRange, "capacityRange invalid: %s", err)
	}

	if newSize < int64(volumeResp.Volume.Size) {
		return nil, status.Error(codes.InvalidArgument, "the new size of the volume will be less than the actual size")
	}

	_, err = d.scaleway.UpdateVolume(&instance.UpdateVolumeRequest{
		Zone:     volumeZone,
		VolumeID: volumeID,
		Size:     scw.SizePtr(scw.Size(newSize)),
	})
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	vol, err := d.scaleway.WaitForVolume(&instance.WaitForVolumeRequest{
		VolumeID: volumeID,
		Zone:     volumeZone,
	})
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	if vol.State != instance.VolumeStateAvailable {
		return nil, status.Errorf(codes.Internal, "volume %s is in state %s", volumeID, vol.State)
	}

	return &csi.ControllerExpandVolumeResponse{CapacityBytes: newSize, NodeExpansionRequired: nodeExpansionRequired}, nil
}
