package driver

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/scaleway/scaleway-sdk-go/scw"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"k8s.io/klog/v2"

	"github.com/scaleway/scaleway-csi/pkg/scaleway"
)

// name of the secret for the encryption passphrase.
const encryptionPassphraseKey = "encryptionPassphrase"

type nodeService struct {
	csi.UnimplementedNodeServer

	diskUtils DiskUtils

	nodeID   string
	nodeZone scw.Zone
}

func newNodeService() (*nodeService, error) {
	metadata, err := scaleway.GetMetadata()
	if err != nil {
		return nil, fmt.Errorf("unable to fetch Scaleway metadata: %w", err)
	}

	zone, err := scw.ParseZone(metadata.Location.ZoneID)
	if err != nil {
		return nil, fmt.Errorf("invalid zone in metadata: %w", err)
	}

	return &nodeService{
		diskUtils: newDiskUtils(),
		nodeID:    metadata.ID,
		nodeZone:  zone,
	}, nil
}

// NodeStageVolume is called by the CO prior to the volume being consumed
// by any workloads on the node by NodePublishVolume.
// The Plugin SHALL assume that this RPC will be executed on the node
// where the volume will be used.
// This RPC SHOULD be called by the CO when a workload that wants to use
// the specified volume is placed (scheduled) on the specified node
// for the first time or for the first time since a NodeUnstageVolume call
// for the specified volume was called and returned success on that node.
func (d *nodeService) NodeStageVolume(ctx context.Context, req *csi.NodeStageVolumeRequest) (*csi.NodeStageVolumeResponse, error) {
	klog.V(4).Infof("NodeStageVolume called with %s", stripSecretFromReq(req))

	// check arguments
	volumeID, _, err := ExtractIDAndZone(req.GetVolumeId())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid parameter volumeID: %s", err)
	}

	encrypted, err := isVolumeEncrypted(req.GetVolumeContext())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid volumeContext: %s", err)
	}

	stagingTargetPath := req.GetStagingTargetPath()
	if stagingTargetPath == "" {
		return nil, status.Error(codes.InvalidArgument, "stagingTargetPath not provided")
	}

	volumeCapability := req.GetVolumeCapability()
	if volumeCapability == nil {
		return nil, status.Error(codes.InvalidArgument, "volumeCapability not provided")
	}

	block, _, err := validateVolumeCapability(volumeCapability)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "volumeCapability not supported: %s", err)
	}

	volumeName, ok := req.GetPublishContext()[scwVolumeNameKey]
	if !ok || volumeName == "" {
		return nil, status.Errorf(codes.InvalidArgument, "%s not found in publish context of volume %s", scwVolumeNameKey, volumeID)
	}

	scwVolumeID, ok := req.GetPublishContext()[scwVolumeIDKey]
	if !ok {
		return nil, status.Errorf(codes.InvalidArgument, "%s not found in publish context of volume %s", scwVolumeIDKey, volumeID)
	}

	devicePath, err := d.diskUtils.GetDevicePath(scwVolumeID)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, status.Errorf(codes.NotFound, "volume %s is not mounted on node yet", volumeID)
		}
		return nil, status.Errorf(codes.Internal, "error getting device path for volume with ID %s: %s", volumeID, err.Error())
	}
	klog.V(4).Infof("volume %s with ID %s has device path %s", volumeName, volumeID, devicePath)

	if encrypted {
		passhrase, ok := req.GetSecrets()[encryptionPassphraseKey]
		if !ok {
			return nil, status.Errorf(codes.InvalidArgument, "missing passphrase secret for key %s", encryptionPassphraseKey)
		}
		devicePath, err = d.diskUtils.EncryptAndOpenDevice(scwVolumeID, passhrase)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "error encrypting/opening volume with ID %s: %s", volumeID, err.Error())
		}
	}

	if block {
		return &csi.NodeStageVolumeResponse{}, nil
	}

	if d.diskUtils.IsMounted(stagingTargetPath) {
		blockDevice, err := d.diskUtils.IsBlockDevice(stagingTargetPath)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "error checking stat for %s: %s", stagingTargetPath, err.Error())
		}
		if blockDevice {
			// block device mounted at stagingTargetPath is not normal
			return nil, status.Errorf(codes.Unknown, "block device mounted as stagingTargetPath %s for volume with ID %s", stagingTargetPath, volumeID)
		}
		klog.V(4).Infof("volume %s with ID %s is already mounted on %s", volumeName, volumeID, stagingTargetPath)
		// TODO check volumeCapability
		return &csi.NodeStageVolumeResponse{}, nil
	}

	mountCap := volumeCapability.GetMount()
	if mountCap == nil {
		return nil, status.Error(codes.InvalidArgument, "mount volume capability is nil")
	}

	mountOptions := mountCap.GetMountFlags()
	fsType := mountCap.GetFsType()

	// See https://github.com/container-storage-interface/spec/issues/482.
	if fsType == "xfs" {
		mountOptions = append(mountOptions, "nouuid")
	}

	klog.V(4).Infof("Volume %s with ID %s will be mounted on %s with type %s and options %s", volumeName, volumeID, stagingTargetPath, fsType, strings.Join(mountOptions, ","))

	// format and mounting volume
	if err := d.diskUtils.FormatAndMount(stagingTargetPath, devicePath, fsType, mountOptions); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to format and mount device from (%q) to (%q) with fstype (%q) and options (%q): %s",
			devicePath, stagingTargetPath, fsType, mountOptions, err)
	}

	klog.V(4).Infof("Volume %s with ID %s has been mounted on %s with type %s and options %s", volumeName, volumeID, stagingTargetPath, fsType, strings.Join(mountOptions, ","))

	// Try expanding the volume if it's created from a snapshot. We provide an
	// empty password as we don't expect the size of an encrypted (or not) volume
	// to change between the moment we open it and now, so luks resizing is useless.
	if err := d.diskUtils.Resize(stagingTargetPath, devicePath, ""); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to resize volume: %s", err)
	}

	return &csi.NodeStageVolumeResponse{}, nil
}

