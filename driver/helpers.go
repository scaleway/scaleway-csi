package driver

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/scaleway/scaleway-csi/scaleway"
	"github.com/scaleway/scaleway-sdk-go/scw"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"k8s.io/klog"
)

func getSnapshotIDAndZone(id string) (string, scw.Zone, error) {
	return extractIDAndZone(id, "snapshotID")
}

func getSourceVolumeIDAndZone(id string) (string, scw.Zone, error) {
	return extractIDAndZone(id, "sourceVolumeID")
}

func getVolumeIDAndZone(id string) (string, scw.Zone, error) {
	return extractIDAndZone(id, "volumeID")
}

func getNodeIDAndZone(id string) (string, scw.Zone, error) {
	return extractIDAndZone(id, "nodeID")
}

func extractIDAndZone(id string, name string) (string, scw.Zone, error) {
	if id == "" {
		return "", scw.Zone(""), status.Errorf(codes.InvalidArgument, "%s is not provided", name)
	}
	splitID := strings.Split(id, "/")
	if len(splitID) > 2 {
		return "", scw.Zone(""), status.Errorf(codes.InvalidArgument, "wrong format for %s", name)
	} else if len(splitID) == 1 {
		return splitID[0], scw.Zone(""), nil
	} else { // id like zone/uuid
		zone, err := scw.ParseZone(splitID[0])
		if err != nil {
			klog.Warningf("wrong zone in %s, will try default zone", name)
			return splitID[1], scw.Zone(""), nil
		}
		return splitID[1], zone, nil
	}
}

func chooseZones(accessibilityRequirements *csi.TopologyRequirement, snapshotZone scw.Zone) ([]scw.Zone, error) {
	if accessibilityRequirements != nil {
		requestedZones := map[string]scw.Zone{}
		for _, req := range accessibilityRequirements.GetRequisite() {
			topologyKeys := req.GetSegments()
			for topologyKey, topologyValue := range topologyKeys {
				switch topologyKey {
				case zoneTopologyKey:
					zone, err := scw.ParseZone(topologyValue)
					if err != nil {
						klog.Warningf("the given value for requisite %s: %s is not a valid zone", zoneTopologyKey, topologyValue)
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
				case zoneTopologyKey:
					zone, err := scw.ParseZone(topologyValue)
					if err != nil {
						klog.Warningf("the given value for preferred %s: %s is not a valid zone", zoneTopologyKey, topologyValue)
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

func validateVolumeCapabilities(volumeCapabilities []*csi.VolumeCapability) error {
	if volumeCapabilities == nil {
		return errVolumeCapabilitiesIsNil
	}

	block := false
	mount := false

	for _, volumeCapability := range volumeCapabilities {
		err := validateVolumeCapability(volumeCapability)
		if err != nil {
			return err
		}
		if volumeCapability.GetBlock() != nil {
			block = true
		}

		if volumeCapability.GetMount() != nil {
			mount = true
		}
	}

	if mount && block {
		return errBothMountBlockVolumes
	}
	return nil
}

func validateVolumeCapability(volumeCapability *csi.VolumeCapability) error {
	if volumeCapability == nil {
		return errVolumeCapabilityIsNil
	}

	for _, accessMode := range supportedAccessModes {
		if accessMode.Mode == volumeCapability.GetAccessMode().GetMode() {
			return nil
		}
	}
	return errAccessModeNotSupported
}

func getVolumeRequestCapacity(capacityRange *csi.CapacityRange) (int64, error) {
	if capacityRange == nil {
		return scaleway.MinimumVolumeSizeInBytes, nil
	}

	requiredBytes := capacityRange.GetRequiredBytes()
	requiredBytesSet := requiredBytes > 0

	limitBytes := capacityRange.GetLimitBytes()
	limitBytesSet := limitBytes > 0

	if !requiredBytesSet && !limitBytesSet {
		return scaleway.MinimumVolumeSizeInBytes, nil
	}

	if requiredBytesSet && limitBytesSet && limitBytes < requiredBytes {
		return 0, errLimitBytesLessThanRequiredBytes
	}

	if requiredBytesSet && !limitBytesSet && requiredBytes < scaleway.MinimumVolumeSizeInBytes {
		return 0, errRequiredBytesLessThanMinimun
	}

	if limitBytesSet && limitBytes < scaleway.MinimumVolumeSizeInBytes {
		return 0, errLimitBytesLessThanMinimum
	}

	if requiredBytesSet && requiredBytes > scaleway.MaximumVolumeSizeInBytes {
		return 0, errRequiredBytesGreaterThanMaximun
	}

	if !requiredBytesSet && limitBytesSet && limitBytes > scaleway.MaximumVolumeSizeInBytes {
		return 0, errLimitBytesGreaterThanMaximum
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

	return scaleway.MinimumVolumeSizeInBytes, nil
}

func newAccessibleTopology(zone scw.Zone) []*csi.Topology {
	return []*csi.Topology{
		{
			Segments: map[string]string{zoneTopologyKey: zone.String()},
		},
	}
}

func createMountPoint(path string, file bool) error {
	_, err := os.Stat(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return err
		}
	}

	if file {
		dir := filepath.Dir(path)
		err := os.MkdirAll(dir, os.FileMode(0755))
		if err != nil {
			return err
		}
		file, err := os.OpenFile(path, os.O_CREATE, os.FileMode(0644))
		defer file.Close()
		if err != nil {
			return err
		}
	} else {
		err := os.MkdirAll(path, os.FileMode(0755))
		if err != nil {
			return err
		}
	}
	return nil
}
