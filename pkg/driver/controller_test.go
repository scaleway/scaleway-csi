package driver

import (
	"context"
	"testing"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/scaleway/scaleway-csi/pkg/scaleway"
	"github.com/scaleway/scaleway-sdk-go/api/instance/v1"
	"github.com/scaleway/scaleway-sdk-go/scw"
)

// A volume moved from one node to another must not be detached from the new
// node when the stale attachment to the old node is unpublished.
func TestControllerUnpublishVolumeOtherNode(t *testing.T) {
	ctx := context.Background()
	zone := scw.ZoneFrPar1

	oldNode := &instance.Server{ID: "fb094b6a-a732-4d5f-8283-bd6726ff5938", Zone: zone, Volumes: map[string]*instance.VolumeServer{}}
	newNode := &instance.Server{ID: "0c6e9d33-4b0a-4f39-9d1b-0d1a4b0c1f2e", Zone: zone, Volumes: map[string]*instance.VolumeServer{}}

	fake := scaleway.NewFake([]*instance.Server{oldNode, newNode}, zone)
	d := &controllerService{scaleway: fake, config: &DriverConfig{}}

	vol, err := fake.CreateVolume(ctx, "test", "", 10_000_000_000, nil, zone)
	if err != nil {
		t.Fatalf("CreateVolume: %s", err)
	}

	if err := fake.AttachVolume(ctx, newNode.ID, vol.ID, zone); err != nil {
		t.Fatalf("AttachVolume: %s", err)
	}

	volumeID := expandZonalID(vol.ID, zone)

	if _, err := d.ControllerUnpublishVolume(ctx, &csi.ControllerUnpublishVolumeRequest{
		VolumeId: volumeID,
		NodeId:   expandZonalID(oldNode.ID, zone),
	}); err != nil {
		t.Fatalf("ControllerUnpublishVolume(old node): %s", err)
	}

	got, err := fake.GetVolume(ctx, vol.ID, zone)
	if err != nil {
		t.Fatalf("GetVolume: %s", err)
	}

	if ids := publishedNodeIDs(got); len(ids) != 1 || ids[0] != expandZonalID(newNode.ID, zone) {
		t.Fatalf("volume must stay attached to %s, published to %v", newNode.ID, ids)
	}

	if _, err := d.ControllerUnpublishVolume(ctx, &csi.ControllerUnpublishVolumeRequest{
		VolumeId: volumeID,
		NodeId:   expandZonalID(newNode.ID, zone),
	}); err != nil {
		t.Fatalf("ControllerUnpublishVolume(new node): %s", err)
	}

	got, err = fake.GetVolume(ctx, vol.ID, zone)
	if err != nil {
		t.Fatalf("GetVolume: %s", err)
	}

	if ids := publishedNodeIDs(got); len(ids) != 0 {
		t.Fatalf("volume must be detached, published to %v", ids)
	}
}
