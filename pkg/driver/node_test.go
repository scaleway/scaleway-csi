package driver

import (
	"context"
	"testing"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/scaleway/scaleway-sdk-go/api/instance/v1"
	"github.com/scaleway/scaleway-sdk-go/scw"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func Test_NodeStageVolume(t *testing.T) {
	zone := scw.ZoneFrPar1
	server := &instance.Server{
		ID:   "fb094b6a-a732-4d5f-8283-bd6726ff5938",
		Name: "test",
		Volumes: map[string]*instance.VolumeServer{
			"0": {
				ID:         "c3be79a0-aa3f-4189-aac2-ac7f41eda819",
				VolumeType: instance.VolumeServerVolumeTypeBSSD,
			},
		},
		Zone: zone,
	}

	tests := []struct {
		name        string
		req         *csi.NodeStageVolumeRequest
		wantErrCode codes.Code
	}{
		{
			name: "For non existing disk with volume id should return not found grpc code",
			req: &csi.NodeStageVolumeRequest{
				VolumeId: "fr-par/7d9dc6b1-1750-4520-a483-76cadd5636ef",
				PublishContext: map[string]string{
					scwVolumeNameKey: "external",
					scwVolumeIDKey:   "30cac9cd-5204-4a09-a37a-c666adee48b3",
				},
				StagingTargetPath: "/data",
				VolumeCapability: &csi.VolumeCapability{
					AccessMode: &csi.VolumeCapability_AccessMode{
						Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_MULTI_WRITER,
					},
					AccessType: &csi.VolumeCapability_Block{
						Block: &csi.VolumeCapability_BlockVolume{},
					},
				},
				VolumeContext: map[string]string{
					encryptedKey: "true",
					kmsKeyIDKey:  "8c9cf1cf-b38e-4f1b-b870-ae640fefc2a6",
				},
			},
			wantErrCode: codes.NotFound,
		},
	}
	for _, tt := range tests {
		ctx := context.Background()
		d := &nodeService{
			diskUtils: newFakeDiskUtils(server),
		}
		t.Run(tt.name, func(t *testing.T) {
			_, err := d.NodeStageVolume(ctx, tt.req)
			if (err != nil) && status.Code(err) != tt.wantErrCode {
				t.Errorf("NodeStageVolume() error = %v, wantErrCode %v", err, tt.wantErrCode)
				return
			}
		})
	}
}
