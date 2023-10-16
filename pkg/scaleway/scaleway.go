package scaleway

import (
	"fmt"

	block "github.com/scaleway/scaleway-sdk-go/api/block/v1alpha1"
	"github.com/scaleway/scaleway-sdk-go/api/instance/v1"
	"github.com/scaleway/scaleway-sdk-go/scw"
)

const (
	// MaxVolumesPerNode represents the number max of volumes attached to one node.
	MaxVolumesPerNode = 16

	// LegacyDefaultVolumeType is the legacy default type for Scaleway Block volumes
	// that use Instance API. We keep it for backward compatibility. It is equivalent
	// to the new 5K IOPS volumes.
	LegacyDefaultVolumeType = "b_ssd"

	// LegacyDefaultVolumeTypeIOPS is the number of IOPS for the LegacyDefaultVolumeType.
	LegacyDefaultVolumeTypeIOPS = 5000

	// MinVolumeSize is the minimum size of a volume.
	MinVolumeSize = 1000000000 //  1 GB.
)

const (
	volumeResource   = "volume"
	serverResource   = "server"
	snapshotResource = "snapshot"
)

// Scaleway is the struct used to communicate with the Scaleway provider.
type Scaleway struct {
	// scaleway client.
	client *scw.Client
	// zones of the region where the client is configured.
	zones []scw.Zone

	// instance API.
	instance *instance.API

	// block API.
	block *block.API
}

// Enforce interface.
var _ Interface = &Scaleway{}

// New returns a new Scaleway object which will use the given user agent.
func New(userAgent string) (*Scaleway, error) {
	client, err := scw.NewClient(
		scw.WithEnv(),
		scw.WithUserAgent(userAgent),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create Scaleway client: %w", err)
	}

	zones, err := clientZones(client)
	if err != nil {
		return nil, err
	}

	return &Scaleway{
		client:   client,
		zones:    zones,
		instance: instance.NewAPI(client),
		block:    block.NewAPI(client),
	}, nil
}
