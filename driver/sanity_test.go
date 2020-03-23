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
	"github.com/kubernetes-csi/csi-test/pkg/sanity"
	"github.com/scaleway/scaleway-csi/scaleway"
	"github.com/scaleway/scaleway-sdk-go/api/instance/v1"
	"github.com/scaleway/scaleway-sdk-go/scw"
	"golang.org/x/sys/unix"
	utilsio "k8s.io/utils/io"
)

func TestSanityCSI(t *testing.T) {
	endpoint := "/tmp/csi-testing.sock"
	nodeID := "fb094b6a-a732-4d5f-8283-bd6726ff5938"
	volumesMap := make(map[string]*instance.Volume)
	serversMap := map[string]*instance.Server{
		nodeID: &instance.Server{
			ID:      nodeID,
			Volumes: make(map[string]*instance.Volume),
			Zone:    scw.ZoneFrPar1,
		},
	}
	snapshotsMap := make(map[string]*instance.Snapshot)
	diskUtilsDevices := make(map[string]*mountpoint)

	driverConfig := &DriverConfig{
		Endpoint: fmt.Sprintf("unix://%s", endpoint),
		Mode:     AllMode,
	}

	driver := &Driver{
		config: driverConfig,
		controllerService: controllerService{
			scaleway: &scaleway.Scaleway{
				InstanceAPI: &fakeInstanceAPI{
					volumesMap:   volumesMap,
					serversMap:   serversMap,
					snapshotsMap: snapshotsMap,
					defaultZone:  scw.ZoneFrPar1,
				},
			},
			config: driverConfig,
		},
		nodeService: nodeService{
			nodeID:   nodeID,
			nodeZone: scw.ZoneFrPar1,
			diskUtils: &fakeDiskUtils{
				devices: diskUtilsDevices,
			},
		},
	}

	go driver.Run() // an error here woule fail the test anyway since the grpc server would not be started

	config := &sanity.Config{
		TargetPath:  os.TempDir() + "/csi-testing-target",
		StagingPath: os.TempDir() + "/csi-testing-staging",
		RemoveTargetPath: func(path string) error {
			return os.RemoveAll(path)
		},
		RemoveStagingPath: func(path string) error {
			return os.RemoveAll(path)
		},
		Address: endpoint,
		IDGen:   sanity.DefaultIDGenerator{},
	}
	sanity.Test(t, config)
	driver.srv.GracefulStop()
	os.RemoveAll(endpoint)
}

type fakeInstanceAPI struct {
	volumesMap   map[string]*instance.Volume
	serversMap   map[string]*instance.Server
	snapshotsMap map[string]*instance.Snapshot
	defaultZone  scw.Zone
}

func (s *fakeInstanceAPI) ListVolumes(req *instance.ListVolumesRequest, opts ...scw.RequestOption) (*instance.ListVolumesResponse, error) {
	volumes := make([]*instance.Volume, 0)
	for _, v := range s.volumesMap {
		if req.Name == nil || strings.Contains(v.Name, *req.Name) {
			volumes = append(volumes, v)
		}
	}
	return &instance.ListVolumesResponse{Volumes: volumes, TotalCount: uint32(len(volumes))}, nil
}

