package driver

import (
	"context"
	"os"
	"strings"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/docker/docker/pkg/mount"
	"github.com/scaleway/scaleway-csi/scaleway"
	"github.com/scaleway/scaleway-sdk-go/scw"
	"golang.org/x/sys/unix"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"k8s.io/klog"
)

const (
	// maximum of volumes per node
	maxVolumesPerNode = 16
)

type nodeService struct {
	diskUtils DiskUtils

	nodeID   string
	nodeZone scw.Zone
}

func newNodeService() nodeService {
	metadata, err := scaleway.NewMetadata().GetMetadata()
	if err != nil {
		panic(err)
	}

	zone, err := scw.ParseZone(metadata.Location.ZoneID)
	if err != nil {
		panic(err)
	}

	return nodeService{
		diskUtils: newDiskUtils(),
		nodeID:    metadata.ID,
		nodeZone:  zone,
	}
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
	klog.V(4).Infof("NodeStageVolume called with %+v", *req)

	// check arguments
	volumeID, _, err := getVolumeIDAndZone(req.GetVolumeId())
	if err != nil {
		return nil, err
	}

	stagingTargetPath := req.GetStagingTargetPath()
	if stagingTargetPath == "" {
		return nil, status.Error(codes.InvalidArgument, "stagingTargetPath not provided")
	}

	volumeCapability := req.GetVolumeCapability()
	if volumeCapability == nil {
		return nil, status.Error(codes.InvalidArgument, "volumeCapability not provided")
	}

	err = validateVolumeCapabilities([]*csi.VolumeCapability{volumeCapability})
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "volumeCapability not supported: %s", err)
	}

	switch volumeCapability.GetAccessType().(type) {
	// no need to mount if it's in block mode
	case *csi.VolumeCapability_Block:
		return &csi.NodeStageVolumeResponse{}, nil
	}

	volumeName, ok := req.GetPublishContext()[scwVolumeName]
	if !ok || volumeName == "" {
		return nil, status.Errorf(codes.InvalidArgument, "%s not found in publish context of volume %s", scwVolumeName, volumeID)
	}

	scwVolumeID, ok := req.GetPublishContext()[scwVolumeID]
	if !ok {
		return nil, status.Errorf(codes.InvalidArgument, "%s not found in publish context of volume %s", scwVolumeID, volumeID)
	}

	devicePath, err := d.diskUtils.GetDevicePath(scwVolumeID)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, status.Errorf(codes.NotFound, "volume %s is not mounted on node yet", volumeID)
		}
		return nil, status.Errorf(codes.Internal, "error getting device path for volume with ID %s: %s", volumeID, err.Error())
	}
	klog.V(4).Infof("volume %s with ID %s has device path %s", volumeName, volumeID, devicePath)

	isMounted, err := d.diskUtils.IsSharedMounted(stagingTargetPath, devicePath)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "error checking mount point of volume %s on path %s: %s", volumeID, stagingTargetPath, err.Error())
	}

	if isMounted {
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

	mount := volumeCapability.GetMount()
	if mount == nil {
		return nil, status.Error(codes.InvalidArgument, "mount volume capability is nil")
	}

	mountOptions := mount.GetMountFlags()
	fsType := mount.GetFsType()

	klog.V(4).Infof("Volume %s with ID %s will be mounted on %s with type %s and options %s", volumeName, volumeID, stagingTargetPath, fsType, strings.Join(mountOptions, ","))

	// format and mounting volume
	err = d.diskUtils.FormatAndMount(stagingTargetPath, devicePath, fsType, mountOptions)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to format and mount device from (%q) to (%q) with fstype (%q) and options (%q): %v",
			devicePath, stagingTargetPath, fsType, mountOptions, err)
	}
	klog.V(4).Infof("Volume %s with ID %s has been mounted on %s with type %s and options %s", volumeName, volumeID, stagingTargetPath, fsType, strings.Join(mountOptions, ","))

	return &csi.NodeStageVolumeResponse{}, nil
}

