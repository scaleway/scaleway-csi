package scaleway

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"
	block "github.com/scaleway/scaleway-sdk-go/api/block/v1"
	"github.com/scaleway/scaleway-sdk-go/api/instance/v1"
	"github.com/scaleway/scaleway-sdk-go/scw"
	"golang.org/x/exp/maps"
)

// Enforce interface.
var _ Interface = &Fake{}

// Fake is a fake Scaleway client. It implements the same interface as the real
// Scaleway client but stores data in-memory.
type Fake struct {
	snapshots   map[string]*block.Snapshot
	volumes     map[string]*block.Volume
	servers     map[string]*instance.Server
	defaultZone scw.Zone
	mux         sync.Mutex
}

// NewFake returns a new fake Scaleway client.
func NewFake(servers []*instance.Server, zone scw.Zone) *Fake {
	if zone == scw.Zone("") {
		zone = scw.ZoneFrPar1
	}

	f := &Fake{
		snapshots:   make(map[string]*block.Snapshot),
		volumes:     make(map[string]*block.Volume),
		servers:     make(map[string]*instance.Server),
		defaultZone: zone,
	}

	for _, s := range servers {
		f.servers[s.ID] = s
	}

	return f
}

// mapPaginatedRange returns elements of a map according to the pagination parameters
// start (index of the first element) and max (amount of elements to return).
// The f callback function allows to filter elements.
func mapPaginatedRange[T any](m map[string]T, f func(T) bool, start, max uint32) (list []T, next string) {
	keys := maps.Keys(m)
	sort.Strings(keys)

	// Return now if start index is above max index or we want no element.
	if int(start) >= len(keys) {
		return
	}

	for i, key := range keys[start:] {
		if max != 0 && len(list) == int(max) {
			next = strconv.Itoa(int(start) + i)
			break
		}

		if f(m[key]) {
			list = append(list, m[key])
		}
	}

	return
}

func (f *Fake) AttachVolume(ctx context.Context, serverID string, volumeID string, zone scw.Zone) error {
	f.mux.Lock()
	defer f.mux.Unlock()

	if zone == scw.Zone("") {
		zone = f.defaultZone
	}

	s, ok := f.servers[serverID]
	if !ok || ok && s.Zone != zone {
		return &scw.ResourceNotFoundError{Resource: serverResource, ResourceID: serverID}
	}

	v, ok := f.volumes[volumeID]
	if !ok || ok && v.Zone != zone {
		return &scw.ResourceNotFoundError{Resource: volumeResource, ResourceID: volumeID}
	}

	if len(s.Volumes) == MaxVolumesPerNode {
		return errors.New("server has reached max volume capacity")
	}

	if v.Status != block.VolumeStatusAvailable {
		return errors.New("volume is not available")
	}

	v.References = append(v.References, &block.Reference{
		ID:                  uuid.NewString(),
		ProductResourceType: InstanceServerProductResourceType,
		ProductResourceID:   serverID,
	})

	v.Status = block.VolumeStatusInUse

	for i := 0; i <= len(s.Volumes); i++ {
		key := fmt.Sprintf("%d", i)
		if _, ok := s.Volumes[key]; !ok {
			s.Volumes[key] = &instance.VolumeServer{
				ID:         volumeID,
				VolumeType: instance.VolumeServerVolumeType("sbs_volume"),
			}
			break
		}
	}

	return nil
}

func (f *Fake) CreateSnapshot(ctx context.Context, name string, volumeID string, zone scw.Zone) (*block.Snapshot, error) {
	f.mux.Lock()
	defer f.mux.Unlock()

	if zone == scw.Zone("") {
		zone = f.defaultZone
	}

	v, ok := f.volumes[volumeID]
	if !ok || ok && v.Zone != zone {
		return nil, errors.New("source volume not found")
	}

	snapshot := &block.Snapshot{
		ID:   uuid.NewString(),
		Name: name,
		ParentVolume: &block.SnapshotParentVolume{
			ID:   volumeID,
			Name: v.Name,
			Type: v.Type,
		},
		Size:      v.Size,
		CreatedAt: scw.TimePtr(time.Now()),
		UpdatedAt: scw.TimePtr(time.Now()),
		Status:    block.SnapshotStatusAvailable,
		Zone:      zone,
	}

	f.snapshots[snapshot.ID] = snapshot

	return snapshot, nil
}

