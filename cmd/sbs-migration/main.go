package main

import (
	"context"
	"flag"
	"path/filepath"

	"github.com/scaleway/scaleway-csi/pkg/driver"
	"github.com/scaleway/scaleway-csi/pkg/migration"
	"github.com/scaleway/scaleway-csi/pkg/scaleway"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"
	"k8s.io/klog/v2"
)

func main() {
	ctx := context.Background()

	var kubeconfig *string
	if home := homedir.HomeDir(); home != "" {
		kubeconfig = flag.String("kubeconfig", filepath.Join(home, ".kube", "config"), "(optional) absolute path to the kubeconfig file")
	} else {
		kubeconfig = flag.String("kubeconfig", "", "absolute path to the kubeconfig file")
	}
	dryRun := flag.Bool("dry-run", false, "When set to true, volumes and snapshots will not be migrated")
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

	// Create Scaleway client.
	scw, err := scaleway.New(driver.UserAgent())
	if err != nil {
		klog.Fatal(err)
	}

	// Migrate volumes and snapshots from Instance to Block API.
	if err := migration.New(clientset, scw, *dryRun).Do(ctx); err != nil {
		klog.Fatal(err)
	}
}
