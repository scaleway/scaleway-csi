// Package migration handles the migration of volumes and snapshots from the
// Instance API to the new Block API.
package migration

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/avast/retry-go/v4"
	"github.com/scaleway/scaleway-csi/pkg/driver"
	"github.com/scaleway/scaleway-csi/pkg/scaleway"
	"github.com/scaleway/scaleway-sdk-go/api/instance/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
)

// retryOpts are the default options for retrying requests to Scaleway API.
var retryOpts = []retry.Option{
	retry.RetryIf(scaleway.IsTooManyRequestsError),
	retry.Delay(1 * time.Second),
}

// The Migration struct holds a Kubernetes and Scaleway client.
type Migration struct {
	k8s    kubernetes.Interface
	scw    *scaleway.Scaleway
	dryRun bool
}

// New returns a new instance of Migration with the specified k8s/scw clients.
func New(k8s kubernetes.Interface, scw *scaleway.Scaleway, dryRun bool) *Migration {
	return &Migration{k8s, scw, dryRun}
}

// listHandles lists handles of PersistentVolumes managed by the Scaleway CSI driver.
func (m *Migration) listHandles(ctx context.Context) ([]string, error) {
	volumes, err := m.k8s.CoreV1().PersistentVolumes().List(ctx, v1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list k8s PersistentVolumes: %w", err)
	}

	var handles []string

	for _, vol := range volumes.Items {
		if vol.Spec.CSI != nil && vol.Spec.CSI.Driver == driver.DriverName {
			handles = append(handles, vol.Spec.CSI.VolumeHandle)
		}
	}

	return handles, nil
}

// migrateHandle migrates a Scaleway volume using the provided handle. If the
// handle is invalid, it is skipped. If the volume does not exist in the Instance
// API, we assume it was already migrated. The first return value is true if the
// volume was effectively migrated.
func (m *Migration) migrateHandle(ctx context.Context, handle string) (bool, error) {
	id, zone, err := driver.ExtractIDAndZone(handle)
	if err != nil {
		// Skip migration if handle is not valid.
		klog.Warningf("Failed to extract ID and Zone from handle %q, it will not be migrated: %s", handle, err)
		return false, nil
	}

	if _, err = retry.DoWithData(func() (*instance.Volume, error) {
		return m.scw.GetLegacyVolume(ctx, id, zone) //nolint:wrapcheck
	}, retryOpts...); err != nil {
		// If legacy volume does not exist, we assume it was already migrated.
		if scaleway.IsNotFoundError(err) {
			return false, nil
		}

		return false, fmt.Errorf("failed to get legacy volume: %w", err)
	}

	if err := retry.Do(func() error {
		return m.scw.MigrateLegacyVolume(ctx, id, zone, m.dryRun) //nolint:wrapcheck
	}, retryOpts...); err != nil {
		return false, fmt.Errorf("could not migrate volume with handle %q: %w", handle, err)
	}

	return true, nil
}

// isManagedCluster returns true if a Scaleway managed node is found in the k8s cluster.
func (m *Migration) isManagedCluster(ctx context.Context) (bool, error) {
	nodes, err := m.k8s.CoreV1().Nodes().List(ctx, v1.ListOptions{})
	if err != nil {
		return false, fmt.Errorf("failed to list nodes: %w", err)
	}

	for _, node := range nodes.Items {
		if _, ok := node.Labels["k8s.scaleway.com/managed"]; ok {
			return true, nil
		}
	}

	return false, nil
}

// Do runs the migration of all Scaleway PersistentVolumes from the Instance API
// to the new Block API.
func (m *Migration) Do(ctx context.Context) error {
	if m.dryRun {
		klog.Infof("Dry run enabled, volumes and snapshots will not be migrated")
	}

	// Quick check to make sure this tool is not run on a managed cluster.
	if os.Getenv("IGNORE_MANAGED_CLUSTER") == "" {
		managed, err := m.isManagedCluster(ctx)
		if err != nil {
			return fmt.Errorf("failed to check if cluster is managed: %w", err)
		}

		if managed {
			return errors.New(
				"this tool does not supported managed clusters (e.g. Kapsule / Kosmos). " +
					"If this is a false alert, you can bypass this verification by setting this " +
					"environment variable: IGNORE_MANAGED_CLUSTER=true",
			)
		}
	}

	handles, err := m.listHandles(ctx)
	if err != nil {
		return fmt.Errorf("could not list handles: %w", err)
	}

	klog.Infof("Found %d Scaleway PersistentVolumes in the cluster", len(handles))

	for _, handle := range handles {
		klog.Infof("Migrating volume with handle %s", handle)

		ok, err := m.migrateHandle(ctx, handle)
		if err != nil {
			return fmt.Errorf("failed to migrate handle %s: %w", handle, err)
		}

		if ok {
			klog.Infof("Volume with handle %s was successfully migrated", handle)
		} else {
			klog.Infof("Volume with handle %s was not migrated", handle)
		}
	}

	return nil
}
