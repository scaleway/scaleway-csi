package driver

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/url"
	"os"
	"os/signal"
	"path"
	"path/filepath"
	"syscall"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"google.golang.org/grpc"
	"k8s.io/klog/v2"
)

const (
	// DriverName is the official name for the Scaleway CSI plugin.
	DriverName = "csi.scaleway.com"

	// ZoneTopologyKey is the topology key used to provision volumes.
	ZoneTopologyKey = "topology." + DriverName + "/zone"

	// ExtraUserAgentEnv is the environment variable that adds some string at the end of the user agent.
	ExtraUserAgentEnv = "EXTRA_USER_AGENT"
)

// Mode represents the mode in which the CSI driver started.
type Mode string

const (
	// ControllerMode represents the controller mode.
	ControllerMode Mode = "controller"
	// NodeMode represents the node mode.
	NodeMode Mode = "node"
	// AllMode represents the the controller and the node mode at the same time.
	AllMode Mode = "all"
)

// DriverConfig is used to configure a new Driver
type DriverConfig struct {
	// Endpoint is the path to the CSI endpoint.
	Endpoint string
	// Prefix added to the name of newly created volumes.
	Prefix string
	// Plugin mode.
	Mode Mode
}

// Driver implements the interfaces csi.IdentityServer, csi.ControllerServer and csi.NodeServer.
type Driver struct {
	csi.UnimplementedIdentityServer

	// controllerService implements the ControllerServer.
	*controllerService

	// nodeService implements the NodeServer.
	*nodeService

	// User config.
	config *DriverConfig

	// grpc server.
	srv *grpc.Server
}

// mode parses the current mode and returns whether the controller/node services
// should be started.
func mode(mode Mode) (controller bool, node bool, err error) {
	switch mode {
	case ControllerMode:
		controller = true
	case NodeMode:
		node = true
	case AllMode:
		controller = true
		node = true
	default:
		err = fmt.Errorf("unknown mode for driver: %s", mode)
	}

	return
}

// NewDriver returns a CSI plugin.
func NewDriver(config *DriverConfig) (*Driver, error) {
	klog.Infof("Driver: %s Version: %s", DriverName, driverVersion)

	driver := &Driver{
		config: config,
	}

	controller, node, err := mode(config.Mode)
	if err != nil {
		return nil, err
	}

	if controller {
		ctrl, err := newControllerService(config)
		if err != nil {
			return nil, err
		}
		driver.controllerService = ctrl
	}

	if node {
		nodeService, err := newNodeService()
		if err != nil {
			return nil, err
		}

		driver.nodeService = nodeService
	}

	return driver, nil
}

// Run starts the CSI plugin on the given endpoint
func (d *Driver) Run() error {
	endpointURL, err := url.Parse(d.config.Endpoint)
	if err != nil {
		return fmt.Errorf("failed to parse endpoint URL: %w", err)
	}

	if endpointURL.Scheme != "unix" {
		klog.Errorf("only unix domain sockets are supported, not %s", endpointURL.Scheme)
		return errors.New("scheme not supported for endpoint")
	}

	addr := path.Join(endpointURL.Host, filepath.FromSlash(endpointURL.Path))

	klog.Infof("Removing existing socket if existing")
	if err := os.Remove(addr); err != nil && !errors.Is(err, fs.ErrNotExist) {
		klog.Errorf("error removing existing socket")
		return errors.New("error removing existing socket")
	}

	dir := filepath.Dir(addr)
	if _, err := os.Stat(dir); errors.Is(err, fs.ErrNotExist) {
		if err = os.MkdirAll(dir, os.ModePerm); err != nil {
			return fmt.Errorf("failed to create dir for socket: %w", err)
		}
	}

	listener, err := net.Listen(endpointURL.Scheme, addr)
	if err != nil {
		return fmt.Errorf("failed to create listener: %w", err)
	}

	// log error through a grpc unary interceptor
	logErrorHandler := func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		resp, err := handler(ctx, req)
		if err != nil {
			klog.Errorf("error for %s: %v", info.FullMethod, err)
		}
		return resp, err
	}

	d.srv = grpc.NewServer(grpc.UnaryInterceptor(logErrorHandler))

	csi.RegisterIdentityServer(d.srv, d)

	controller, node, err := mode(d.config.Mode)
	if err != nil {
		return err
	}

	if controller {
		csi.RegisterControllerServer(d.srv, d)
	}

	if node {
		csi.RegisterNodeServer(d.srv, d)
	}

	// graceful shutdown
	gracefulStop := make(chan os.Signal, 1)
	signal.Notify(gracefulStop, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-gracefulStop
		d.srv.GracefulStop()
	}()

	klog.Infof("CSI server started on %s", d.config.Endpoint)
	return d.srv.Serve(listener) //nolint: wrapcheck
}
