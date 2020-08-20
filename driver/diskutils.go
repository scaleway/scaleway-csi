package driver

import (
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/docker/docker/pkg/mount"
	"golang.org/x/sys/unix"
	"k8s.io/klog"
	utilsio "k8s.io/utils/io"
)

const (
	diskByIDPath         = "/dev/disk/by-id"
	diskSCWPrefix        = "scsi-0SCW_b_ssd_volume-"
	diskLuksMapperPrefix = "scw-luks-"
	diskLuksMapperPath   = "/dev/mapper/"

	defaultFSType = "ext4"

	procMountInfoMaxListTries             = 3
	procMountsExpectedNumFieldsPerLine    = 6
	procMountInfoExpectedAtLeastNumFields = 10
	procMountsPath                        = "/proc/mounts"
	procMountInfoPath                     = "/proc/self/mountinfo"
	expectedAtLeastNumFieldsPerMountInfo  = 10
)

type DiskUtils interface {
	// FormatAndMount tries to mount `devicePath` on `targetPath` as `fsType` with `mountOptions`
	// If it fails it will try to format `devicePath` as `fsType` first and retry
	FormatAndMount(targetPath string, devicePath string, fsType string, mountOptions []string) error

	// MountToTarget tries to mount `sourcePath` on `targetPath` as `fsType` with `mountOptions`
	MountToTarget(sourcePath, targetPath, fsType string, mountOptions []string) error

	// IsBlockDevice returns true if `path` is a block device
	IsBlockDevice(path string) (bool, error)

	// GetDevicePath returns the path for the specified volumeID
	GetDevicePath(volumeID string) (string, error)

	// IsSharedMounted returns true is `devicePath` is shared mounted on `targetPath`
	IsSharedMounted(targetPath string, devicePath string) (bool, error)

	// GetMountInfo returns a mount informations for `targetPath`
	// taken from https://github.com/kubernetes/kubernetes/blob/master/pkg/util/mount/mount_linux.go
	GetMountInfo(targetPath string) (*mountInfo, error)

	// GetStatfs return the statfs struct for the given path
	GetStatfs(path string) (*unix.Statfs_t, error)

	// Resize resizes the given volumes
	Resize(targetPath string, devicePath string) error

	// EncryptAndOpenDevice encrypts the volume with the given ID with the given passphrase and open it
	// If the device is already encrypted (LUKS header present), it will only open the device
	EncryptAndOpenDevice(volumeID string, passphrase string) (string, error)

	// CloseDevice closes the encrypted device with the given ID
	CloseDevice(volumeID string) error

	// GetMappedDevicePath returns the path on where the encrypted device with the given ID is mapped
	GetMappedDevicePath(volumeID string) (string, error)
}

type diskUtils struct{}

func newDiskUtils() *diskUtils {
	return &diskUtils{}
}

func (d *diskUtils) EncryptAndOpenDevice(volumeID string, passphrase string) (string, error) {
	encryptedDevicePath, err := d.GetMappedDevicePath(volumeID)
	if err != nil {
		return "", err
	}

	if encryptedDevicePath != "" {
		// device is already encrypted and open
		return encryptedDevicePath, nil
	}

	// let's check if the device is aready a luks device
	devicePath, err := d.GetDevicePath(volumeID)
	if err != nil {
		return "", fmt.Errorf("error getting device path for volume %s: %w", volumeID, err)
	}
	isLuks, err := luksIsLuks(devicePath)
	if err != nil {
		return "", fmt.Errorf("error checking if device %s is a luks device: %w", devicePath, err)
	}

	if !isLuks {
		// need to format the device
		err = luksFormat(devicePath, passphrase)
		if err != nil {
			return "", fmt.Errorf("error formating device %s: %w", devicePath, err)
		}
	}

	err = luksOpen(devicePath, diskLuksMapperPrefix+volumeID, passphrase)
	if err != nil {
		return "", fmt.Errorf("error luks opening device %s: %w", devicePath, err)
	}
	return diskLuksMapperPath + diskLuksMapperPrefix + volumeID, nil
}

func (d *diskUtils) CloseDevice(volumeID string) error {
	encryptedDevicePath, err := d.GetMappedDevicePath(volumeID)
	if err != nil {
		return err
	}

	if encryptedDevicePath != "" {
		err = luksClose(diskLuksMapperPrefix + volumeID)
		if err != nil {
			return fmt.Errorf("error luks closing %s: %w", encryptedDevicePath, err)
		}
	}

	return nil
}

func (d *diskUtils) GetMappedDevicePath(volumeID string) (string, error) {
	mappedPath := diskLuksMapperPath + diskLuksMapperPrefix + volumeID
	_, err := os.Stat(mappedPath)
	if err != nil {
		// if the mapped device does not exists on disk, it's not open
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("error checking stat on %s: %w", mappedPath, err)
	}

	statusStdout, err := luksStatus(diskLuksMapperPrefix + volumeID)
	if err != nil {
		return "", fmt.Errorf("error checking luks status on %s: %w", diskLuksMapperPrefix+volumeID, err)
	}

	statusLines := strings.Split(string(statusStdout), "\n")

	if len(statusLines) == 0 {
		return "", fmt.Errorf("luksStatus stdout have 0 lines")
	}

	// first line should look like
	// /dev/mapper/<name> is active.
	if !strings.HasSuffix(statusLines[0], "is active.") {
		// when a device is not active, an error exit code is thrown
		// something went wrong if we reach here
		return "", fmt.Errorf("luksStatus returned ok, but device %s is not active", diskLuksMapperPrefix+volumeID)
	}

	return mappedPath, nil
}

