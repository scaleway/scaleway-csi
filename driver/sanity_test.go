package driver

import (
	"fmt"
	"os"
	"os/exec"
	"path"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/kubernetes-csi/csi-test/v5/pkg/sanity"
	"github.com/scaleway/scaleway-csi/scaleway"
	"github.com/scaleway/scaleway-sdk-go/api/instance/v1"
	"github.com/scaleway/scaleway-sdk-go/scw"
	"golang.org/x/sys/unix"
	kmount "k8s.io/mount-utils"
	kexec "k8s.io/utils/exec"
	utilsio "k8s.io/utils/io"
)

type fakeHelper struct {
	fakeDiskUtils
	fakeInstanceAPI
}

func TestSanityCSI(t *testing.T) {
	endpoint := "/tmp/csi-testing.sock"
	nodeID := "fb094b6a-a732-4d5f-8283-bd6726ff5938"
	defaultVol := &instance.Volume{
		ID:         "fb094b6a-b73b-4d5f-8283-bd6726ff5938",
		VolumeType: instance.VolumeVolumeTypeLSSD,
		Zone:       scw.ZoneFrPar1,
		Name:       "local",
	}
	volumesMap := map[string]*instance.Volume{
		defaultVol.ID: defaultVol,
	}
	serversMap := map[string]*instance.Server{
		nodeID: &instance.Server{
			ID: nodeID,
			Volumes: map[string]*instance.VolumeServer{
				"0": ConvertVolumeVolumeServer(defaultVol),
			},
			Zone: scw.ZoneFrPar1,
		},
	}
	snapshotsMap := make(map[string]*instance.Snapshot)
	diskUtilsDevices := make(map[string]*mountpoint)

	driverConfig := &DriverConfig{
		Endpoint: fmt.Sprintf("unix://%s", endpoint),
		Mode:     AllMode,
	}
	nodeConfig := &NodeConfig{
		VolumeNumber: 16,
	}
	fakeInstance := &fakeInstanceAPI{
		volumesMap:   volumesMap,
		serversMap:   serversMap,
		snapshotsMap: snapshotsMap,
		defaultZone:  scw.ZoneFrPar1,
	}
	fakeDiskUtils := &fakeDiskUtils{
		kMounter: &kmount.SafeFormatAndMount{
			Interface: kmount.New(""),
			Exec:      kexec.New(),
		},
		devices: diskUtilsDevices,
	}
	fakeHelper := &fakeHelper{
		fakeDiskUtils:   *fakeDiskUtils,
		fakeInstanceAPI: *fakeInstance,
	}

	driver := &Driver{
		driverconfig: driverConfig,
		nodeconfig:   nodeConfig,
		controllerService: controllerService{
			scaleway: &scaleway.Scaleway{
				InstanceAPI: fakeHelper,
			},
			config: driverConfig,
		},
		nodeService: nodeService{
			nodeID:    nodeID,
			nodeZone:  scw.ZoneFrPar1,
			diskUtils: fakeHelper,
		},
	}

	go driver.Run() // an error here would fail the test anyway since the grpc server would not be started

	config := sanity.NewTestConfig()
	config.Address = endpoint
	config.TestNodeVolumeAttachLimit = true
	config.TestVolumeExpandSize = config.TestVolumeSize * 2
	config.RemoveTargetPath = func(path string) error {
		return os.RemoveAll(path)
	}
	config.RemoveStagingPath = func(path string) error {
		return os.RemoveAll(path)
	}
	sanity.Test(t, config)
	driver.srv.GracefulStop()
	os.RemoveAll(endpoint)
}

func ConvertVolumeVolumeServer(vol *instance.Volume) *instance.VolumeServer {
	return &instance.VolumeServer{
		ID:               vol.ID,
		Name:             vol.Name,
		Organization:     vol.Organization,
		Size:             vol.Size,
		VolumeType:       instance.VolumeServerVolumeType(vol.VolumeType),
		CreationDate:     vol.CreationDate,
		ModificationDate: vol.ModificationDate,
		State:            instance.VolumeServerStateAvailable,
		Project:          vol.Project,
		Boot:             false,
		Zone:             vol.Zone,
	}
}

