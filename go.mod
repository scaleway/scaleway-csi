module github.com/scaleway/scaleway-csi

go 1.14

require (
	github.com/container-storage-interface/spec v1.2.0
	github.com/docker/docker v1.13.1
	github.com/golang/protobuf v1.3.2
	github.com/google/uuid v1.1.1
	github.com/kubernetes-csi/csi-test/v3 v3.1.0
	github.com/scaleway/scaleway-sdk-go v1.0.0-beta.6.0.20200526134120-bbbe8eca40a5
	golang.org/x/sys v0.0.0-20191113165036-4c7a9d0fe056
	google.golang.org/grpc v1.25.1
	k8s.io/klog v1.0.0
	k8s.io/utils v0.0.0-20190607212802-c55fbcfc754a
)