func (f *Fake) CreateVolume(ctx context.Context, name string, snapshotID string, size int64, perfIOPS *uint32, zone scw.Zone) (*block.Volume, error) {
	f.mux.Lock()
	defer f.mux.Unlock()

	if zone == scw.Zone("") {
		zone = f.defaultZone
	}

	scwSize, err := NewSize(size)
	if err != nil {
		return nil, err
	}

	volume := &block.Volume{
		ID:        uuid.NewString(),
		Name:      name,
		Type:      "nvme_5k",
		CreatedAt: scw.TimePtr(time.Now()),
		UpdatedAt: scw.TimePtr(time.Now()),
		Status:    block.VolumeStatusAvailable,
		Zone:      zone,
		Specs: &block.VolumeSpecifications{
			Class: block.StorageClassSbs,
		},
	}

	if perfIOPS == nil {
		volume.Specs.PerfIops = scw.Uint32Ptr(5000)
	}

	if snapshotID != "" {
		s, ok := f.snapshots[snapshotID]
		if !ok || ok && s.Zone != zone {
			return nil, &scw.ResourceNotFoundError{Resource: snapshotResource, ResourceID: snapshotID}
		}

		volume.ParentSnapshotID = scw.StringPtr(snapshotID)
		volume.Size = s.Size
	} else {
		volume.Size = scwSize
	}

	f.volumes[volume.ID] = volume

	return volume, nil
}

func (f *Fake) DeleteSnapshot(ctx context.Context, snapshotID string, zone scw.Zone) error {
	f.mux.Lock()
	defer f.mux.Unlock()

	if zone == scw.Zone("") {
		zone = f.defaultZone
	}

	if s, ok := f.snapshots[snapshotID]; ok && s.Zone == zone {
		delete(f.snapshots, snapshotID)
	}

	return &scw.ResourceNotFoundError{Resource: snapshotResource, ResourceID: snapshotID}
}

func (f *Fake) DeleteVolume(ctx context.Context, volumeID string, zone scw.Zone) error {
	f.mux.Lock()
	defer f.mux.Unlock()

	if zone == scw.Zone("") {
		zone = f.defaultZone
	}

	if s, ok := f.volumes[volumeID]; ok && s.Zone == zone {
		delete(f.volumes, volumeID)
	}

	return &scw.ResourceNotFoundError{Resource: volumeResource, ResourceID: volumeID}
}

func (f *Fake) DetachVolume(ctx context.Context, volumeID string, zone scw.Zone) error {
	f.mux.Lock()
	defer f.mux.Unlock()

	if zone == scw.Zone("") {
		zone = f.defaultZone
	}

	v, ok := f.volumes[volumeID]
	if !ok || ok && v.Zone != zone {
		return &scw.ResourceNotFoundError{Resource: volumeResource, ResourceID: volumeID}
	}

	if v.Status != block.VolumeStatusInUse || len(v.References) == 0 {
		return errors.New("volume not in required state, it is not in use")
	}

	s, ok := f.servers[v.References[0].ProductResourceID]
	if !ok || ok && s.Zone != zone {
		return &scw.ResourceNotFoundError{Resource: serverResource, ResourceID: v.References[0].ProductResourceID}
	}

	for k, vol := range s.Volumes {
		if vol.ID == volumeID {
			delete(s.Volumes, k)
			break
		}
	}

	v.References = nil
	v.Status = block.VolumeStatusAvailable

	return nil
}

func (f *Fake) GetServer(ctx context.Context, serverID string, zone scw.Zone) (*instance.Server, error) {
	f.mux.Lock()
	defer f.mux.Unlock()

	if zone == scw.Zone("") {
		zone = f.defaultZone
	}

	if s, ok := f.servers[serverID]; ok && s.Zone == zone {
		return f.servers[serverID], nil
	}

	return nil, &scw.ResourceNotFoundError{Resource: serverResource, ResourceID: serverID}
}