// NodeUnstageVolume is a reverse operation of NodeStageVolume.
// It must undo the work by the corresponding NodeStageVolume.
func (d *nodeService) NodeUnstageVolume(ctx context.Context, req *csi.NodeUnstageVolumeRequest) (*csi.NodeUnstageVolumeResponse, error) {
	klog.V(4).Infof("NodeUnstageVolume called with %s", stripSecretFromReq(req))

	// check arguments
	volumeID, _, err := ExtractIDAndZone(req.GetVolumeId())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid parameter volumeID: %s", err)
	}

	stagingTargetPath := req.GetStagingTargetPath()
	if stagingTargetPath == "" {
		return nil, status.Error(codes.InvalidArgument, "stagingTargetPath not provided")
	}

	if _, err = d.diskUtils.GetDevicePath(volumeID); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, status.Errorf(codes.NotFound, "volume with ID %s not found", volumeID)
		}
		return nil, status.Errorf(codes.Internal, "error getting device path for volume with ID %s: %s", volumeID, err.Error())
	}

	if _, err := os.Stat(stagingTargetPath); errors.Is(err, fs.ErrNotExist) {
		return nil, status.Errorf(codes.NotFound, "volume with ID %s not found on node", volumeID)
	}

	if d.diskUtils.IsMounted(stagingTargetPath) {
		klog.V(4).Infof("Volume with ID %s is mounted on %s, umounting it", volumeID, stagingTargetPath)
		err = d.diskUtils.Unmount(stagingTargetPath)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "error unmounting target path: %s", err.Error())
		}
	}

	if err := d.diskUtils.CloseDevice(volumeID); err != nil {
		return nil, status.Errorf(codes.Internal, "error closing device with ID %s: %s", volumeID, err.Error())
	}

	return &csi.NodeUnstageVolumeResponse{}, nil
}

