package driver

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/scaleway/scaleway-csi/pkg/scaleway"
	block "github.com/scaleway/scaleway-sdk-go/api/block/v1"
	"github.com/scaleway/scaleway-sdk-go/api/instance/v1"
	"github.com/scaleway/scaleway-sdk-go/scw"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
	"k8s.io/klog/v2"
)

// UserAgent returns the CSI driver user-agent.
func UserAgent() (userAgent string) {
	userAgent = fmt.Sprintf("%s %s (%s)", DriverName, driverVersion, gitCommit)
	if extraUA := os.Getenv(ExtraUserAgentEnv); extraUA != "" {
		userAgent = userAgent + " " + extraUA
	}

	return
}

// expandZonalID concatenates the ID and zone of a resource to create a zonal ID.
func expandZonalID(id string, zone scw.Zone) string {
	return fmt.Sprintf("%s/%s", zone, id)
}

// ExtractIDAndZone takes a zonal ID and returns the ID and zone of a resource.
func ExtractIDAndZone(id string) (string, scw.Zone, error) {
	if id == "" {
		return "", scw.Zone(""), status.Errorf(codes.InvalidArgument, "ID must not be empty")
	}

	splitID := strings.Split(id, "/")
	if len(splitID) > 2 {
		return "", scw.Zone(""), status.Errorf(codes.InvalidArgument, "ID %q is not correctly formatted", id)
	} else if len(splitID) == 1 {
		return splitID[0], scw.Zone(""), nil
	} else { // id like zone/uuid
		zone, err := scw.ParseZone(splitID[0])
		if err != nil {
			klog.Warningf("wrong zone in ID %q, will try default zone", id)
			return splitID[1], scw.Zone(""), nil //nolint:nilerr
		}
		return splitID[1], zone, nil
	}
}

// chooseZones returns the most appropriate zones according to the accessibility
// requirements and snapshot zone.
func chooseZones(accessibilityRequirements *csi.TopologyRequirement, snapshotZone scw.Zone) ([]scw.Zone, error) {
	if accessibilityRequirements != nil {
		requestedZones := map[string]scw.Zone{}
		for _, req := range accessibilityRequirements.GetRequisite() {
			topologyKeys := req.GetSegments()
			for topologyKey, topologyValue := range topologyKeys {
				switch topologyKey {
				case ZoneTopologyKey:
					zone, err := scw.ParseZone(topologyValue)
					if err != nil {
						klog.Warningf("the given value for requisite %s: %s is not a valid zone", ZoneTopologyKey, topologyValue)
						continue
					}
					if snapshotZone == scw.Zone("") || snapshotZone == zone {
						requestedZones[topologyValue] = zone
					}
				default:
					klog.Warningf("unknow topology key %s for requisite", topologyKey)
				}
			}
		}

		preferredZones := []scw.Zone{}
		preferredZonesMap := map[string]scw.Zone{}
		for _, pref := range accessibilityRequirements.GetPreferred() {
			topologyKeys := pref.GetSegments()
			for topologyKey, topologyValue := range topologyKeys {
				switch topologyKey {
				case ZoneTopologyKey:
					zone, err := scw.ParseZone(topologyValue)
					if err != nil {
						klog.Warningf("the given value for preferred %s: %s is not a valid zone", ZoneTopologyKey, topologyValue)
						continue
					}
					if snapshotZone == scw.Zone("") || snapshotZone == zone {
						if _, ok := preferredZonesMap[topologyValue]; !ok {
							if accessibilityRequirements.GetRequisite() != nil {
								if _, ok := requestedZones[topologyValue]; !ok {
									return nil, status.Errorf(codes.InvalidArgument, "%s: %s is specified in preferred but not in requisite", topologyKey, topologyValue)
								}
								delete(requestedZones, topologyValue)
							}

							preferredZonesMap[topologyValue] = zone
							preferredZones = append(preferredZones, zone)
						}
					}
				default:
					klog.Warningf("unknow topology key %s for preferred", topologyKey)
				}
			}
		}

		for _, requestedZone := range requestedZones {
			preferredZones = append(preferredZones, requestedZone)
		}

		if snapshotZone != scw.Zone("") && len(preferredZones) != 1 {
			return nil, status.Error(codes.ResourceExhausted, "desired volume content source and desired topology are not compatible, different zones")
		}
		return preferredZones, nil
	}

	if snapshotZone != scw.Zone("") {
		return []scw.Zone{snapshotZone}, nil
	}

	return []scw.Zone{}, nil
}