type fakeInstanceAPI struct {
	volumesMap   map[string]*instance.Volume
	serversMap   map[string]*instance.Server
	snapshotsMap map[string]*instance.Snapshot
	defaultZone  scw.Zone
}

func (s *fakeHelper) ListVolumesTypes(req *instance.ListVolumesTypesRequest, opts ...scw.RequestOption) (*instance.ListVolumesTypesResponse, error) {
	return &instance.ListVolumesTypesResponse{
		Volumes: map[string]*instance.VolumeType{
			instance.VolumeVolumeTypeBSSD.String(): {
				Constraints: &instance.VolumeTypeConstraints{
					Max: 10 * 1000 * 1000 * 1000 * 1000,
					Min: 1 * 1000 * 1000 * 1000,
				},
			},
		},
	}, nil
}

func (s *fakeHelper) ListVolumes(req *instance.ListVolumesRequest, opts ...scw.RequestOption) (*instance.ListVolumesResponse, error) {
	volumes := make([]*instance.Volume, 0)
	for _, v := range s.volumesMap {
		if req.Name == nil || strings.Contains(v.Name, *req.Name) {
			volumes = append(volumes, v)
		}
	}
	return &instance.ListVolumesResponse{Volumes: volumes, TotalCount: uint32(len(volumes))}, nil
}

func (s *fakeHelper) CreateVolume(req *instance.CreateVolumeRequest, opts ...scw.RequestOption) (*instance.CreateVolumeResponse, error) {
	if req.Zone == "" {
		req.Zone = s.defaultZone
	}
	volume := &instance.Volume{}
	volume.ID = uuid.New().String()
	volume.Zone = req.Zone
	volume.VolumeType = req.VolumeType
	if req.Size != nil {
		volume.Size = *req.Size
	} else if req.BaseVolume != nil {
		baseVol, ok := s.volumesMap[*req.BaseVolume]
		if !ok {
			return nil, &scw.ResourceNotFoundError{}
		}
		volume.Size = baseVol.Size
	} else if req.BaseSnapshot != nil {
		baseSnap, ok := s.snapshotsMap[*req.BaseSnapshot]
		if !ok {
			return nil, &scw.ResourceNotFoundError{}
		}
		volume.Size = baseSnap.Size
	} else {
		return nil, &scw.ResponseError{StatusCode: 400}
	}
	volume.State = instance.VolumeStateAvailable
	volume.Name = req.Name

	s.volumesMap[volume.ID] = volume
	return &instance.CreateVolumeResponse{Volume: volume}, nil
}

func (s *fakeHelper) GetVolume(req *instance.GetVolumeRequest, opts ...scw.RequestOption) (*instance.GetVolumeResponse, error) {
	if vol, ok := s.volumesMap[req.VolumeID]; ok {
		return &instance.GetVolumeResponse{Volume: vol}, nil
	}
	return nil, &scw.ResourceNotFoundError{}
}

func (s *fakeHelper) UpdateVolume(req *instance.UpdateVolumeRequest, opts ...scw.RequestOption) (*instance.UpdateVolumeResponse, error) {
	vol, ok := s.volumesMap[req.VolumeID]
	if !ok {
		return nil, &scw.ResourceNotFoundError{}
	}

	if req.Name != nil {
		vol.Name = *req.Name
	}
	// TODO add size
	return &instance.UpdateVolumeResponse{
		Volume: vol,
	}, nil
}

func (s *fakeHelper) DeleteVolume(req *instance.DeleteVolumeRequest, opts ...scw.RequestOption) error {
	if _, ok := s.volumesMap[req.VolumeID]; ok {
		delete(s.volumesMap, req.VolumeID)
		return nil
	}
	return &scw.ResourceNotFoundError{}
}