// NodePublishVolume is called by the CO when a workload
// that wants to use the specified volume is placed (scheduled)
// on a node. The Plugin SHALL assume that this RPC will be executed
// on the node where the volume will be used.
func (d *nodeService) NodePublishVolume(ctx context.Context, req *csi.NodePublishVolumeRequest) (*csi.NodePublishVolumeResponse, error) {
	klog.V(4).Infof("NodePublishVolume called with %s", stripSecretFromReq(req))

	// check arguments
	volumeID, _, err := ExtractIDAndZone(req.GetVolumeId())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid parameter volumeID: %s", err)
	}

	targetPath := req.GetTargetPath()
	if targetPath == "" {
		return nil, status.Error(codes.InvalidArgument, "targetPath not provided")
	}

	volumeCapability := req.GetVolumeCapability()
	if volumeCapability == nil {
		return nil, status.Error(codes.InvalidArgument, "volumeCapability not provided")
	}

	block, mount, err := validateVolumeCapability(volumeCapability)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "volumeCapability not supported: %s", err)
	}

	stagingTargetPath := req.GetStagingTargetPath()
	if stagingTargetPath == "" {
		return nil, status.Error(codes.FailedPrecondition, "stagingTargetPath not provided")
	}

	scwVolumeID, ok := req.GetPublishContext()[scwVolumeIDKey]
	if !ok {
		return nil, status.Errorf(codes.InvalidArgument, "%s not found for volume with ID %s", scwVolumeIDKey, volumeID)
	}

	volumeName, ok := req.GetPublishContext()[scwVolumeNameKey]
	if !ok {
		return nil, status.Errorf(codes.InvalidArgument, "%s not provided in publishContext", scwVolumeNameKey)
	}

	devicePath, err := d.diskUtils.GetDevicePath(scwVolumeID)
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "volume %s not found: %s", volumeID, err.Error())
	}

	encrypted, err := isVolumeEncrypted(req.GetVolumeContext())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid volumeContext: %s", err)
	}

	if encrypted {
		devicePath, err = d.diskUtils.GetMappedDevicePath(scwVolumeID)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "error getting mapped device for encrypted device %s: %s", devicePath, err.Error())
		}
	}

	if d.diskUtils.IsMounted(targetPath) {
		blockDevice, err := d.diskUtils.IsBlockDevice(targetPath)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "error checking stat for %s: %s", targetPath, err.Error())
		}
		if blockDevice && mount || !blockDevice && block {
			return nil, status.Error(codes.AlreadyExists, "cannot change volumeCapability type")
		}

		klog.V(4).Infof("Volume %s with ID %s is already mounted on %s", volumeName, volumeID, stagingTargetPath)
		return &csi.NodePublishVolumeResponse{}, nil
	}

	var (
		sourcePath   string
		fsType       string
		mountOptions = []string{"bind"}
	)

	if block {
		sourcePath = devicePath
	} else {
		sourcePath = stagingTargetPath
		fsType = req.GetVolumeCapability().GetMount().GetFsType()
		mountOptions = append(mountOptions, req.GetVolumeCapability().GetMount().GetMountFlags()...)
	}

	if req.GetReadonly() {
		mountOptions = append(mountOptions, "ro")
	}

	if err := createMountPoint(targetPath, block); err != nil {
		return nil, status.Errorf(codes.Internal, "error creating mount point %s for volume with ID %s", targetPath, volumeID)
	}

	if err := d.diskUtils.MountToTarget(sourcePath, targetPath, fsType, mountOptions); err != nil {
		return nil, status.Errorf(codes.Internal, "error mounting source %s to target %s with fs of type %s : %s", sourcePath, targetPath, fsType, err.Error())
	}

	return &csi.NodePublishVolumeResponse{}, nil
}

