package scaleway

import (
	"errors"
	"fmt"
	"math"
	"strconv"

	"github.com/google/uuid"
	"github.com/scaleway/scaleway-sdk-go/scw"
)

// maxPageSize for paginated lists.
const maxPageSize = 50

// paginatedList allows to list resources with a start and max pagination options.
func paginatedList[T any](query func(page int32, pageSize uint32) ([]T, error), start, max uint32) (elements []T, next string, err error) {
	// Reduce page size if needed.
	listPageSize := uint32(maxPageSize)
	if max < listPageSize && max != 0 {
		listPageSize = max
	}

	var (
		// first page to query (must start at 1).
		page = int32(math.Floor(float64(start)/float64(listPageSize))) + 1
		// first element index.
		first = int(start % listPageSize)
	)

	for {
		var resp []T
		resp, err = query(page, listPageSize)
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
				if respCount == int(listPageSize) {
					next = strconv.Itoa(int(start + max))
				}

				return
			}
		}

		// less results than page size.
		if respCount != int(listPageSize) {
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
// TODO: be able to handle multiple regions.
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

// isValidUUID returns true if the provided value is a valid UUID.
func isValidUUID(u string) bool {
	_, err := uuid.Parse(u)
	return err == nil
}

// NewSize creates a new scw.Size from an int64 value.
func NewSize(size int64) (scw.Size, error) {
	if size < 0 {
		return 0, errors.New("size cannot be a negative number")
	}
	return scw.Size(size), nil
}