// NodeUnstageVolume is a reverse operation of NodeStageVolume.
// It must undo the work by the corresponding NodeStageVolume.
func (d *nodeService) NodeUnstageVolume(ctx context.Context, req *csi.NodeUnstageVolumeRequest) (*csi.NodeUnstageVolumeResponse, error) {
	klog.V(4).Infof("NodeUnstageVolume called with %+v", *req)

	// check arguments
	volumeID, _, err := getVolumeIDAndZone(req.GetVolumeId())
	if err != nil {
		return nil, err
	}

	stagingTargetPath := req.GetStagingTargetPath()
	if stagingTargetPath == "" {
		return nil, status.Error(codes.InvalidArgument, "stagingTargetPath not provided")
	}

	_, err = d.diskUtils.GetDevicePath(volumeID)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, status.Errorf(codes.NotFound, "volume with ID %s not found", volumeID)
		}
		return nil, status.Errorf(codes.Internal, "error getting device path for volume with ID %s: %s", volumeID, err.Error())
	}

	if _, err := os.Stat(stagingTargetPath); os.IsNotExist(err) {
		return nil, status.Errorf(codes.NotFound, "volume with ID %s not found on node", volumeID)
	}

	isMounted, err := d.diskUtils.IsSharedMounted(stagingTargetPath, "")
	if err != nil {
		return nil, status.Errorf(codes.Internal, "error checking if target is mounted: %s", err.Error())
	}

	if isMounted {
		klog.V(4).Infof("Volume with ID %s is mounted on %s, umounting it", volumeID, stagingTargetPath)
		err = mount.Unmount(stagingTargetPath)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "NodeUnstageVolume: error unmounting target path: %s", err.Error())
		}
	}

	return &csi.NodeUnstageVolumeResponse{}, nil
}