// NodeUnpublishVolume is a reverse operation of NodePublishVolume.
// This RPC MUST undo the work by the corresponding NodePublishVolume.
func (d *nodeService) NodeUnpublishVolume(ctx context.Context, req *csi.NodeUnpublishVolumeRequest) (*csi.NodeUnpublishVolumeResponse, error) {
	klog.V(4).Infof("NodeUnpublishVolume called with %s", stripSecretFromReq(req))

	targetPath := req.GetTargetPath()
	if targetPath == "" {
		return nil, status.Error(codes.InvalidArgument, "targetPath not provided")
	}

	if err := d.diskUtils.Unmount(targetPath); err != nil {
		return nil, status.Errorf(codes.Internal, "error unmounting target path: %s", err.Error())
	}

	return &csi.NodeUnpublishVolumeResponse{}, nil
}

// NodeGetVolumeStats returns the volume capacity statistics available for the volume
func (d *nodeService) NodeGetVolumeStats(ctx context.Context, req *csi.NodeGetVolumeStatsRequest) (*csi.NodeGetVolumeStatsResponse, error) {
	klog.V(4).Infof("NodeGetVolumeStats called with %s", stripSecretFromReq(req))

	volumeID, _, err := ExtractIDAndZone(req.GetVolumeId())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid parameter volumeID: %s", err)
	}

	volumePath := req.GetVolumePath()
	if volumePath == "" {
		return nil, status.Error(codes.InvalidArgument, "volumePath not provided")
	}

	stagingPath := req.GetStagingTargetPath()
	if stagingPath != "" {
		volumePath = stagingPath
	}

	if !d.diskUtils.IsMounted(volumePath) {
		return nil, status.Errorf(codes.NotFound, "volume with ID %s not mounted to %s", volumeID, req.GetVolumePath())
	}

	if _, err := d.diskUtils.GetDevicePath(volumeID); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, status.Errorf(codes.NotFound, "volume with ID %s not found", volumeID)
		}
		return nil, status.Errorf(codes.Internal, "error getting device path for volume with ID %s: %s", volumeID, err.Error())
	}

	fs, err := d.diskUtils.GetStatfs(volumePath)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "error doing stat on %s: %s", volumePath, err.Error())
	}

	totalBytes := uint64ToInt64(fs.Blocks) * int64(fs.Bsize)
	availableBytes := uint64ToInt64(fs.Bfree) * int64(fs.Bsize)
	usedBytes := totalBytes - availableBytes

	totalInodes := uint64ToInt64(fs.Files)
	freeInodes := uint64ToInt64(fs.Ffree)
	usedInodes := totalInodes - freeInodes

	diskUsage := &csi.VolumeUsage{
		Unit:      csi.VolumeUsage_BYTES,
		Total:     totalBytes,
		Available: availableBytes,
		Used:      usedBytes,
	}

	inodesUsage := &csi.VolumeUsage{
		Unit:      csi.VolumeUsage_INODES,
		Total:     totalInodes,
		Available: freeInodes,
		Used:      usedInodes,
	}

	return &csi.NodeGetVolumeStatsResponse{
		Usage: []*csi.VolumeUsage{
			diskUsage,
			inodesUsage,
		},
	}, nil
}