func (s *fakeHelper) GetServer(req *instance.GetServerRequest, opts ...scw.RequestOption) (*instance.GetServerResponse, error) {
	if srv, ok := s.serversMap[req.ServerID]; ok {
		return &instance.GetServerResponse{Server: srv}, nil
	}
	return nil, &scw.ResourceNotFoundError{}
}

func (s *fakeHelper) AttachVolume(req *instance.AttachVolumeRequest, opts ...scw.RequestOption) (*instance.AttachVolumeResponse, error) {
	if vol, ok := s.volumesMap[req.VolumeID]; ok {
		if srv, ok := s.serversMap[req.ServerID]; ok {
			// emulate instance error if volume is already attached to server
			for i := 0; i < maxVolumesPerNode; i++ {
				key := fmt.Sprintf("%d", i)
				if existingVol, ok := srv.Volumes[key]; ok && existingVol.ID == vol.ID {
					return nil, &scw.InvalidArgumentsError{}
				}
			}

			// add volume to volumes list
			// We loop through all the possible volume keys (0 to len(volumes))
			// to find a non existing key and assign it to the requested volume.
			// A key should always be found. However we return an error if no keys were found.
			found := false
			for i := 0; i < maxVolumesPerNode; i++ {
				key := fmt.Sprintf("%d", i)
				if _, ok := srv.Volumes[key]; !ok {
					vol.Server = &instance.ServerSummary{
						ID: req.ServerID,
					}
					srv.Volumes[key] = ConvertVolumeVolumeServer(vol)
					found = true
					break
				}
			}
			if !found {
				return nil, fmt.Errorf("could not find key to attach volume %s", req.VolumeID)
			}

			s.devices[path.Join(diskByIDPath, diskSCWPrefix+req.VolumeID)] = &mountpoint{
				block: true,
			}
			return &instance.AttachVolumeResponse{Server: srv}, nil
		}
	}
	return nil, &scw.ResourceNotFoundError{}
}

func (s *fakeHelper) DetachVolume(req *instance.DetachVolumeRequest, opts ...scw.RequestOption) (*instance.DetachVolumeResponse, error) {
	if vol, ok := s.volumesMap[req.VolumeID]; ok {
		if srv, ok := s.serversMap[vol.Server.ID]; ok {
			// remove volume from volumes list
			for key, volume := range srv.Volumes {
				if volume.ID == req.VolumeID {
					vol.Server = nil
					delete(srv.Volumes, key)
					break
				}
			}

			delete(s.devices, path.Join(diskByIDPath, diskSCWPrefix+req.VolumeID))
			return &instance.DetachVolumeResponse{Server: srv}, nil
		}
	}
	return nil, &scw.ResourceNotFoundError{}
}

func (s *fakeHelper) WaitForVolume(req *instance.WaitForVolumeRequest, opts ...scw.RequestOption) (*instance.Volume, error) {
	if vol, ok := s.volumesMap[req.VolumeID]; ok {
		return vol, nil
	}
	return nil, &scw.ResourceNotFoundError{}
}

func (s *fakeHelper) GetSnapshot(req *instance.GetSnapshotRequest, opts ...scw.RequestOption) (*instance.GetSnapshotResponse, error) {
	snapshot, ok := s.snapshotsMap[req.SnapshotID]
	if !ok {
		return nil, &scw.ResourceNotFoundError{}
	}
	return &instance.GetSnapshotResponse{
		Snapshot: snapshot,
	}, nil
}

func (s *fakeHelper) ListSnapshots(req *instance.ListSnapshotsRequest, opts ...scw.RequestOption) (*instance.ListSnapshotsResponse, error) {
	snapshots := make([]*instance.Snapshot, 0)
	for _, snap := range s.snapshotsMap {
		if req.Name == nil || strings.Contains(snap.Name, *req.Name) {
			if snap.State == instance.SnapshotStateSnapshotting {
				snap.State = instance.SnapshotStateAvailable
			}
			snapshots = append(snapshots, snap)
		}
	}
	return &instance.ListSnapshotsResponse{Snapshots: snapshots, TotalCount: uint32(len(snapshots))}, nil
}