func (s *fakeInstanceAPI) CreateVolume(req *instance.CreateVolumeRequest, opts ...scw.RequestOption) (*instance.CreateVolumeResponse, error) {
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
			return nil, &scw.ResponseError{StatusCode: 404}
		}
		volume.Size = baseVol.Size
	} else if req.BaseSnapshot != nil {
		baseSnap, ok := s.snapshotsMap[*req.BaseSnapshot]
		if !ok {
			return nil, &scw.ResponseError{StatusCode: 404}
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

func (s *fakeInstanceAPI) GetVolume(req *instance.GetVolumeRequest, opts ...scw.RequestOption) (*instance.GetVolumeResponse, error) {
	if vol, ok := s.volumesMap[req.VolumeID]; ok {
		return &instance.GetVolumeResponse{Volume: vol}, nil
	}
	return nil, &scw.ResponseError{StatusCode: 404}
}

func (s *fakeInstanceAPI) DeleteVolume(req *instance.DeleteVolumeRequest, opts ...scw.RequestOption) error {
	if _, ok := s.volumesMap[req.VolumeID]; ok {
		delete(s.volumesMap, req.VolumeID)
		return nil
	}
	return &scw.ResponseError{StatusCode: 404}
}

func (s *fakeInstanceAPI) GetServer(req *instance.GetServerRequest, opts ...scw.RequestOption) (*instance.GetServerResponse, error) {
	if srv, ok := s.serversMap[req.ServerID]; ok {
		return &instance.GetServerResponse{Server: srv}, nil
	}
	return nil, &scw.ResponseError{StatusCode: 404}
}

func (s *fakeInstanceAPI) AttachVolume(req *instance.AttachVolumeRequest, opts ...scw.RequestOption) (*instance.AttachVolumeResponse, error) {
	if vol, ok := s.volumesMap[req.VolumeID]; ok {
		if srv, ok := s.serversMap[req.ServerID]; ok {
			for i := 0; i <= len(srv.Volumes); i++ {
				key := fmt.Sprintf("%d", i)
				if _, ok := srv.Volumes[key]; !ok {
					srv.Volumes[key] = vol
					break
				}
			} // an empty slot will always be found
			return &instance.AttachVolumeResponse{Server: srv}, nil
		}
	}
	return nil, &scw.ResponseError{StatusCode: 404}
}

func (s *fakeInstanceAPI) DetachVolume(req *instance.DetachVolumeRequest, opts ...scw.RequestOption) (*instance.DetachVolumeResponse, error) {
	if _, ok := s.volumesMap[req.VolumeID]; ok {
		delete(s.volumesMap, req.VolumeID)
		return &instance.DetachVolumeResponse{}, nil
	}
	return nil, &scw.ResponseError{StatusCode: 404}
}

func (s *fakeInstanceAPI) GetSnapshot(req *instance.GetSnapshotRequest, opts ...scw.RequestOption) (*instance.GetSnapshotResponse, error) {
	snapshot, ok := s.snapshotsMap[req.SnapshotID]
	if !ok {
		return nil, &scw.ResponseError{StatusCode: 404}
	}
	return &instance.GetSnapshotResponse{
		Snapshot: snapshot,
	}, nil
}

func (s *fakeInstanceAPI) ListSnapshots(req *instance.ListSnapshotsRequest, opts ...scw.RequestOption) (*instance.ListSnapshotsResponse, error) {
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

func (s *fakeInstanceAPI) CreateSnapshot(req *instance.CreateSnapshotRequest, opts ...scw.RequestOption) (*instance.CreateSnapshotResponse, error) {
	if req.Zone == "" {
		req.Zone = s.defaultZone
	}

	volume, ok := s.volumesMap[req.VolumeID]
	if !ok {
		return nil, &scw.ResponseError{StatusCode: 404}
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
	snapshot.CreationDate = time.Now()
	s.snapshotsMap[snapshot.ID] = snapshot

	return &instance.CreateSnapshotResponse{
		Snapshot: snapshot,
	}, nil
}

func (s *fakeInstanceAPI) DeleteSnapshot(req *instance.DeleteSnapshotRequest, opts ...scw.RequestOption) error {
	if _, ok := s.snapshotsMap[req.SnapshotID]; ok {
		delete(s.snapshotsMap, req.SnapshotID)
		return nil
	}
	return &scw.ResponseError{StatusCode: 404}
}

type mountpoint struct {
	path         string
	fsType       string
	mountOptions []string
	block        bool
}

type fakeDiskUtils struct {
	devices map[string]*mountpoint
}

func (d *fakeDiskUtils) FormatAndMount(targetPath string, devicePath string, fsType string, mountOptions []string) error {
	return d.MountToTarget(devicePath, targetPath, fsType, mountOptions)
}

func (d *fakeDiskUtils) MountToTarget(sourcePath, targetPath, fsType string, mountOptions []string) error {
	if fsType == "" {
		fsType = defaultFSType
	}

	d.devices[targetPath] = &mountpoint{
		path:         sourcePath,
		fsType:       fsType,
		mountOptions: mountOptions,
		block:        strings.HasPrefix(sourcePath, diskByIDPath),
	}
	return nil
}

func (d *fakeDiskUtils) getDeviceType(devicePath string) (string, error) {
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

func (d *fakeDiskUtils) GetDevicePath(volumeID string) (string, error) {
	return path.Join(diskByIDPath, diskSCWPrefix+volumeID), nil
}

func (d *fakeDiskUtils) IsSharedMounted(targetPath string, devicePath string) (bool, error) {
	if targetPath == "" {
		return false, errTargetPathEmpty
	}
	if _, ok := d.devices[targetPath]; ok {
		return true, nil
	}

	return false, nil
}

// taken from https://github.com/kubernetes/kubernetes/blob/master/pkg/util/mount/mount_linux.go
func (d *fakeDiskUtils) GetMountInfo(targetPath string) (*mountInfo, error) {
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

func (d *fakeDiskUtils) IsBlockDevice(path string) (bool, error) {
	for _, mp := range d.devices {
		if mp.path == path {
			return mp.block, nil
		}
	}
	return false, fmt.Errorf("not found") // enough for csi sanity?
}

func (d *fakeDiskUtils) GetStatfs(path string) (*unix.Statfs_t, error) {
	return &unix.Statfs_t{
		Blocks: 1000,
		Bsize:  4,
		Bfree:  500,
		Files:  1000,
		Ffree:  500,
	}, nil
}