// NodeGetCapabilities allows the CO to check the supported capabilities of node service provided by the Plugin.
func (d *nodeService) NodeGetCapabilities(ctx context.Context, req *csi.NodeGetCapabilitiesRequest) (*csi.NodeGetCapabilitiesResponse, error) {
	return &csi.NodeGetCapabilitiesResponse{
		Capabilities: []*csi.NodeServiceCapability{
			{
				Type: &csi.NodeServiceCapability_Rpc{
					Rpc: &csi.NodeServiceCapability_RPC{
						Type: csi.NodeServiceCapability_RPC_STAGE_UNSTAGE_VOLUME,
					},
				},
			},
			{
				Type: &csi.NodeServiceCapability_Rpc{
					Rpc: &csi.NodeServiceCapability_RPC{
						Type: csi.NodeServiceCapability_RPC_GET_VOLUME_STATS,
					},
				},
			},
			{
				Type: &csi.NodeServiceCapability_Rpc{
					Rpc: &csi.NodeServiceCapability_RPC{
						Type: csi.NodeServiceCapability_RPC_EXPAND_VOLUME,
					},
				},
			},
			{
				Type: &csi.NodeServiceCapability_Rpc{
					Rpc: &csi.NodeServiceCapability_RPC{
						Type: csi.NodeServiceCapability_RPC_SINGLE_NODE_MULTI_WRITER,
					},
				},
			},
		},
	}, nil
}

// NodeGetInfo returns information about node's volumes
func (d *nodeService) NodeGetInfo(ctx context.Context, req *csi.NodeGetInfoRequest) (*csi.NodeGetInfoResponse, error) {
	return &csi.NodeGetInfoResponse{
		NodeId:            d.nodeZone.String() + "/" + d.nodeID,
		MaxVolumesPerNode: scaleway.MaxVolumesPerNode - 1, // One is already used by the l_ssd or b_ssd root volume
		AccessibleTopology: &csi.Topology{
			Segments: map[string]string{
				ZoneTopologyKey: d.nodeZone.String(),
			},
		},
	}, nil
}

// NodeExpandVolume expands the given volume
func (d *nodeService) NodeExpandVolume(ctx context.Context, req *csi.NodeExpandVolumeRequest) (*csi.NodeExpandVolumeResponse, error) {
	klog.V(4).Infof("NodeExpandVolume called with %s", stripSecretFromReq(req))

	volumeID, _, err := ExtractIDAndZone(req.GetVolumeId())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid parameter volumeID: %s", err)
	}

	volumePath := req.GetVolumePath()
	if volumePath == "" {
		return nil, status.Error(codes.InvalidArgument, "volumePath not provided")
	}

	devicePath, err := d.diskUtils.GetDevicePath(volumeID)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, status.Errorf(codes.NotFound, "volume %s is not mounted on node", volumeID)
		}
		return nil, status.Errorf(codes.Internal, "failed to get device path for volume %s: %s", volumeID, err)
	}

	isBlock, err := d.diskUtils.IsBlockDevice(volumePath)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "error checking stat for %s: %s", devicePath, err.Error())
	}

	volumeCapability := req.GetVolumeCapability()
	if volumeCapability != nil {
		if isBlock, _, err = validateVolumeCapability(volumeCapability); err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "volumeCapability not supported: %s", err)
		}
	}

	// no need to resize if it's in block mode
	if isBlock {
		return &csi.NodeExpandVolumeResponse{}, nil
	}

	klog.V(4).Infof("resizing volume %s mounted on %s", volumeID, volumePath)
	encrypted, err := d.diskUtils.IsEncrypted(devicePath)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "error checking if volume %s is encrypted: %s", volumeID, err.Error())
	}

	passphrase := req.GetSecrets()[encryptionPassphraseKey]
	if encrypted {
		devicePath, err = d.diskUtils.GetMappedDevicePath(volumeID)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "error retrieving mapped device path for volume with ID %s: %s", volumeID, err.Error())
		}
		klog.V(4).Infof("mapped device path for volume %s is %s", volumeID, devicePath)
		if passphrase == "" {
			return nil, status.Errorf(codes.InvalidArgument, "device %s is LUKS encrypted, but no passphrase was provided", devicePath)
		}
	}

	if err = d.diskUtils.Resize(volumePath, devicePath, passphrase); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to resize volume %s mounted on %s: %s", volumeID, volumePath, err)
	}

	return &csi.NodeExpandVolumeResponse{}, nil
}