func (s *fakeHelper) CreateSnapshot(req *instance.CreateSnapshotRequest, opts ...scw.RequestOption) (*instance.CreateSnapshotResponse, error) {
	if req.Zone == "" {
		req.Zone = s.defaultZone
	}

	volume, ok := s.volumesMap[req.VolumeID]
	if !ok {
		return nil, &scw.ResourceNotFoundError{}
	}
	snapshot := &instance.Snapshot{}
	snapshot.ID = uuid.New().String()
	snapshot.Zone = req.Zone
	snapshot.Name = req.Name
	snapshot.VolumeType = volume.VolumeType
	snapshot.Size = volume.Size
	snapshot.State = instance.SnapshotStateSnapshotting
	snapshot.BaseVolume = &instance.SnapshotBaseVolume{
		ID:   volume.ID,
		Name: volume.Name,
	}
	snapshot.CreationDate = scw.TimePtr(time.Now())
	s.snapshotsMap[snapshot.ID] = snapshot

	return &instance.CreateSnapshotResponse{
		Snapshot: snapshot,
	}, nil
}

func (s *fakeHelper) DeleteSnapshot(req *instance.DeleteSnapshotRequest, opts ...scw.RequestOption) error {
	if _, ok := s.snapshotsMap[req.SnapshotID]; ok {
		delete(s.snapshotsMap, req.SnapshotID)
		return nil
	}
	return &scw.ResourceNotFoundError{}
}

type mountpoint struct {
	targetPath   string
	fsType       string
	mountOptions []string
	block        bool
}

type fakeDiskUtils struct {
	kMounter *kmount.SafeFormatAndMount
	devices  map[string]*mountpoint
}

// FormatAndMount is only used for non block devices
func (s *fakeHelper) FormatAndMount(targetPath string, devicePath string, fsType string, mountOptions []string) error {
	if fsType == "" {
		fsType = defaultFSType
	}

	s.devices[devicePath] = &mountpoint{
		targetPath:   targetPath,
		fsType:       fsType,
		mountOptions: mountOptions,
		block:        false,
	}
	return nil
}

func (s *fakeHelper) Unmount(target string) error {
	return kmount.CleanupMountPoint(target, s.kMounter, true)
}

func (s *fakeHelper) MountToTarget(sourcePath, targetPath, fsType string, mountOptions []string) error {
	if fsType == "" {
		fsType = defaultFSType
	}

	s.devices[sourcePath] = &mountpoint{
		targetPath:   targetPath,
		fsType:       fsType,
		mountOptions: mountOptions,
		block:        strings.HasPrefix(sourcePath, diskByIDPath),
	}
	return nil
}

func (s *fakeHelper) getDeviceType(devicePath string) (string, error) {
	blkidPath, err := exec.LookPath("blkid")
	if err != nil {
		return "", err
	}

	blkidArgs := []string{"-p", "-s", "TYPE", "-s", "PTTYPE", "-o", "export", devicePath}
	blkidOutputBytes, err := exec.Command(blkidPath, blkidArgs...).Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			if exitErr.ExitCode() == 2 {
				// From man page of blkid:
				// If the specified token was not found, or no (specified) devices
				// could be identified, or it is impossible to gather
				// any information about the device identifiers
				// or device content an exit code of 2 is returned.
				return "", nil
			}
		}
		return "", err
	}

	blkidOutput := string(blkidOutputBytes)
	blkidOutputLines := strings.Split(blkidOutput, "\n")
	for _, blkidLine := range blkidOutputLines {
		if len(blkidLine) == 0 {
			continue
		}

		blkidLineSplit := strings.Split(blkidLine, "=")
		if blkidLineSplit[0] == "TYPE" && len(blkidLineSplit[1]) > 0 {
			return blkidLineSplit[1], nil
		}
	}
	// TODO real error???
	return "", nil
}