// validateVolumeCapabilities makes sure the provided volume capabilities are
// valid and supported by the driver. If optional is false and no volumeCapabilities
// are provided, an error is returned.
func validateVolumeCapabilities(volumeCapabilities []*csi.VolumeCapability, optional bool) error {
	if !optional && len(volumeCapabilities) == 0 {
		return errors.New("no volumeCapabilities were provided")
	}

	for i, volumeCapability := range volumeCapabilities {
		if _, _, err := validateVolumeCapability(volumeCapability); err != nil {
			return fmt.Errorf("unsupported volume capability at index %d: %w", i, err)
		}
	}

	return nil
}

// validateVolumeCapability validates a single volume capacity.
func validateVolumeCapability(volumeCapability *csi.VolumeCapability) (block bool, mount bool, err error) {
	mode := volumeCapability.GetAccessMode().GetMode()
	if !slices.Contains(supportedAccessModes, mode) {
		return false, false, fmt.Errorf("mode %q not supported", mode.String())
	}

	block = volumeCapability.GetBlock() != nil
	mount = volumeCapability.GetMount() != nil

	// It should be impossible for block and mount to be true at the same time.
	if block && mount {
		return false, false, errors.New("both mount and block volume type specified")
	}

	if !block && !mount {
		return false, false, errors.New("one of block or mount access type is not specified")
	}

	return block, mount, nil
}

// getVolumeRequestCapacity returns the volume capacity that will be requested
// to the Scaleway block storage according to the provided capacity range.
func getVolumeRequestCapacity(capacityRange *csi.CapacityRange) (int64, error) {
	if capacityRange == nil {
		return scaleway.MinVolumeSize, nil
	}

	requiredBytes := capacityRange.GetRequiredBytes()
	requiredBytesSet := requiredBytes > 0

	limitBytes := capacityRange.GetLimitBytes()
	limitBytesSet := limitBytes > 0

	if !requiredBytesSet && !limitBytesSet {
		return scaleway.MinVolumeSize, nil
	}

	if requiredBytesSet && limitBytesSet && limitBytes < requiredBytes {
		return 0, errors.New("limit size is less than required size")
	}

	if requiredBytesSet && !limitBytesSet && requiredBytes < scaleway.MinVolumeSize {
		return 0, errors.New("required size is less than the minimum size")
	}

	if limitBytesSet && limitBytes < scaleway.MinVolumeSize {
		return 0, errors.New("limit size is less than the minimum size")
	}

	if requiredBytesSet && limitBytesSet && requiredBytes == limitBytes {
		return requiredBytes, nil
	}

	if requiredBytesSet {
		return requiredBytes, nil
	}

	if limitBytesSet {
		return limitBytes, nil
	}

	return scaleway.MinVolumeSize, nil
}

// createMountPoint creates a mount point to the specified path. When file is
// set to true, it creates a file mount point (needed to bind a block volume).
func createMountPoint(path string, file bool) error {
	_, err := os.Stat(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("failed to stat mountpoint: %w", err)
	}

	if file {
		if err := os.MkdirAll(filepath.Dir(path), os.FileMode(0755)); err != nil {
			return fmt.Errorf("failed to create dir for mountpoint: %w", err)
		}

		file, err := os.OpenFile(path, os.O_CREATE, os.FileMode(0644))
		if err != nil {
			return fmt.Errorf("failed to create mountpoint file: %w", err)
		}
		defer file.Close()
	} else {
		if err := os.MkdirAll(path, os.FileMode(0755)); err != nil {
			return fmt.Errorf("failed to create mountpoint dir: %w", err)
		}
	}

	return nil
}

// secretsField is the name of the field that contains a map with secrets.
const secretsField = "Secrets"

// stripSecretFromReq returns a CSI request as a string after stripping all secrets.
func stripSecretFromReq(req any) string {
	ret := "{"

	// Try to dereference pointer if needed.
	reqValue := reflect.ValueOf(req)
	if reqValue.Kind() == reflect.Pointer {
		reqValue = reqValue.Elem()
	}

	reqType := reqValue.Type()
	if reqType.Kind() == reflect.Struct {
		for i := 0; i < reqValue.NumField(); i++ {
			field := reqType.Field(i)
			value := reqValue.Field(i)

			// Skip non-exported fields.
			if !field.IsExported() {
				continue
			}

			valueToPrint := fmt.Sprintf("%+v", value.Interface())

			if field.Name == secretsField && value.Kind() == reflect.Map {
				valueToPrint = "["
				for j := 0; j < len(value.MapKeys()); j++ {
					valueToPrint += fmt.Sprintf("%s:<redacted>", value.MapKeys()[j].String())
					if j != len(value.MapKeys())-1 {
						valueToPrint += " "
					}
				}
				valueToPrint += "]"
			}

			ret += fmt.Sprintf("%s:%s", field.Name, valueToPrint)
			if i != reqValue.NumField()-1 {
				ret += " "
			}
		}
	}

	ret += "}"

	return ret
}

