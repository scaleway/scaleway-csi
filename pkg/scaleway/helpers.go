package scaleway

import (
	"fmt"
	"math"
	"strconv"

	"github.com/google/uuid"
	block "github.com/scaleway/scaleway-sdk-go/api/block/v1alpha1"
	"github.com/scaleway/scaleway-sdk-go/scw"
)

// maxPageSize for paginated lists.
const maxPageSize = 50

// paginatedList allows to list resources with a start and max pagination options.
func paginatedList[T any](query func(page int32, pageSize uint32) ([]T, error), start, max uint32) (elements []T, next string, err error) {
	// Reduce page size if needed.
	listPageSize := maxPageSize
	if int(max) < listPageSize && max != 0 {
		listPageSize = int(max)
	}

	var (
		// first page to query (must start at 1).
		page = int32(math.Floor(float64(start)/float64(listPageSize))) + 1
		// first element index.
		first = int(start % uint32(listPageSize))
	)

	for {
		var resp []T
		resp, err = query(page, uint32(listPageSize))
		if err != nil {
			return nil, "", err
		}

		respCount := len(resp)

		if first >= respCount {
			return
		}

		if max == 0 {
			elements = append(elements, resp[first:]...)
		} else {
			elements = append(elements, resp[first:min(respCount, first+int(max)-len(elements))]...)

			// reached max elements.
			if len(elements) >= int(max) {
				if respCount == listPageSize {
					next = strconv.Itoa(int(start + max))
				}

				return
			}
		}

		// less results than page size.
		if respCount != listPageSize {
			return
		}

		first = 0
		page++
	}
}

// min returns the smaller of a or b.
func min(a, b int) int {
	return int(math.Min(float64(a), float64(b)))
}

// clientZones returns the zones of the region where the client is configured.
func clientZones(client *scw.Client) ([]scw.Zone, error) {
	if defaultZone, ok := client.GetDefaultZone(); ok {
		region, err := defaultZone.Region()
		if err != nil {
			return nil, fmt.Errorf("failed to parse region from default zone: %w", err)
		}

		if len(region.GetZones()) > 0 {
			return region.GetZones(), nil
		}

		return []scw.Zone{defaultZone}, nil
	}

	if region, ok := client.GetDefaultRegion(); ok {
		if len(region.GetZones()) > 0 {
			return region.GetZones(), nil
		}
	}

	return nil, fmt.Errorf("no zone/region was provided, please set the SCW_DEFAULT_ZONE environment variable")
}

// snapshotToSnapshotSummary converts a Snapshot to a SnapshotSummary.
func snapshotToSnapshotSummary(snapshot *block.Snapshot) *block.SnapshotSummary {
	return &block.SnapshotSummary{
		ID:           snapshot.ID,
		Name:         snapshot.Name,
		ParentVolume: snapshot.ParentVolume,
		Size:         snapshot.Size,
		ProjectID:    snapshot.ProjectID,
		CreatedAt:    snapshot.CreatedAt,
		UpdatedAt:    snapshot.UpdatedAt,
		Status:       snapshot.Status,
		Tags:         snapshot.Tags,
		Zone:         snapshot.Zone,
		Class:        snapshot.Class,
	}
}

// isValidUUID returns true if the provided value is a valid UUID.
func isValidUUID(u string) bool {
	_, err := uuid.Parse(u)
	return err == nil
}
