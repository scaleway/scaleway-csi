package scaleway

import "github.com/scaleway/scaleway-sdk-go/api/instance/v1"

// ExpandSnapshotID concatenates the zone with the snapshot ID
func ExpandSnapshotID(snapshot *instance.Snapshot) string {
	return snapshot.Zone.String() + "/" + snapshot.ID
}

// ExpandVolumeID concatenates the zone with the volume ID
func ExpandVolumeID(volume *instance.Volume) string {
	return volume.Zone.String() + "/" + volume.ID
}

// ExpandServerID concatenates the zone with the server ID
func ExpandServerID(server *instance.Server) string {
	return server.Zone.String() + "/" + server.ID
}