// getOrCreateVolume gets a volume by name or creates it if it does not exist.
func (d *controllerService) getOrCreateVolume(ctx context.Context, name, snapshotID string, size int64, perfIOPS *uint32, zones []scw.Zone) (*block.Volume, error) {
	if len(zones) == 0 {
		zones = append(zones, scw.Zone(""))
	}

	scwSize, err := scaleway.NewSize(size)
	if err != nil {
		return nil, fmt.Errorf("size is invalid: %w", err)
	}

	for _, zone := range zones {
		volume, err := d.scaleway.GetVolumeByName(ctx, name, scwSize, zone)
		if err != nil && !errors.Is(err, scaleway.ErrVolumeNotFound) {
			return nil, fmt.Errorf("failed to try to get existing volume %q: %w", name, err)
		}

		if volume != nil {
			// volume exists.
			return volume, nil
		}
	}

	var errs []error
	for _, zone := range zones {
		volume, err := d.scaleway.CreateVolume(ctx, name, snapshotID, size, perfIOPS, zone)
		if err != nil {
			errs = append(errs, err)
			continue
		}

		return volume, nil
	}

	return nil, fmt.Errorf("failed to create volume: %w", errors.Join(errs...))
}

// getOrCreateSnapshot gets a snapshot by name or creates it if it does not exist.
func (d *controllerService) getOrCreateSnapshot(ctx context.Context, name, sourceVolumeID string, zone scw.Zone) (*block.Snapshot, error) {
	snapshot, err := d.scaleway.GetSnapshotByName(ctx, name, sourceVolumeID, zone)
	if err != nil && !errors.Is(err, scaleway.ErrSnapshotNotFound) {
		return nil, fmt.Errorf("failed to try to get existing snapshot: %w", err)
	}

	if snapshot == nil {
		// Create snapshot if it does not exist.
		snapshot, err = d.scaleway.CreateSnapshot(ctx, name, sourceVolumeID, zone)
		if err != nil {
			return nil, fmt.Errorf("failed to create snapshot: %w", err)
		}
	}

	// Wait for snapshot to be cut.
	snapshot, err = d.scaleway.WaitForSnapshot(ctx, snapshot.ID, zone)
	if err != nil {
		return nil, fmt.Errorf("wait for snapshot ended with an error: %w", err)
	}

	return snapshot, nil
}

// parseCreateVolumeParams parses the params sent by the client during the
// creation of a volume. It returns the requested number of IOPS if specified.
// The second return value is true if the volume should be encrypted.
func parseCreateVolumeParams(params map[string]string) (*uint32, bool, error) {
	var (
		encrypted  bool
		perfIOPS   *uint32
		volumeType string
	)

	for key, value := range params {
		switch strings.ToLower(key) {
		case volumeTypeKey:
			if value != scaleway.LegacyDefaultVolumeType {
				return nil, false, fmt.Errorf("invalid value (%s) for parameter %s: unknown volume type", value, key)
			}

			volumeType = value
		case encryptedKey:
			encryptedValue, err := strconv.ParseBool(value)
			if err != nil {
				return nil, false, fmt.Errorf("invalid bool value (%s) for parameter %s: %s", value, key, err)
			}
			encrypted = encryptedValue

		case volumeIOPSKey:
			iops, err := strconv.ParseUint(value, 10, 0)
			if err != nil {
				return nil, false, fmt.Errorf("invalid value (%s) for parameter %s: %s", value, key, err)
			}

			perfIOPS = scw.Uint32Ptr(uint32(iops))
		default:
			return nil, false, fmt.Errorf("invalid parameter key %s", key)
		}
	}

	// Params are invalid if LegacyDefaultVolumeType is set but number of IOPS is
	// different than what is supported.
	if volumeType == scaleway.LegacyDefaultVolumeType && perfIOPS != nil &&
		*perfIOPS != scaleway.LegacyDefaultVolumeTypeIOPS {
		return nil, false, fmt.Errorf("volume type %s only supports %d iops",
			scaleway.LegacyDefaultVolumeType, scaleway.LegacyDefaultVolumeTypeIOPS)
	}

	return perfIOPS, encrypted, nil
}