func (d *diskUtils) FormatAndMount(targetPath string, devicePath string, fsType string, mountOptions []string) error {
	if fsType == "" {
		fsType = defaultFSType
	}

	klog.V(4).Infof("Attempting to mount %s on %s with type %s", devicePath, targetPath, fsType)
	err := d.MountToTarget(devicePath, targetPath, fsType, mountOptions)
	if err != nil {
		klog.V(4).Infof("Mount attempt failed, trying to format device %s with type %s", devicePath, fsType)
		realFsType, fsErr := d.getDeviceType(devicePath)
		if fsErr != nil {
			return fsErr
		}

		if realFsType == "" {
			fsErr = d.formatDevice(devicePath, fsType)
			if fsErr != nil {
				return fsErr
			}
			return d.MountToTarget(devicePath, targetPath, fsType, mountOptions)
		}
		return err
	}
	return nil
}

func (d *diskUtils) MountToTarget(sourcePath, targetPath, fsType string, mountOptions []string) error {
	if fsType == "" {
		fsType = defaultFSType
	}

	return mount.Mount(sourcePath, targetPath, fsType, strings.Join(mountOptions, ","))
}

func (d *diskUtils) formatDevice(devicePath string, fsType string) error {
	if fsType == "" {
		fsType = defaultFSType
	}

	mkfsPath, err := exec.LookPath("mkfs." + fsType)
	if err != nil {
		return err
	}

	mkfsArgs := []string{devicePath}
	if fsType == "ext4" || fsType == "ext3" {
		mkfsArgs = []string{
			"-F",  // Force mke2fs to create a filesystem
			"-m0", // 0 blocks reserved for the super-user
			devicePath,
		}
	}

	return exec.Command(mkfsPath, mkfsArgs...).Run()
}

func (d *diskUtils) getDeviceType(devicePath string) (string, error) {
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

func (d *diskUtils) GetDevicePath(volumeID string) (string, error) {
	devicePath := path.Join(diskByIDPath, diskSCWPrefix+volumeID)
	realDevicePath, err := filepath.EvalSymlinks(devicePath)
	if err != nil {
		return "", err
	}

	deviceInfo, err := os.Stat(realDevicePath)
	if err != nil {
		return "", err
	}

	deviceMode := deviceInfo.Mode()
	if os.ModeDevice != deviceMode&os.ModeDevice || os.ModeCharDevice == deviceMode&os.ModeCharDevice {
		return "", errDevicePathIsNotDevice
	}

	return devicePath, nil
}

func (d *diskUtils) IsSharedMounted(targetPath string, devicePath string) (bool, error) {
	if targetPath == "" {
		return false, errTargetPathEmpty
	}

	mountInfo, err := d.GetMountInfo(targetPath)
	if err != nil {
		return false, err
	}

	if mountInfo == nil {
		return false, nil
	}

	sharedMounted := false
	for _, optionalField := range mountInfo.optionalFields {
		tag := strings.Split(optionalField, ":")
		if tag != nil && tag[0] == "shared" {
			sharedMounted = true
		}
	}
	if !sharedMounted {
		return false, errTargetNotSharedMounter
	}

	if devicePath != "" && mountInfo.source != devicePath {
		return false, errTargetNotMounterOnRightDevice
	}

	return true, nil
}

// taken from https://github.com/kubernetes/kubernetes/blob/master/pkg/util/mount/mount_linux.go
// This represents a single line in /proc/<pid>/mountinfo.
type mountInfo struct {
	// Unique ID for the mount (maybe reused after umount).
	id int
	// The ID of the parent mount (or of self for the root of this mount namespace's mount tree).
	parentID int
	// The value of `st_dev` for files on this filesystem.
	majorMinor string
	// The pathname of the directory in the filesystem which forms the root of this mount.
	root string
	// Mount source, filesystem-specific information. e.g. device, tmpfs name.
	source string
	// Mount point, the pathname of the mount point.
	mountPoint string
	// Optional fieds, zero or more fields of the form "tag[:value]".
	optionalFields []string
	// The filesystem type in the form "type[.subtype]".
	fsType string
	// Per-mount options.
	mountOptions []string
	// Per-superblock options.
	superOptions []string
}

// taken from https://github.com/kubernetes/kubernetes/blob/master/pkg/util/mount/mount_linux.go
func (d *diskUtils) GetMountInfo(targetPath string) (*mountInfo, error) {
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
	return nil, nil
}

func (d *diskUtils) IsBlockDevice(path string) (bool, error) {
	realPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		return false, err
	}

	deviceInfo, err := os.Stat(realPath)
	if err != nil {
		return false, err
	}

	deviceMode := deviceInfo.Mode()
	if os.ModeDevice != deviceMode&os.ModeDevice || os.ModeCharDevice == deviceMode&os.ModeCharDevice {
		return false, nil
	}

	return true, nil

}

func (d *diskUtils) GetStatfs(path string) (*unix.Statfs_t, error) {
	fs := &unix.Statfs_t{}
	err := unix.Statfs(path, fs)
	return fs, err
}

func (d *diskUtils) Resize(targetPath string, devicePath string) error {
	mountInfo, err := d.GetMountInfo(targetPath)
	if err != nil {
		return err
	}

	switch mountInfo.fsType {
	case "ext3", "ext4":
		resize2fsPath, err := exec.LookPath("resize2fs")
		if err != nil {
			return err
		}
		resize2fsArgs := []string{devicePath}
		return exec.Command(resize2fsPath, resize2fsArgs...).Run()
	case "xfs":
		xfsGrowfsPath, err := exec.LookPath("xfs_growfs")
		if err != nil {
			return err
		}
		xfsGrowfsArgs := []string{"-d", targetPath}
		return exec.Command(xfsGrowfsPath, xfsGrowfsArgs...).Run()
	}

	return fmt.Errorf("filesystem %s does not support resizing", mountInfo.fsType)
}
