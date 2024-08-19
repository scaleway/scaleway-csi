package main

import (
	"context"
	"flag"

	"github.com/scaleway/scaleway-csi/pkg/driver"
	"github.com/scaleway/scaleway-csi/pkg/migration"
	"github.com/scaleway/scaleway-csi/pkg/scaleway"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog/v2"
)

func main() {
	var (
		ctx = context.Background()

		// Flags
		kubeconfig               = flag.String("kubeconfig", "", "absolute path to the kubeconfig file")
		disableVolumeMigration   = flag.Bool("disable-volume-migration", false, "Disables listing volumes and migrating them")
		disableSnapshotMigration = flag.Bool("disable-snapshot-migration", false, "Disables listing snapshots and migrating them")
		dryRun                   = flag.Bool("dry-run", false, "Simulates the volume and snapshot migration process")
	)
	flag.Parse()

	// Create Kubernetes client.
	config, err := clientcmd.BuildConfigFromFlags("", *kubeconfig)
	if err != nil {
		klog.Fatal(err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		klog.Fatal(err)
	}

	dynClient, err := dynamic.NewForConfig(config)
	if err != nil {
		klog.Fatal(err)
	}

	// Create Scaleway client.
	scw, err := scaleway.New(driver.UserAgent())
	if err != nil {
		klog.Fatal(err)
	}

	// Migrate volumes and snapshots from Instance to Block API.
	opts := &migration.Options{
		DryRun:                   *dryRun,
		DisableVolumeMigration:   *disableVolumeMigration,
		DisableSnapshotMigration: *disableSnapshotMigration,
	}

	if err := migration.New(clientset, dynClient, scw, opts).Do(ctx); err != nil {
		klog.Fatal(err)
	}
}
