package driver

import (
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/scaleway/scaleway-sdk-go/api/instance/v1"
	kmount "k8s.io/mount-utils"

	"golang.org/x/exp/maps"
	"golang.org/x/exp/slices"
	"golang.org/x/sys/unix"
)

type mountpoint struct {
	targetPath   string
	fsType       string
	mountOptions []string
	block        bool
}

type fakeDiskUtils struct {
	// Pointer to a server. When a NodeController publishes or unpublishes a
	// volume, it must update the volumes in this server.
	server *instance.Server

	devices map[string]*mountpoint

	mux sync.Mutex
}

func newFakeDiskUtils(server *instance.Server) *fakeDiskUtils {
	return &fakeDiskUtils{
		server:  server,
		devices: make(map[string]*mountpoint),
	}
}

func (f *fakeDiskUtils) refreshDevices() {
	// Remove devices no longer present in server object.
	for p := range f.devices {
		if strings.HasPrefix(p, diskByIDPath) &&
			!slices.ContainsFunc(maps.Values(f.server.Volumes), func(v *instance.VolumeServer) bool {
				return devicePath(v.ID) == p
			}) {
			delete(f.devices, p)
		}
	}

	// Add new devices
	for _, v := range f.server.Volumes {
		devPath := devicePath(v.ID)
		if _, ok := f.devices[devPath]; !ok {
			f.devices[devPath] = &mountpoint{
				block: true,
			}
		}
	}
}

func (f *fakeDiskUtils) FormatAndMount(targetPath string, devicePath string, fsType string, mountOptions []string) error {
	f.mux.Lock()
	defer f.mux.Unlock()

	f.refreshDevices()

	if fsType == "" {
		fsType = defaultFSType
	}

	f.devices[devicePath] = &mountpoint{
		targetPath:   targetPath,
		fsType:       fsType,
		mountOptions: mountOptions,
		block:        false,
	}

	return nil
}

// Unmount unmounts the given target
func (f *fakeDiskUtils) Unmount(target string) error {
	if err := kmount.CleanupMountPoint(target, kmount.New(""), true); err != nil {
		return fmt.Errorf("failed to unmount target: %w", err)
	}

	return nil
}

// MountToTarget tries to mount `sourcePath` on `targetPath` as `fsType` with `mountOptions`
func (f *fakeDiskUtils) MountToTarget(sourcePath, targetPath, fsType string, mountOptions []string) error {
	f.mux.Lock()
	defer f.mux.Unlock()

	f.refreshDevices()

	if fsType == "" {
		fsType = defaultFSType
	}

	f.devices[sourcePath] = &mountpoint{
		targetPath:   targetPath,
		fsType:       fsType,
		mountOptions: mountOptions,
		block:        strings.HasPrefix(sourcePath, diskByIDPath),
	}

	return nil
}

// IsBlockDevice returns true if `path` is a block device
func (f *fakeDiskUtils) IsBlockDevice(path string) (bool, error) {
	f.mux.Lock()
	defer f.mux.Unlock()

	f.refreshDevices()

	for _, mp := range f.devices {
		if mp.targetPath == path {
			return mp.block, nil
		}
	}

	return false, fmt.Errorf("not found") // enough for csi sanity?
}

// GetDevicePath returns the path for the specified volumeID
func (f *fakeDiskUtils) GetDevicePath(volumeID string) (string, error) {
	f.mux.Lock()
	defer f.mux.Unlock()

	f.refreshDevices()

	devPath := devicePath(volumeID)

	if _, ok := f.devices[devPath]; ok {
		return devPath, nil
	}

	return "", os.ErrNotExist
}

func (f *fakeDiskUtils) IsMounted(targetPath string) bool {
	f.mux.Lock()
	defer f.mux.Unlock()

	f.refreshDevices()

	for _, tp := range f.devices {
		if tp.targetPath == targetPath {
			return true
		}
	}

	return false
}

// GetStatfs return the statfs struct for the given path
func (f *fakeDiskUtils) GetStatfs(path string) (*unix.Statfs_t, error) {
	return &unix.Statfs_t{
		Blocks: 1000,
		Bsize:  4,
		Bfree:  500,
		Files:  1000,
		Ffree:  500,
	}, nil
}

func (f *fakeDiskUtils) Resize(targetPath string, devicePath, passphrase string) error {
	return nil
}

// IsEncrypted returns true if the device with the given path is encrypted with LUKS
func (f *fakeDiskUtils) IsEncrypted(devicePath string) (bool, error) {
	return false, nil
}

func (f *fakeDiskUtils) EncryptAndOpenDevice(volumeID string, passphrase string) (string, error) {
	return "", nil
}

// CloseDevice closes the encrypted device with the given ID
func (f *fakeDiskUtils) CloseDevice(volumeID string) error {
	return nil
}

// GetMappedDevicePath returns the path on where the encrypted device with the given ID is mapped
func (f *fakeDiskUtils) GetMappedDevicePath(volumeID string) (string, error) {
	return "", nil
}
