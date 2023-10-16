package driver

import (
	"reflect"
	"strconv"
	"testing"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/scaleway/scaleway-csi/pkg/scaleway"
	block "github.com/scaleway/scaleway-sdk-go/api/block/v1alpha1"
	"github.com/scaleway/scaleway-sdk-go/scw"
)

func TestExtractIDAndZone(t *testing.T) {
	t.Parallel()
	type args struct {
		id string
	}
	tests := []struct {
		name    string
		args    args
		want    string
		want1   scw.Zone
		wantErr bool
	}{
		{
			name: "empty ID",
			args: args{
				id: "",
			},
			want:    "",
			want1:   scw.Zone(""),
			wantErr: true,
		},
		{
			name: "invalid ID format",
			args: args{
				id: "fr-par-1/4d33a22f-9794-4f29-a92e-083c03d60681/abcd",
			},
			want:    "",
			want1:   scw.Zone(""),
			wantErr: true,
		},
		{
			name: "ID without zone",
			args: args{
				id: "4d33a22f-9794-4f29-a92e-083c03d60681",
			},
			want:    "4d33a22f-9794-4f29-a92e-083c03d60681",
			want1:   scw.Zone(""),
			wantErr: false,
		},
		{
			name: "ID and zone",
			args: args{
				id: "fr-par-1/4d33a22f-9794-4f29-a92e-083c03d60681",
			},
			want:    "4d33a22f-9794-4f29-a92e-083c03d60681",
			want1:   scw.ZoneFrPar1,
			wantErr: false,
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, got1, err := ExtractIDAndZone(tt.args.id)
			if (err != nil) != tt.wantErr {
				t.Errorf("ExtractIDAndZone() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("ExtractIDAndZone() got = %v, want %v", got, tt.want)
			}
			if !reflect.DeepEqual(got1, tt.want1) {
				t.Errorf("ExtractIDAndZone() got1 = %v, want %v", got1, tt.want1)
			}
		})
	}
}

