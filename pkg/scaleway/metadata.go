package scaleway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/scaleway/scaleway-sdk-go/api/instance/v1"
	"github.com/scaleway/scaleway-sdk-go/scw"
	"k8s.io/klog/v2"
)

const (
	metadataAPIURL         = "http://169.254.42.42/conf?format=json"
	metadataRequestTimeout = 10 * time.Second
	cloudInitDataFile      = "/run/cloud-init/instance-data.json"
)

var metadataSources = []metadataSource{cloudInitMetadataSource{}, apiMetadataSource{}}

// GetMetadata gets metadata of the instance that runs this function. It tries
// each metadata source successively (cloud-init, api) until one responds successfully.
func GetMetadata() (*instance.Metadata, error) {
	for _, src := range metadataSources {
		md, err := src.Get()
		if err != nil {
			klog.Errorf("failed to get Metadata from source %T: %s", src, err)
			continue
		}

		return md, nil
	}

	return nil, errors.New("no metadata source responded successfully")
}

// metadataSource is a generic Instance metadata source.
type metadataSource interface {
	Get() (*instance.Metadata, error)
}

// apiMetadataSource retrieves Instance metadata from the Instance metadata API.
type apiMetadataSource struct{}

func (apiMetadataSource) Get() (m *instance.Metadata, err error) {
	ctx, cancel := context.WithTimeout(context.TODO(), metadataRequestTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, metadataAPIURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create metadata request")
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to get metadata from API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("metadata API did not return 200: got %d", resp.StatusCode)
	}

	metadata := &instance.Metadata{}
	if err = json.NewDecoder(resp.Body).Decode(metadata); err != nil {
		return nil, fmt.Errorf("error decoding metadata: %w", err)
	}

	return metadata, nil
}

// apiMetadataSource retrieves Instance metadata from the cloud-init metadata.
type cloudInitMetadataSource struct{}

func (cloudInitMetadataSource) Get() (*instance.Metadata, error) {
	f, err := os.Open(cloudInitDataFile)
	if err != nil {
		return nil, fmt.Errorf("failed to open cloud-init data file: %w", err)
	}
	defer f.Close()

	data := cloudInitInstanceData{}
	if err = json.NewDecoder(f).Decode(&data); err != nil {
		return nil, fmt.Errorf("error decoding cloud-init data file: %w", err)
	}

	// Validate data.
	if data.DS.Metadata == nil {
		return nil, errors.New("missing metadata in cloud-init data file")
	}

	if _, err = scw.ParseZone(data.DS.Metadata.Location.ZoneID); err != nil {
		return nil, fmt.Errorf("zone is not valid in .location.zone_id: %w", err)
	}

	return data.DS.Metadata, nil
}

type cloudInitInstanceData struct {
	DS struct {
		Metadata *instance.Metadata `json:"meta_data,omitempty"`
	} `json:"ds"`
}