// NodePublishVolume is called by the CO when a workload
// that wants to use the specified volume is placed (scheduled)
// on a node. The Plugin SHALL assume that this RPC will be executed
// on the node where the volume will be used.
func (d *nodeService) NodePublishVolume(ctx context.Context, req *csi.NodePublishVolumeRequest) (*csi.NodePublishVolumeResponse, error) {
	klog.V(4).Infof("NodePublishVolume called with %+v", *req)

	// check arguments
	volumeID, _, err := getVolumeIDAndZone(req.GetVolumeId())
	if err != nil {
		return nil, err
	}

	targetPath := req.GetTargetPath()
	if targetPath == "" {
		return nil, status.Error(codes.InvalidArgument, "targetPath not provided")
	}

	volumeCapability := req.GetVolumeCapability()
	if volumeCapability == nil {
		return nil, status.Error(codes.InvalidArgument, "volumeCapability not provided")
	}

	err = validateVolumeCapabilities([]*csi.VolumeCapability{volumeCapability})
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "volumeCapability not supported: %s", err)
	}

	stagingTargetPath := req.GetStagingTargetPath()
	if stagingTargetPath == "" {
		return nil, status.Error(codes.FailedPrecondition, "stagingTargetPath not provided")
	}

	scwVolumeID, ok := req.GetPublishContext()[scwVolumeID]
	if !ok {
		return nil, status.Errorf(codes.InvalidArgument, "%s not found for volume wit ID %s", scwVolumeID, volumeID)
	}

	volumeName, ok := req.GetPublishContext()[scwVolumeName]
	if !ok {
		return nil, status.Errorf(codes.InvalidArgument, "%s not provided in publishContext", scwVolumeName)
	}

	devicePath, err := d.diskUtils.GetDevicePath(scwVolumeID)
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "volume %s not found: %s", volumeID, err.Error())
	}

	// TODO check volumeID
	isMounted, err := d.diskUtils.IsSharedMounted(targetPath, devicePath)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "error checking mount point of volume %s on path %s: %v", volumeID, stagingTargetPath, err)
	}

	if isMounted {
		blockDevice, err := d.diskUtils.IsBlockDevice(targetPath)
		if blockDevice && volumeCapability.GetMount() != nil || !blockDevice && volumeCapability.GetBlock() != nil {
			return nil, status.Error(codes.AlreadyExists, "cannot change volumeCapability type")
		}

		if volumeCapability.GetBlock() != nil {
			// unix specific, will error if not unix
			fd, err := unix.Openat(unix.AT_FDCWD, devicePath, unix.O_RDONLY, uint32(0))
			defer unix.Close(fd)
			if err != nil {
				return nil, status.Errorf(codes.Internal, "error opening block device %s: %s", devicePath, err.Error())
			}
			ro, err := unix.IoctlGetInt(fd, unix.BLKROGET)
			if err != nil {
				return nil, status.Errorf(codes.Internal, "error getting BLKROGET for block device %s: %s", devicePath, err.Error())
			}

			if (ro == 1) == req.GetReadonly() {
				klog.V(4).Infof("Volume %s with ID %s is already mounted as a raw device on %s", volumeName, volumeID, targetPath)
				return &csi.NodePublishVolumeResponse{}, nil
			}
			return nil, status.Errorf(codes.AlreadyExists, "volume with ID %s does not match the given mount mode for the request", volumeID)
		}

		mountInfo, err := d.diskUtils.GetMountInfo(targetPath)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "error getting mount information of path %s: %s", targetPath, err.Error())
		}

		isReadOnly := false
		if mountInfo != nil {
			for _, opt := range mountInfo.mountOptions {
				if opt == "rw" {
					break
				} else if opt == "ro" {
					isReadOnly = true
					break
				}
			}
		}

		if isReadOnly != req.GetReadonly() {
			return nil, status.Errorf(codes.AlreadyExists, "volume with ID %s does not match the given mount mode for the request", volumeID)
		}

		klog.V(4).Infof("Volume %s with ID %s is already mounted on %s", volumeName, volumeID, stagingTargetPath)
		return &csi.NodePublishVolumeResponse{}, nil
	}

	var sourcePath string
	var fsType string
	var mountOptions []string
	mount := volumeCapability.GetMount()
	if mount == nil {
		if volumeCapability.GetBlock() != nil {
			sourcePath = devicePath
			if req.GetReadonly() {
				fd, err := unix.Openat(unix.AT_FDCWD, devicePath, unix.O_RDONLY, uint32(0))
				if err != nil {
					return nil, status.Errorf(codes.Internal, "error opening block device %s: %s", devicePath, err.Error())
				}
				err = unix.IoctlSetPointerInt(fd, unix.BLKROSET, 1)
				unix.Close(fd)
				if err != nil {
					return nil, status.Errorf(codes.Internal, "error setting BLKROSET for block device %s: %s", devicePath, err.Error())
				}
			}
		}
	} else {
		sourcePath = stagingTargetPath
		fsType = mount.GetFsType()
		mountOptions = mount.GetMountFlags()
	}

	mountOptions = append(mountOptions, "bind")

	if req.GetReadonly() {
		mountOptions = append(mountOptions, "ro")
	}

	err = createMountPoint(targetPath, volumeCapability.GetBlock() != nil)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "error creating mount point %s for volume with ID %s", targetPath, volumeID)
	}

	err = d.diskUtils.MountToTarget(sourcePath, targetPath, fsType, mountOptions)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "error mounting source %s to target %s with fs of type %s : %s", sourcePath, targetPath, fsType, err.Error())
	}
	return &csi.NodePublishVolumeResponse{}, nil
}

// NodeUnpublishVolume is a reverse operation of NodePublishVolume.
//This RPC MUST undo the work by the corresponding NodePublishVolume.
func (d *nodeService) NodeUnpublishVolume(ctx context.Context, req *csi.NodeUnpublishVolumeRequest) (*csi.NodeUnpublishVolumeResponse, error) {
	klog.V(4).Infof("NodeUnpublishVolume called with %+v", *req)

	// check arguments
	volumeID, _, err := getVolumeIDAndZone(req.GetVolumeId())
	if err != nil {
		return nil, err
	}

	targetPath := req.GetTargetPath()
	if targetPath == "" {
		return nil, status.Error(codes.InvalidArgument, "targetPath not provided")
	}

	_, err = d.diskUtils.GetDevicePath(volumeID)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, status.Errorf(codes.NotFound, "volume with ID %s not found", volumeID)
		}
		return nil, status.Errorf(codes.Internal, "error getting device path for volume with ID %s: %s", volumeID, err.Error())
	}

	isMounted, err := d.diskUtils.IsSharedMounted(targetPath, "")
	if err != nil {
		return nil, status.Errorf(codes.Internal, "error checking if target is mounted: %s", err.Error())
	}

	if isMounted {
		err = mount.Unmount(targetPath)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "error unmounting target path: %s", err.Error())
		}
	}

	return &csi.NodeUnpublishVolumeResponse{}, nil
}