func (s *fakeHelper) GetDevicePath(volumeID string) (string, error) {
	if _, ok := s.devices[path.Join(diskByIDPath, diskSCWPrefix+volumeID)]; ok {
		return path.Join(diskByIDPath, diskSCWPrefix+volumeID), nil
	}

	return "", os.ErrNotExist
}

func (s *fakeHelper) IsSharedMounted(targetPath string, devicePath string) (bool, error) {
	if targetPath == "" {
		return false, errTargetPathEmpty
	}
	if d, ok := s.devices[devicePath]; ok {
		return d.targetPath == targetPath, nil
	}

	for _, tp := range s.devices {
		if tp.targetPath == targetPath {
			return true, nil
		}
	}

	return false, nil
}

// taken from https://github.com/kubernetes/kubernetes/blob/master/pkg/util/mount/mount_linux.go
func (s *fakeHelper) GetMountInfo(targetPath string) (*mountInfo, error) {
	content, err := utilsio.ConsistentRead(procMountInfoPath, procMountInfoMaxListTries)
	if err != nil {
		return &mountInfo{}, err
	}
	contentStr := string(content)

	for _, line := range strings.Split(contentStr, "\n") {
		if line == "" {
			// the last split() item is empty string following the last \n
			continue
		}
		// See `man proc` for authoritative description of format of the file.
		fields := strings.Fields(line)
		if len(fields) < expectedAtLeastNumFieldsPerMountInfo {
			return nil, fmt.Errorf("wrong number of fields in (expected at least %d, got %d): %s", expectedAtLeastNumFieldsPerMountInfo, len(fields), line)
		}
		if fields[4] != targetPath {
			continue
		}
		id, err := strconv.Atoi(fields[0])
		if err != nil {
			return nil, err
		}
		parentID, err := strconv.Atoi(fields[1])
		if err != nil {
			return nil, err
		}
		info := &mountInfo{
			id:           id,
			parentID:     parentID,
			majorMinor:   fields[2],
			root:         fields[3],
			mountPoint:   fields[4],
			mountOptions: strings.Split(fields[5], ","),
		}
		// All fields until "-" are "optional fields".
		i := 6
		for ; i < len(fields) && fields[i] != "-"; i++ {
			info.optionalFields = append(info.optionalFields, fields[i])
		}
		// Parse the rest 3 fields.
		i++
		if len(fields)-i < 3 {
			return nil, fmt.Errorf("expect 3 fields in %s, got %d", line, len(fields)-i)
		}
		info.fsType = fields[i]
		info.source = fields[i+1]
		info.superOptions = strings.Split(fields[i+2], ",")
		return info, nil
	}
	return &mountInfo{}, nil
}

func (s *fakeHelper) IsBlockDevice(path string) (bool, error) {
	for _, mp := range s.devices {
		if mp.targetPath == path {
			return mp.block, nil
		}
	}
	return false, fmt.Errorf("not found") // enough for csi sanity?
}

func (s *fakeHelper) GetStatfs(path string) (*unix.Statfs_t, error) {
	return &unix.Statfs_t{
		Blocks: 1000,
		Bsize:  4,
		Bfree:  500,
		Files:  1000,
		Ffree:  500,
	}, nil
}

func (s *fakeHelper) Resize(targetPath string, devicePath string) error {
	return nil
}

func (s *fakeHelper) EncryptAndOpenDevice(volumeID string, passphrase string) (string, error) {
	return "", nil
}

func (s *fakeHelper) CloseDevice(volumeID string) error {
	return nil
}

func (s *fakeHelper) GetMappedDevicePath(volumeID string) (string, error) {
	return "", nil
}
