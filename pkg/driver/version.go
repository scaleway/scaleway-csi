package driver

import (
	"fmt"
	"runtime"
)

// taken from https://github.com/kubernetes-sigs/aws-ebs-csi-driver/blob/db95482f8963350d70e9932b0936e6794fe76bf2/pkg/driver/version.go

// These are set during build time via -ldflags
var (
	driverVersion string
	gitCommit     string
	buildDate     string
)

// VersionInfo represents the current running version
type VersionInfo struct {
	DriverVersion string `json:"driverVersion"`
	GitCommit     string `json:"gitCommit"`
	BuildDate     string `json:"buildDate"`
	GoVersion     string `json:"goVersion"`
	Compiler      string `json:"compiler"`
	Platform      string `json:"platform"`
}

// GetVersion returns the current running version
func GetVersion() VersionInfo {
	return VersionInfo{
		DriverVersion: driverVersion,
		GitCommit:     gitCommit,
		BuildDate:     buildDate,
		GoVersion:     runtime.Version(),
		Compiler:      runtime.Compiler,
		Platform:      fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH),
	}
}
