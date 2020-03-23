package driver

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/signal"
	"path"
	"path/filepath"
	"syscall"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"google.golang.org/grpc"
	"k8s.io/klog"
)

const (
	// DriverName is the official name for the Scaleway CSI plugin
	DriverName      = "csi.scaleway.com"
	zoneTopologyKey = "topology." + DriverName + "/zone"
)

// Mode represents the mode in which the CSI driver started
type Mode string

const (
	// ControllerMode represents the controller mode
	ControllerMode Mode = "controller"
	// NodeMode represents the node mode
	NodeMode Mode = "node"
	// AllMode represents the the controller and the node mode at the same time
	AllMode Mode = "all"
)

// DriverConfig is used to configure a new Driver
type DriverConfig struct {
	Endpoint string
	Prefix   string
	Mode     Mode
}

// Driver implements the interfaces csi.IdentityServer, csi.ControllerServer and csi.NodeServer
type Driver struct {
	controllerService
	nodeService

	config *DriverConfig

	srv *grpc.Server
}

// NewDriver returns a CSI plugin
func NewDriver(config *DriverConfig) (*Driver, error) {
	klog.Infof("Driver: %s Version: %s", DriverName, driverVersion)

	driver := &Driver{
		config: config,
	}

	switch config.Mode {
	case ControllerMode:
		driver.controllerService = newControllerService(config)
	case NodeMode:
		driver.nodeService = newNodeService()
	case AllMode:
		driver.controllerService = newControllerService(config)
		driver.nodeService = newNodeService()
	default:
		return nil, fmt.Errorf("unknown mode for driver: %s", config.Mode)
	}

	return driver, nil
}

// Run starts the CSI plugin on the given endpoint
func (d *Driver) Run() error {
	endpointURL, err := url.Parse(d.config.Endpoint)
	if err != nil {
		return err
	}

	if endpointURL.Scheme != "unix" {
		klog.Errorf("only unix domain sockets are supported, not %s", endpointURL.Scheme)
		return errSchemeNotSupported
	}

	addr := path.Join(endpointURL.Host, filepath.FromSlash(endpointURL.Path))

	klog.Infof("Removing existing socket if existing")
	if err := os.Remove(addr); err != nil && !os.IsNotExist(err) {
		klog.Errorf("error removing existing socket")
		return errRemovingSocket
	}

	dir := filepath.Dir(addr)
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		err = os.MkdirAll(dir, os.ModePerm)
		if err != nil {
			return err
		}
	}

	listener, err := net.Listen(endpointURL.Scheme, addr)
	if err != nil {
		return err
	}

	// log error through a grpc unary interceptor
	logErrorHandler := func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		resp, err := handler(ctx, req)
		if err != nil {
			klog.Errorf("error for %s: %v", info.FullMethod, err)
		}
		return resp, err
	}

	opts := []grpc.ServerOption{
		grpc.UnaryInterceptor(logErrorHandler),
	}

	d.srv = grpc.NewServer(opts...)

	csi.RegisterIdentityServer(d.srv, d)

	switch d.config.Mode {
	case ControllerMode:
		csi.RegisterControllerServer(d.srv, d)
	case NodeMode:
		csi.RegisterNodeServer(d.srv, d)
	case AllMode:
		csi.RegisterControllerServer(d.srv, d)
		csi.RegisterNodeServer(d.srv, d)
	default:
		return fmt.Errorf("unknown mode for driver: %s", d.config.Mode) // should never happen though

	}

	// graceful shutdown
	gracefulStop := make(chan os.Signal, 1)
	signal.Notify(gracefulStop, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-gracefulStop
		d.srv.GracefulStop()
	}()

	klog.Infof("CSI server started on %s", d.config.Endpoint)
	return d.srv.Serve(listener)
}