func Test_chooseZones(t *testing.T) {
	t.Parallel()
	type args struct {
		accessibilityRequirements *csi.TopologyRequirement
		snapshotZone              scw.Zone
	}
	tests := []struct {
		name    string
		args    args
		want    []scw.Zone
		wantErr bool
	}{
		{
			name: "nothing should return empty zone",
			args: args{
				snapshotZone: scw.Zone(""),
			},
			want:    []scw.Zone{},
			wantErr: false,
		},
		{
			name: "nothing (non-nil) should return empty zone",
			args: args{
				accessibilityRequirements: &csi.TopologyRequirement{},
				snapshotZone:              scw.Zone(""),
			},
			want:    []scw.Zone{},
			wantErr: false,
		},
		{
			name: "snapshot from fr-par-1",
			args: args{
				snapshotZone: scw.ZoneFrPar1,
			},
			want:    []scw.Zone{scw.ZoneFrPar1},
			wantErr: false,
		},
		{
			name: "empty preferred",
			args: args{
				accessibilityRequirements: &csi.TopologyRequirement{
					Preferred: []*csi.Topology{
						{
							Segments: map[string]string{},
						},
					},
				},
				snapshotZone: scw.Zone(""),
			},
			want:    []scw.Zone{},
			wantErr: false,
		},
		{
			name: "empty requisite",
			args: args{
				accessibilityRequirements: &csi.TopologyRequirement{
					Requisite: []*csi.Topology{
						{
							Segments: map[string]string{},
						},
					},
				},
				snapshotZone: scw.Zone(""),
			},
			want:    []scw.Zone{},
			wantErr: false,
		},
		{
			name: "empty preferred and requisite",
			args: args{
				accessibilityRequirements: &csi.TopologyRequirement{
					Preferred: []*csi.Topology{
						{
							Segments: map[string]string{},
						},
					},
					Requisite: []*csi.Topology{
						{
							Segments: map[string]string{},
						},
					},
				},
				snapshotZone: scw.Zone(""),
			},
			want:    []scw.Zone{},
			wantErr: false,
		},
		{
			name: "fr-par-1 requisite",
			args: args{
				accessibilityRequirements: &csi.TopologyRequirement{
					Preferred: []*csi.Topology{
						{
							Segments: map[string]string{},
						},
					},
					Requisite: []*csi.Topology{
						{
							Segments: map[string]string{
								ZoneTopologyKey: string(scw.ZoneFrPar1),
							},
						},
					},
				},
				snapshotZone: scw.Zone(""),
			},
			want:    []scw.Zone{scw.ZoneFrPar1},
			wantErr: false,
		},
		{
			name: "fr-par-1 preferred and requisite",
			args: args{
				accessibilityRequirements: &csi.TopologyRequirement{
					Preferred: []*csi.Topology{
						{
							Segments: map[string]string{
								ZoneTopologyKey: string(scw.ZoneFrPar1),
							},
						},
					},
					Requisite: []*csi.Topology{
						{
							Segments: map[string]string{
								ZoneTopologyKey: string(scw.ZoneFrPar1),
							},
						},
					},
				},
				snapshotZone: scw.Zone(""),
			},
			want:    []scw.Zone{scw.ZoneFrPar1},
			wantErr: false,
		},
		{
			name: "fr-par-1 preferred and requisite, fr-par2 requisite",
			args: args{
				accessibilityRequirements: &csi.TopologyRequirement{
					Preferred: []*csi.Topology{
						{
							Segments: map[string]string{
								ZoneTopologyKey: string(scw.ZoneFrPar1),
							},
						},
					},
					Requisite: []*csi.Topology{
						{
							Segments: map[string]string{
								ZoneTopologyKey: string(scw.ZoneFrPar1),
							},
						},
						{
							Segments: map[string]string{
								ZoneTopologyKey: string(scw.ZoneFrPar2),
							},
						},
					},
				},
				snapshotZone: scw.Zone(""),
			},
			want:    []scw.Zone{scw.ZoneFrPar1, scw.ZoneFrPar2},
			wantErr: false,
		},
		{
			name: "fr-par-1/fr-par-2 preferred and requisite",
			args: args{
				accessibilityRequirements: &csi.TopologyRequirement{
					Preferred: []*csi.Topology{
						{
							Segments: map[string]string{
								ZoneTopologyKey: string(scw.ZoneFrPar2),
							},
						},
						{
							Segments: map[string]string{
								ZoneTopologyKey: string(scw.ZoneFrPar1),
							},
						},
					},
					Requisite: []*csi.Topology{
						{
							Segments: map[string]string{
								ZoneTopologyKey: string(scw.ZoneFrPar1),
							},
						},
						{
							Segments: map[string]string{
								ZoneTopologyKey: string(scw.ZoneFrPar2),
							},
						},
					},
				},
				snapshotZone: scw.Zone(""),
			},
			want:    []scw.Zone{scw.ZoneFrPar2, scw.ZoneFrPar1},
			wantErr: false,
		},
		{
			name: "fr-par-4/fr-par-3 preferred and requisite",
			args: args{
				accessibilityRequirements: &csi.TopologyRequirement{
					Preferred: []*csi.Topology{
						{
							Segments: map[string]string{
								ZoneTopologyKey: string("fr-par-4"),
							},
						},
						{
							Segments: map[string]string{
								ZoneTopologyKey: string("fr-par-3"),
							},
						},
					},
					Requisite: []*csi.Topology{
						{
							Segments: map[string]string{
								ZoneTopologyKey: string("fr-par-4"),
							},
						},
						{
							Segments: map[string]string{
								ZoneTopologyKey: string("fr-par-3"),
							},
						},
					},
				},
				snapshotZone: scw.Zone(""),
			},
			want:    []scw.Zone{"fr-par-4", "fr-par-3"},
			wantErr: false,
		},
		{
			name: "invalid topology values should be ignored",
			args: args{
				accessibilityRequirements: &csi.TopologyRequirement{
					Preferred: []*csi.Topology{
						{
							Segments: map[string]string{
								ZoneTopologyKey: string("fr-ams"),
							},
						},
					},
					Requisite: []*csi.Topology{
						{
							Segments: map[string]string{
								ZoneTopologyKey: string("fr-par"),
							},
						},
					},
				},
				snapshotZone: scw.Zone(""),
			},
			want:    []scw.Zone{},
			wantErr: false,
		},
		{
			name: "invalid topology keys should be ignored",
			args: args{
				accessibilityRequirements: &csi.TopologyRequirement{
					Preferred: []*csi.Topology{
						{
							Segments: map[string]string{
								"test": string("fr-ams"),
							},
						},
					},
					Requisite: []*csi.Topology{
						{
							Segments: map[string]string{
								"testagain": string("fr-par"),
							},
						},
					},
				},
				snapshotZone: scw.Zone(""),
			},
			want:    []scw.Zone{},
			wantErr: false,
		},
		{
			name: "fr-par-1 preferred",
			args: args{
				accessibilityRequirements: &csi.TopologyRequirement{
					Preferred: []*csi.Topology{
						{
							Segments: map[string]string{
								ZoneTopologyKey: string(scw.ZoneFrPar1),
							},
						},
					},
				},
				snapshotZone: scw.Zone(""),
			},
			want:    []scw.Zone{scw.ZoneFrPar1},
			wantErr: false,
		},
		{
			name: "fr-par-1 preferred and requisite, fr-par-2 requisite, snapshot in fr-par-1",
			args: args{
				accessibilityRequirements: &csi.TopologyRequirement{
					Preferred: []*csi.Topology{
						{
							Segments: map[string]string{
								ZoneTopologyKey: string(scw.ZoneFrPar1),
							},
						},
					},
					Requisite: []*csi.Topology{
						{
							Segments: map[string]string{
								ZoneTopologyKey: string(scw.ZoneFrPar1),
							},
						},
						{
							Segments: map[string]string{
								ZoneTopologyKey: string(scw.ZoneFrPar2),
							},
						},
					},
				},
				snapshotZone: scw.ZoneFrPar1,
			},
			want:    []scw.Zone{scw.ZoneFrPar1},
			wantErr: false,
		},
		{
			name: "fr-par-1 preferred (with duplicate) and requisite, snapshot in fr-par-1",
			args: args{
				accessibilityRequirements: &csi.TopologyRequirement{
					Preferred: []*csi.Topology{
						{
							Segments: map[string]string{
								ZoneTopologyKey: string(scw.ZoneFrPar1),
							},
						},
						{
							Segments: map[string]string{
								ZoneTopologyKey: string(scw.ZoneFrPar1),
							},
						},
					},
					Requisite: []*csi.Topology{
						{
							Segments: map[string]string{
								ZoneTopologyKey: string(scw.ZoneFrPar1),
							},
						},
					},
				},
				snapshotZone: scw.ZoneFrPar1,
			},
			want:    []scw.Zone{scw.ZoneFrPar1},
			wantErr: false,
		},
		{
			name: "fr-par-1/fr-par-2 preferred and requisite, snapshot in fr-par-1",
			args: args{
				accessibilityRequirements: &csi.TopologyRequirement{
					Preferred: []*csi.Topology{
						{
							Segments: map[string]string{
								ZoneTopologyKey: string(scw.ZoneFrPar1),
							},
						},
						{
							Segments: map[string]string{
								ZoneTopologyKey: string(scw.ZoneFrPar2),
							},
						},
					},
					Requisite: []*csi.Topology{
						{
							Segments: map[string]string{
								ZoneTopologyKey: string(scw.ZoneFrPar1),
							},
						},
						{
							Segments: map[string]string{
								ZoneTopologyKey: string(scw.ZoneFrPar2),
							},
						},
					},
				},
				snapshotZone: scw.ZoneFrPar1,
			},
			want:    []scw.Zone{scw.ZoneFrPar1},
			wantErr: false,
		},
		{
			name: "fr-par-1/fr-par-2 preferred and requisite, snapshot in nl-ams-1 should error",
			args: args{
				accessibilityRequirements: &csi.TopologyRequirement{
					Preferred: []*csi.Topology{
						{
							Segments: map[string]string{
								ZoneTopologyKey: string(scw.ZoneFrPar1),
							},
						},
						{
							Segments: map[string]string{
								ZoneTopologyKey: string(scw.ZoneFrPar2),
							},
						},
					},
					Requisite: []*csi.Topology{
						{
							Segments: map[string]string{
								ZoneTopologyKey: string(scw.ZoneFrPar1),
							},
						},
						{
							Segments: map[string]string{
								ZoneTopologyKey: string(scw.ZoneFrPar2),
							},
						},
					},
				},
				snapshotZone: scw.ZoneNlAms1,
			},
			want:    nil,
			wantErr: true,
		},
		{
			name: "fr-par-1/fr-par-2 preferred and fr-par-2 requisite should error",
			args: args{
				accessibilityRequirements: &csi.TopologyRequirement{
					Preferred: []*csi.Topology{
						{
							Segments: map[string]string{
								ZoneTopologyKey: string(scw.ZoneFrPar2),
							},
						},
						{
							Segments: map[string]string{
								ZoneTopologyKey: string(scw.ZoneFrPar1),
							},
						},
					},
					Requisite: []*csi.Topology{
						{
							Segments: map[string]string{
								ZoneTopologyKey: string(scw.ZoneFrPar2),
							},
						},
					},
				},
				snapshotZone: scw.ZoneFrPar1,
			},
			want:    nil,
			wantErr: true,
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := chooseZones(tt.args.accessibilityRequirements, tt.args.snapshotZone)
			if (err != nil) != tt.wantErr {
				t.Errorf("chooseZones() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("chooseZones() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_validateVolumeCapability(t *testing.T) {
	t.Parallel()
	type args struct {
		volumeCapability *csi.VolumeCapability
	}
	tests := []struct {
		name      string
		args      args
		wantBlock bool
		wantMount bool
		wantErr   bool
	}{
		{
			name: "multi node is not supported",
			args: args{
				volumeCapability: &csi.VolumeCapability{
					AccessType: &csi.VolumeCapability_Block{},
					AccessMode: &csi.VolumeCapability_AccessMode{
						Mode: csi.VolumeCapability_AccessMode_MULTI_NODE_MULTI_WRITER,
					},
				},
			},
			wantBlock: false,
			wantMount: false,
			wantErr:   true,
		},
		{
			name: "single node as block",
			args: args{
				volumeCapability: &csi.VolumeCapability{
					AccessType: &csi.VolumeCapability_Block{Block: &csi.VolumeCapability_BlockVolume{}},
					AccessMode: &csi.VolumeCapability_AccessMode{
						Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
					},
				},
			},
			wantBlock: true,
			wantMount: false,
			wantErr:   false,
		},
		{
			name: "single node as mount",
			args: args{
				volumeCapability: &csi.VolumeCapability{
					AccessType: &csi.VolumeCapability_Mount{Mount: &csi.VolumeCapability_MountVolume{}},
					AccessMode: &csi.VolumeCapability_AccessMode{
						Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
					},
				},
			},
			wantBlock: false,
			wantMount: true,
			wantErr:   false,
		},
		{
			name: "empty access type should error",
			args: args{
				volumeCapability: &csi.VolumeCapability{
					AccessType: nil,
					AccessMode: &csi.VolumeCapability_AccessMode{
						Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
					},
				},
			},
			wantBlock: false,
			wantMount: false,
			wantErr:   true,
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			gotBlock, gotMount, err := validateVolumeCapability(tt.args.volumeCapability)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateVolumeCapability() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if gotBlock != tt.wantBlock {
				t.Errorf("validateVolumeCapability() gotBlock = %v, want %v", gotBlock, tt.wantBlock)
			}
			if gotMount != tt.wantMount {
				t.Errorf("validateVolumeCapability() gotMount = %v, want %v", gotMount, tt.wantMount)
			}
		})
	}
}

func Test_validateVolumeCapabilities(t *testing.T) {
	t.Parallel()
	type args struct {
		volumeCapabilities []*csi.VolumeCapability
		optional           bool
	}
	tests := []struct {
		name    string
		args    args
		wantErr bool
	}{
		{
			name: "empty volumeCapabilities should error when not optional",
			args: args{
				volumeCapabilities: []*csi.VolumeCapability{},
				optional:           false,
			},
			wantErr: true,
		},
		{
			name: "empty volumeCapabilities should not error when optional",
			args: args{
				volumeCapabilities: []*csi.VolumeCapability{},
				optional:           true,
			},
			wantErr: false,
		},
		{
			name: "valid volumeCapabilities",
			args: args{
				volumeCapabilities: []*csi.VolumeCapability{
					{
						AccessType: &csi.VolumeCapability_Block{Block: &csi.VolumeCapability_BlockVolume{}},
						AccessMode: &csi.VolumeCapability_AccessMode{
							Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
						},
					},
					{
						AccessType: &csi.VolumeCapability_Mount{Mount: &csi.VolumeCapability_MountVolume{}},
						AccessMode: &csi.VolumeCapability_AccessMode{
							Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
						},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "second volumeCapability is invalid",
			args: args{
				volumeCapabilities: []*csi.VolumeCapability{
					{
						AccessType: &csi.VolumeCapability_Block{Block: &csi.VolumeCapability_BlockVolume{}},
						AccessMode: &csi.VolumeCapability_AccessMode{
							Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
						},
					},
					{
						AccessType: &csi.VolumeCapability_Mount{Mount: &csi.VolumeCapability_MountVolume{}},
						AccessMode: &csi.VolumeCapability_AccessMode{
							Mode: csi.VolumeCapability_AccessMode_MULTI_NODE_MULTI_WRITER,
						},
					},
				},
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if err := validateVolumeCapabilities(tt.args.volumeCapabilities, tt.args.optional); (err != nil) != tt.wantErr {
				t.Errorf("validateVolumeCapabilities() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func Test_isVolumeEncrypted(t *testing.T) {
	t.Parallel()
	type args struct {
		volumeContext map[string]string
	}
	tests := []struct {
		name    string
		args    args
		want    bool
		wantErr bool
	}{
		{
			name: "volume should be encrypted",
			args: args{
				volumeContext: map[string]string{
					encryptedKey: "true",
				},
			},
			want:    true,
			wantErr: false,
		},
		{
			name: "volume should not be encrypted",
			args: args{
				volumeContext: map[string]string{
					encryptedKey: "false",
				},
			},
			want:    false,
			wantErr: false,
		},
		{
			name: "volume should not be encrypted by default",
			args: args{
				volumeContext: map[string]string{},
			},
			want:    false,
			wantErr: false,
		},
		{
			name: "invalid encrypted value",
			args: args{
				volumeContext: map[string]string{
					encryptedKey: "invalid",
				},
			},
			want:    false,
			wantErr: true,
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := isVolumeEncrypted(tt.args.volumeContext)
			if (err != nil) != tt.wantErr {
				t.Errorf("isVolumeEncrypted() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("isVolumeEncrypted() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_expandZonalID(t *testing.T) {
	type args struct {
		id   string
		zone scw.Zone
	}
	tests := []struct {
		name string
		args args
		want string
	}{
		{
			name: "fr-par-1/4d33a22f-9794-4f29-a92e-083c03d60681",
			args: args{
				id:   "4d33a22f-9794-4f29-a92e-083c03d60681",
				zone: scw.ZoneFrPar1,
			},
			want: "fr-par-1/4d33a22f-9794-4f29-a92e-083c03d60681",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := expandZonalID(tt.args.id, tt.args.zone); got != tt.want {
				t.Errorf("expandZonalID() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_getVolumeRequestCapacity(t *testing.T) {
	t.Parallel()
	type args struct {
		capacityRange *csi.CapacityRange
	}
	tests := []struct {
		name    string
		args    args
		want    int64
		wantErr bool
	}{
		{
			name: "nil capacity range, should default to driver MinVolumeSize",
			args: args{
				capacityRange: nil,
			},
			want:    scaleway.MinVolumeSize,
			wantErr: false,
		},
		{
			name: "empty capacity range, should default to driver MinVolumeSize",
			args: args{
				capacityRange: &csi.CapacityRange{},
			},
			want:    scaleway.MinVolumeSize,
			wantErr: false,
		},
		{
			name: "limit less than required",
			args: args{
				capacityRange: &csi.CapacityRange{
					LimitBytes:    1000000000,
					RequiredBytes: 2000000000,
				},
			},
			want:    0,
			wantErr: true,
		},
		{
			name: "required less than driver MinVolumeSize",
			args: args{
				capacityRange: &csi.CapacityRange{
					RequiredBytes: scaleway.MinVolumeSize - 100,
				},
			},
			want:    0,
			wantErr: true,
		},
		{
			name: "limit less than driver MinVolumeSize",
			args: args{
				capacityRange: &csi.CapacityRange{
					LimitBytes: scaleway.MinVolumeSize - 100,
				},
			},
			want:    0,
			wantErr: true,
		},
		{
			name: "limit == required",
			args: args{
				capacityRange: &csi.CapacityRange{
					LimitBytes:    2000000000,
					RequiredBytes: 2000000000,
				},
			},
			want:    2000000000,
			wantErr: false,
		},
		{
			name: "when required and limit are set, should return required",
			args: args{
				capacityRange: &csi.CapacityRange{
					LimitBytes:    3000000000,
					RequiredBytes: 2000000000,
				},
			},
			want:    2000000000,
			wantErr: false,
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := getVolumeRequestCapacity(tt.args.capacityRange)
			if (err != nil) != tt.wantErr {
				t.Errorf("getVolumeRequestCapacity() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("getVolumeRequestCapacity() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_parseCreateVolumeParams(t *testing.T) {
	t.Parallel()
	type args struct {
		params map[string]string
	}
	tests := []struct {
		name    string
		args    args
		want    *uint32
		want1   bool
		wantErr bool
	}{
		{
			name: "no params",
			args: args{
				params: map[string]string{},
			},
			want:    nil,
			want1:   false,
			wantErr: false,
		},
		{
			name: "unknown param",
			args: args{
				params: map[string]string{
					"unknown_param": "unknown_param_value",
				},
			},
			want:    nil,
			want1:   false,
			wantErr: true,
		},
		{
			name: "encryption set to true",
			args: args{
				params: map[string]string{
					encryptedKey: "true",
				},
			},
			want:    nil,
			want1:   true,
			wantErr: false,
		},
		{
			name: "encryption set to false",
			args: args{
				params: map[string]string{
					encryptedKey: "false",
				},
			},
			want:    nil,
			want1:   false,
			wantErr: false,
		},
		{
			name: "encryption set to non-boolean value should error",
			args: args{
				params: map[string]string{
					encryptedKey: "abcd",
				},
			},
			want:    nil,
			want1:   false,
			wantErr: true,
		},
		{
			name: "legacy default volume type should return nil iops",
			args: args{
				params: map[string]string{
					volumeTypeKey: scaleway.LegacyDefaultVolumeType,
				},
			},
			want:    nil,
			want1:   false,
			wantErr: false,
		},
		{
			name: "legacy default volume type compatible with iops param set to 5K",
			args: args{
				params: map[string]string{
					volumeTypeKey: scaleway.LegacyDefaultVolumeType,
					volumeIOPSKey: strconv.Itoa(scaleway.LegacyDefaultVolumeTypeIOPS),
				},
			},
			want:    scw.Uint32Ptr(scaleway.LegacyDefaultVolumeTypeIOPS),
			want1:   false,
			wantErr: false,
		},
		{
			name: "legacy default volume type not compatible with iops param set to something else than 5K",
			args: args{
				params: map[string]string{
					volumeTypeKey: scaleway.LegacyDefaultVolumeType,
					volumeIOPSKey: "1234",
				},
			},
			want:    nil,
			want1:   false,
			wantErr: true,
		},
		{
			name: "unknown volume type",
			args: args{
				params: map[string]string{
					volumeTypeKey: "abcd",
				},
			},
			want:    nil,
			want1:   false,
			wantErr: true,
		},
		{
			name: "iops not a number",
			args: args{
				params: map[string]string{
					volumeIOPSKey: "abcd",
				},
			},
			want:    nil,
			want1:   false,
			wantErr: true,
		},
		{
			name: "iops and encryption set",
			args: args{
				params: map[string]string{
					volumeIOPSKey: "15000",
					encryptedKey:  "true",
				},
			},
			want:    scw.Uint32Ptr(15000),
			want1:   true,
			wantErr: false,
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, got1, err := parseCreateVolumeParams(tt.args.params)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseCreateVolumeParams() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("parseCreateVolumeParams() got = %v, want %v", got, tt.want)
			}
			if got1 != tt.want1 {
				t.Errorf("parseCreateVolumeParams() got1 = %v, want %v", got1, tt.want1)
			}
		})
	}
}

func Test_parseStartingToken(t *testing.T) {
	t.Parallel()
	type args struct {
		token string
	}
	tests := []struct {
		name    string
		args    args
		want    uint32
		wantErr bool
	}{
		{
			name: "not a number",
			args: args{
				token: "abcd",
			},
			want:    0,
			wantErr: true,
		},
		{
			name: "defaults to 0",
			args: args{
				token: "",
			},
			want:    0,
			wantErr: false,
		},
		{
			name: "min 0",
			args: args{
				token: "-2",
			},
			want:    0,
			wantErr: false,
		},
		{
			name: "123",
			args: args{
				token: "123",
			},
			want:    123,
			wantErr: false,
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := parseStartingToken(tt.args.token)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseStartingToken() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("parseStartingToken() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_publishedNodeIDs(t *testing.T) {
	t.Parallel()
	type args struct {
		volume *block.Volume
	}
	tests := []struct {
		name string
		args args
		want []string
	}{
		{
			name: "volume is not attached to anything",
			args: args{
				volume: &block.Volume{},
			},
			want: []string{},
		},
		{
			name: "volume is attached to one server",
			args: args{
				volume: &block.Volume{
					Zone: scw.ZoneFrPar1,
					References: []*block.Reference{
						{
							ProductResourceType: scaleway.InstanceServerProductResourceType,
							ProductResourceID:   "618712ec-3bdd-4497-ae44-a91bb2569ef1",
						},
					},
				},
			},
			want: []string{"fr-par-1/618712ec-3bdd-4497-ae44-a91bb2569ef1"},
		},
		{
			name: "volume is attached to another product",
			args: args{
				volume: &block.Volume{
					References: []*block.Reference{
						{
							ProductResourceType: "another_product",
							ProductResourceID:   "618712ec-3bdd-4497-ae44-a91bb2569ef1",
						},
					},
				},
			},
			want: []string{},
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := publishedNodeIDs(tt.args.volume); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("publishedNodeIDs() = %v, want %v", got, tt.want)
			}
		})
	}
}
