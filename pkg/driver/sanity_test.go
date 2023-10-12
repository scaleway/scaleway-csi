package driver

import (
	"fmt"
	"os"
	"testing"

	"github.com/kubernetes-csi/csi-test/v5/pkg/sanity"
	"github.com/scaleway/scaleway-csi/pkg/scaleway"
	"github.com/scaleway/scaleway-sdk-go/api/instance/v1"
	"github.com/scaleway/scaleway-sdk-go/scw"
)

func TestSanityCSI(t *testing.T) {
	zone := scw.ZoneFrPar1
	endpoint := "/tmp/csi-testing.sock"
	server := &instance.Server{
		ID:   "fb094b6a-a732-4d5f-8283-bd6726ff5938",
		Name: "test",
		Volumes: map[string]*instance.VolumeServer{
			"0": {
				ID:         "c3be79a0-aa3f-4189-aac2-ac7f41eda819",
				VolumeType: instance.VolumeServerVolumeTypeBSSD,
			},
		},
		Zone: zone,
	}

	fake := scaleway.NewFake([]*instance.Server{server}, zone)

	driverConfig := &DriverConfig{
		Endpoint: fmt.Sprintf("unix://%s", endpoint),
		Mode:     AllMode,
	}

	driver := &Driver{
		config: driverConfig,
		controllerService: &controllerService{
			scaleway: fake,
			config:   driverConfig,
		},
		nodeService: &nodeService{
			nodeID:    server.ID,
			nodeZone:  zone,
			diskUtils: newFakeDiskUtils(server),
		},
	}

	go driver.Run() //nolint:errcheck // an error here would fail the test anyway since the grpc server would not be started

	config := sanity.NewTestConfig()
	config.Address = endpoint
	config.TestNodeVolumeAttachLimit = true
	config.TestVolumeExpandSize = config.TestVolumeSize * 2
	config.RemoveTargetPath = func(path string) error {
		return os.RemoveAll(path) //nolint: wrapcheck
	}
	config.RemoveStagingPath = func(path string) error {
		return os.RemoveAll(path) //nolint: wrapcheck
	}
	sanity.Test(t, config)
	driver.srv.GracefulStop()
	os.RemoveAll(endpoint)
}