// csiVolume returns a CSI Volume from a Scaleway Volume spec.
func csiVolume(volume *block.Volume) *csi.Volume {
	var contentSource *csi.VolumeContentSource
	if volume.ParentSnapshotID != nil {
		contentSource = &csi.VolumeContentSource{
			Type: &csi.VolumeContentSource_Snapshot{
				Snapshot: &csi.VolumeContentSource_SnapshotSource{
					SnapshotId: expandZonalID(*volume.ParentSnapshotID, volume.Zone),
				},
			},
		}
	}

	return &csi.Volume{
		VolumeId:      expandZonalID(volume.ID, volume.Zone),
		CapacityBytes: scwSizetoInt64(volume.Size),
		AccessibleTopology: []*csi.Topology{
			{
				Segments: map[string]string{ZoneTopologyKey: volume.Zone.String()},
			},
		},
		ContentSource: contentSource,
	}
}

// publishedNodeIDs returns the ID of the node that the volume is attached to.
// There will be either one or zero ID in the returned slice as the volume can
// be attached to at most one server.
func publishedNodeIDs(volume *block.Volume) []string {
	ids := make([]string, 0, len(volume.References))

	for _, v := range volume.References {
		if v.ProductResourceType == scaleway.InstanceServerProductResourceType {
			ids = append(ids, expandZonalID(v.ProductResourceID, volume.Zone))
			break
		}
	}

	return ids
}

// csiSnapshot returns a CSI Snapshot from a Snapshot.
func csiSnapshot(snapshot *block.Snapshot) *csi.Snapshot {
	snap := &csi.Snapshot{
		SizeBytes:  scwSizetoInt64(snapshot.Size),
		SnapshotId: expandZonalID(snapshot.ID, snapshot.Zone),
		ReadyToUse: snapshot.Status == block.SnapshotStatusAvailable,
	}

	if snapshot.ParentVolume != nil {
		snap.SourceVolumeId = expandZonalID(snapshot.ParentVolume.ID, snapshot.Zone)
	}

	if snapshot.CreatedAt != nil {
		snap.CreationTime = timestamppb.New(*snapshot.CreatedAt)
	}

	return snap
}

// parseStartingToken parses a numeric starting token.
func parseStartingToken(token string) (uint32, error) {
	if token == "" {
		return 0, nil
	}

	start, err := strconv.ParseUint(token, 10, 0)
	if err != nil {
		return 0, fmt.Errorf("failed to parse token into a number: %w", err)
	}

	return uint32(start), nil
}

// isVolumeEncrypted returns true if the volume context specifies that the volume
// should be encrypted.
func isVolumeEncrypted(volumeContext map[string]string) (bool, error) {
	encrypted := false
	if encryptedValueString, ok := volumeContext[encryptedKey]; ok {
		encryptedValue, err := strconv.ParseBool(encryptedValueString)
		if err != nil {
			return false, fmt.Errorf("failed to check if volume is encrypted from volume context: %w", err)
		}

		encrypted = encryptedValue
	}

	return encrypted, nil
}

// codeFromError takes an error and returns the most appropriate GRPC error code
// according to the CSI spec.
func codeFromScalewayError(err error) codes.Code {
	switch {
	case errors.Is(err, scaleway.ErrVolumeDifferentSize), errors.Is(err, scaleway.ErrSnapshotExists):
		return codes.AlreadyExists
	case scaleway.IsNotFoundError(err), scaleway.IsGoneError(err):
		return codes.NotFound
	case scaleway.IsPreconditionFailedError(err):
		// Most likely when trying to delete a volume that is already attached.
		return codes.FailedPrecondition
	default:
		return codes.Internal
	}
}

// scwSizetoInt64 converts an scw.Size to int64. It panics if the size exceeds math.MaxInt64.
func scwSizetoInt64(s scw.Size) int64 {
	return uint64ToInt64(uint64(s))
}

// uint64ToInt64 converts an uint64 value to an int64 value. It panics if the value
// exceeds math.MaxInt64.
func uint64ToInt64(v uint64) int64 {
	// This should never happen in practice.
	if v > math.MaxInt64 {
		panic("value exceeds max int64")
	}

	return int64(v)
}

// attachedScratchVolumes returns the number of attached scratch volumes, based
// on the instance metadata.
func attachedScratchVolumes(md *instance.Metadata) int {
	var count int

	for _, vol := range md.Volumes {
		if vol.VolumeType == "scratch" {
			count++
		}
	}

	return count
}

// maxVolumesPerNode returns the maximum number of volumes that can be attached to a node,
// after substracting the system root volume and the provided number of reserved volumes.
// It returns an error if the result is 0 or less.
func maxVolumesPerNode(reservedCount int) (int64, error) {
	max := scaleway.MaxVolumesPerNode - reservedCount - 1
	if max <= 0 {
		return 0, fmt.Errorf("max number of volumes that can be attached to this node must be at least 1, currently is %d", max)
	}

	return int64(max), nil
}