// NodeGetVolumeStats returns the volume capacity statistics available for the volume
func (d *nodeService) NodeGetVolumeStats(ctx context.Context, req *csi.NodeGetVolumeStatsRequest) (*csi.NodeGetVolumeStatsResponse, error) {
	klog.V(4).Infof("NodeGetVolumeStats called with %+v", *req)

	volumeID, _, err := getVolumeIDAndZone(req.GetVolumeId())
	if err != nil {
		return nil, err
	}

	volumePath := req.GetVolumePath()
	if volumePath == "" {
		return nil, status.Error(codes.InvalidArgument, "volumePath not provided")
	}

	stagingPath := req.GetStagingTargetPath()
	if stagingPath != "" {
		volumePath = stagingPath
	}

	isMounted, err := d.diskUtils.IsSharedMounted(volumePath, "")
	if err != nil {
		return nil, status.Errorf(codes.Internal, "error checking mount point of path %s for volume %s: %s", volumePath, volumeID, err.Error())
	}

	if !isMounted {
		return nil, status.Errorf(codes.NotFound, "volume with ID %s not found", volumeID)
	}

	_, err = d.diskUtils.GetDevicePath(volumeID)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, status.Errorf(codes.NotFound, "volume with ID %s not found", volumeID)
		}
		return nil, status.Errorf(codes.Internal, "error getting device path for volume with ID %s: %s", volumeID, err.Error())
	}

	fs, err := d.diskUtils.GetStatfs(volumePath)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "error doing stat on %s: %s", volumePath, err.Error())
	}

	totalBytes := fs.Blocks * uint64(fs.Bsize)
	availableBytes := fs.Bfree * uint64(fs.Bsize)
	usedBytes := totalBytes - availableBytes

	totalInodes := fs.Files
	freeInodes := fs.Ffree
	usedInodes := totalInodes - freeInodes

	diskUsage := &csi.VolumeUsage{
		Unit:      csi.VolumeUsage_BYTES,
		Total:     int64(totalBytes),
		Available: int64(availableBytes),
		Used:      int64(usedBytes),
	}

	inodesUsage := &csi.VolumeUsage{
		Unit:      csi.VolumeUsage_INODES,
		Total:     int64(totalInodes),
		Available: int64(freeInodes),
		Used:      int64(usedInodes),
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
			&csi.NodeServiceCapability{
				Type: &csi.NodeServiceCapability_Rpc{
					Rpc: &csi.NodeServiceCapability_RPC{
						Type: csi.NodeServiceCapability_RPC_STAGE_UNSTAGE_VOLUME,
					},
				},
			},
			&csi.NodeServiceCapability{
				Type: &csi.NodeServiceCapability_Rpc{
					Rpc: &csi.NodeServiceCapability_RPC{
						Type: csi.NodeServiceCapability_RPC_GET_VOLUME_STATS,
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
		MaxVolumesPerNode: maxVolumesPerNode - 1, // One is already used by the l_ssd root volume
		AccessibleTopology: &csi.Topology{
			Segments: map[string]string{
				zoneTopologyKey: d.nodeZone.String(),
			},
		},
	}, nil
}

// NodeExpandVolume expands the given volume
func (d *nodeService) NodeExpandVolume(ctx context.Context, req *csi.NodeExpandVolumeRequest) (*csi.NodeExpandVolumeResponse, error) {
	return nil, status.Error(codes.Unimplemented, "NodeExpandVolume is not implemented yet")
}