func (f *Fake) GetSnapshot(ctx context.Context, snapshotID string, zone scw.Zone) (*block.Snapshot, error) {
	f.mux.Lock()
	defer f.mux.Unlock()

	if zone == scw.Zone("") {
		zone = f.defaultZone
	}

	if s, ok := f.snapshots[snapshotID]; ok && s.Zone == zone {
		return s, nil
	}

	return nil, &scw.ResourceNotFoundError{Resource: snapshotResource, ResourceID: snapshotID}
}

func (f *Fake) GetSnapshotByName(ctx context.Context, name string, sourceVolumeID string, zone scw.Zone) (*block.Snapshot, error) {
	f.mux.Lock()
	defer f.mux.Unlock()

	if zone == scw.Zone("") {
		zone = f.defaultZone
	}

	for _, s := range f.snapshots {
		if s.Name == name && s.Zone == zone {
			if s.ParentVolume != nil && s.ParentVolume.ID != sourceVolumeID {
				return nil, ErrSnapshotExists
			}

			return s, nil
		}
	}

	return nil, ErrSnapshotNotFound
}

func (f *Fake) GetVolume(ctx context.Context, volumeID string, zone scw.Zone) (*block.Volume, error) {
	f.mux.Lock()
	defer f.mux.Unlock()

	if zone == scw.Zone("") {
		zone = f.defaultZone
	}

	if s, ok := f.volumes[volumeID]; ok && s.Zone == zone {
		return f.volumes[volumeID], nil
	}

	return nil, &scw.ResourceNotFoundError{Resource: volumeResource, ResourceID: volumeID}
}

func (f *Fake) GetVolumeByName(ctx context.Context, name string, size scw.Size, zone scw.Zone) (*block.Volume, error) {
	f.mux.Lock()
	defer f.mux.Unlock()

	if zone == scw.Zone("") {
		zone = f.defaultZone
	}

	for _, v := range f.volumes {
		if v.Name == name && v.Zone == zone {
			if v.Size != size {
				return nil, ErrVolumeDifferentSize
			}

			return v, nil
		}
	}

	return nil, ErrVolumeNotFound
}

func (f *Fake) ListSnapshots(ctx context.Context, start uint32, max uint32) ([]*block.Snapshot, string, error) {
	f.mux.Lock()
	defer f.mux.Unlock()

	results, next := mapPaginatedRange(f.snapshots, func(_ *block.Snapshot) bool { return true }, start, max)

	return results, next, nil
}

func (f *Fake) ListSnapshotsBySourceVolume(ctx context.Context, start uint32, max uint32, sourceVolumeID string, sourceVolumeZone scw.Zone) ([]*block.Snapshot, string, error) {
	f.mux.Lock()
	defer f.mux.Unlock()

	results, next := mapPaginatedRange(f.snapshots, func(s *block.Snapshot) bool {
		return s.ParentVolume != nil && s.ParentVolume.ID == sourceVolumeID && s.Zone == sourceVolumeZone
	}, start, max)

	return results, next, nil
}

func (f *Fake) ListVolumes(ctx context.Context, start uint32, max uint32) ([]*block.Volume, string, error) {
	f.mux.Lock()
	defer f.mux.Unlock()

	results, next := mapPaginatedRange(f.volumes, func(v *block.Volume) bool { return true }, start, max)

	return results, next, nil
}

func (f *Fake) ResizeVolume(ctx context.Context, volumeID string, zone scw.Zone, size int64) error {
	f.mux.Lock()
	defer f.mux.Unlock()

	if zone == scw.Zone("") {
		zone = f.defaultZone
	}

	scwSize, err := NewSize(size)
	if err != nil {
		return err
	}

	if s, ok := f.volumes[volumeID]; ok && s.Zone == zone {
		if scwSize < f.volumes[volumeID].Size {
			return errors.New("new volume size is less than current volume size")
		}

		f.volumes[volumeID].Size = scwSize

		return nil
	}

	return &scw.ResourceNotFoundError{Resource: volumeResource, ResourceID: volumeID}
}

func (f *Fake) WaitForSnapshot(ctx context.Context, snapshotID string, zone scw.Zone) (*block.Snapshot, error) {
	return f.GetSnapshot(ctx, snapshotID, zone)
}
