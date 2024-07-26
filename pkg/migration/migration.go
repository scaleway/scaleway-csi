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
	"golang.org/x/sync/errgroup"
	kerror "k8s.io/apimachinery/pkg/api/errors"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
)

const (
	maxParallelMigrations = 3
	retryDelay            = 1 * time.Second
)

var (
	volSnapContentRes = schema.GroupVersionResource{Group: "snapshot.storage.k8s.io", Version: "v1", Resource: "volumesnapshotcontents"}

	// retryOpts are the default options for retrying requests to Scaleway API.
	retryOpts = []retry.Option{
		retry.RetryIf(isRetryable),
		retry.Delay(retryDelay),
	}
)

type Options struct {
	DryRun                   bool
	DisableVolumeMigration   bool
	DisableSnapshotMigration bool
}

// The Migration struct holds a Kubernetes and Scaleway client.
type Migration struct {
	k8s    kubernetes.Interface
	k8sDyn *dynamic.DynamicClient
	scw    *scaleway.Scaleway
	opts   *Options
}

// New returns a new instance of Migration with the specified k8s/scw clients.
func New(k8s kubernetes.Interface, k8sDyn *dynamic.DynamicClient, scw *scaleway.Scaleway, opts *Options) *Migration {
	return &Migration{k8s, k8sDyn, scw, opts}
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

// listSnapshotHandles lists handles of VolumentSnapshotContents managed by the Scaleway CSI driver.
func (m *Migration) listSnapshotHandles(ctx context.Context) ([]string, error) {
	volsnapcontents, err := m.k8sDyn.Resource(volSnapContentRes).List(ctx, v1.ListOptions{})
	if err != nil {
		if kerror.IsNotFound(err) {
			klog.Warningf("Could not list VolumeSnapshotContents, CRD is probably missing: %s", err)
			return nil, nil
		}

		return nil, fmt.Errorf("failed to list k8s VolumeSnapshotContents: %w", err)
	}

	handles := make([]string, 0, len(volsnapcontents.Items))

	for _, v := range volsnapcontents.Items {
		d, ok, err := unstructured.NestedString(v.Object, "spec", "driver")
		if err != nil {
			return nil, fmt.Errorf("failed to get driver for %s: %w", v.GetName(), err)
		}
		// Skip snapshots not managed by this driver.
		if !ok || d != driver.DriverName {
			continue
		}

		h, ok, err := unstructured.NestedString(v.Object, "status", "snapshotHandle")
		if err != nil {
			return nil, fmt.Errorf("failed to get snapshotHandle for %s: %w", v.GetName(), err)
		}
		// Skip snapshots with missing handle.
		if !ok {
			continue
		}

		handles = append(handles, h)
	}

	return handles, nil
}

type migrateKind string

const (
	volumeMigrateKind   migrateKind = "volume"
	snapshotMigrateKind migrateKind = "snapshot"
)

// migrateHandle migrates a Scaleway volume or snapshot using the provided handle.
// If the handle is invalid, it is skipped. If the volume or snapshot does not exist
// in the Instance API, we assume it was already migrated. The first return value is
// true if the volume or snapshot was effectively migrated.
func (m *Migration) migrateHandle(ctx context.Context, kind migrateKind, handle string) (bool, error) {
	id, zone, err := driver.ExtractIDAndZone(handle)
	if err != nil {
		// Skip migration if handle is not valid.
		klog.Warningf("Failed to extract ID and Zone from handle %q, it will not be migrated: %s", handle, err)
		return false, nil
	}

	if err = retry.Do(func() (err error) {
		switch kind {
		case volumeMigrateKind:
			_, err = m.scw.GetLegacyVolume(ctx, id, zone)
		case snapshotMigrateKind:
			_, err = m.scw.GetLegacySnapshot(ctx, id, zone)
		default:
			panic(fmt.Sprintf("unknown kind: %s", kind))
		}
		return
	}, retryOpts...); err != nil {
		// If legacy resource does not exist, we assume it was already migrated.
		if scaleway.IsNotFoundError(err) {
			return false, nil
		}

		return false, fmt.Errorf("failed to get legacy %s: %w", kind, err)
	}

	if err := retry.Do(func() error {
		switch kind {
		case volumeMigrateKind:
			return m.scw.MigrateLegacyVolume(ctx, id, zone, m.opts.DryRun) //nolint:wrapcheck
		case snapshotMigrateKind:
			return m.scw.MigrateLegacySnapshot(ctx, id, zone, m.opts.DryRun) //nolint:wrapcheck
		default:
			panic(fmt.Sprintf("unknown kind: %s", kind))
		}
	}, retryOpts...); err != nil {
		return false, fmt.Errorf("could not migrate %s with handle %q: %w", kind, handle, err)
	}

	return true, nil
}

func (m *Migration) migrate(ctx context.Context, kind migrateKind) error {
	// List handles.
	var (
		handles []string
		err     error
	)
	switch kind {
	case snapshotMigrateKind:
		handles, err = m.listSnapshotHandles(ctx)
	case volumeMigrateKind:
		handles, err = m.listHandles(ctx)
	default:
		panic(fmt.Sprintf("unknown kind: %s", kind))
	}
	if err != nil {
		return fmt.Errorf("could not list handles: %w", err)
	}

	switch kind {
	case snapshotMigrateKind:
		klog.Infof("Found %d Scaleway VolumeSnapshotContents in the cluster", len(handles))
	case volumeMigrateKind:
		klog.Infof("Found %d Scaleway PersistentVolumes in the cluster", len(handles))
	default:
		panic(fmt.Sprintf("unknown kind: %s", kind))
	}

	// Run handle migrations in parallel.
	eg, ctx := errgroup.WithContext(ctx)
	eg.SetLimit(maxParallelMigrations)

	for _, handle := range handles {
		select {
		case <-ctx.Done():
		default:
			eg.Go(func() error {
				klog.Infof("Migrating %s with handle %s", kind, handle)

				ok, err := m.migrateHandle(ctx, kind, handle)
				if err != nil {
					return fmt.Errorf("failed to migrate handle %s: %w", handle, err)
				}

				if ok {
					klog.Infof("%s with handle %s was successfully migrated", kind, handle)
				} else {
					klog.Infof("%s with handle %s was not migrated", kind, handle)
				}

				return nil
			})
		}
	}

	if err := eg.Wait(); err != nil {
		return fmt.Errorf("%s migration failed: %w", kind, err)
	}

	return nil
}

// Do runs the migration of all Scaleway PersistentVolumes and VolumeSnapshotContents
// from the Instance API to the new Block API.
func (m *Migration) Do(ctx context.Context) error {
	// Quick check to make sure this tool is not run on a managed cluster.
	if os.Getenv("IGNORE_MANAGED_CLUSTER") != "true" {
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

	// Migrate volumes first.
	if !m.opts.DisableVolumeMigration {
		if m.opts.DryRun {
			klog.Infof("Dry run enabled, volumes and their associated snapshots will not be migrated")
		}

		if err := m.migrate(ctx, volumeMigrateKind); err != nil {
			return err
		}
	}

	// Migrate snapshots.
	if !m.opts.DisableSnapshotMigration {
		if m.opts.DryRun {
			klog.Infof("Dry run enabled, snapshots and their associated volumes will not be migrated")
		}
		if err := m.migrate(ctx, snapshotMigrateKind); err != nil {
			return err
		}
	}

	return nil
}

func isRetryable(err error) bool {
	return scaleway.IsTooManyRequestsError(err) || scaleway.IsInternalServerError(err)
}
