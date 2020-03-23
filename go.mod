module github.com/scaleway/scaleway-csi

go 1.14

require (
	github.com/container-storage-interface/spec v1.2.0
	github.com/docker/docker v1.13.1
	github.com/golang/protobuf v1.3.2
	github.com/google/uuid v1.1.1
	github.com/kubernetes-csi/csi-test v2.2.0+incompatible
	github.com/onsi/ginkgo v1.10.3 // indirect
	github.com/onsi/gomega v1.7.1 // indirect
	github.com/scaleway/scaleway-sdk-go v1.0.0-beta.6
	golang.org/x/sys v0.0.0-20190215142949-d0b11bdaac8a
	google.golang.org/grpc v1.21.1
	k8s.io/klog v1.0.0
	k8s.io/utils v0.0.0-20190607212802-c55fbcfc754a
)
