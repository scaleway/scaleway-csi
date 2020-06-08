package driver

import (
	"fmt"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/scaleway/scaleway-sdk-go/scw"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// AssertTrue fails the test if is not true.
func AssertTrue(tb testing.TB, test bool) {
	if !test {
		_, file, line, _ := runtime.Caller(1)
		fmt.Printf("\033[31m%s:%d: unexpected result: %t (wanted true)\033[39m\n", filepath.Base(file), line, test)
		tb.FailNow()
	}
}

// AssertFalse fails the test if is not false.
func AssertFalse(tb testing.TB, test bool) {
	if test {
		_, file, line, _ := runtime.Caller(1)
		fmt.Printf("\033[31m%s:%d: unexpected result: %t (wanted false)\033[39m\n", filepath.Base(file), line, test)
		tb.FailNow()
	}
}

// AssertNoError fails the test if an err is not nil.
func AssertNoError(tb testing.TB, err error) {
	if err != nil {
		_, file, line, _ := runtime.Caller(1)
		fmt.Printf("\033[31m%s:%d: unexpected error: %s\033[39m\n", filepath.Base(file), line, err.Error())
		tb.FailNow()
	}
}

// Equals fails the test if exp is not equal to act.
func Equals(tb testing.TB, exp, act interface{}) {
	if !reflect.DeepEqual(exp, act) {
		_, file, line, _ := runtime.Caller(1)
		fmt.Printf("\033[31m%s:%d: unexpected result\nexp: %#v\ngot: %#v\033[39m\n", filepath.Base(file), line, exp, act)
		tb.FailNow()
	}
}

func Test_extractIDAndZone(t *testing.T) {
	t.Run("simpleID", func(t *testing.T) {
		id, zone, err := extractIDAndZone("testID", "")
		AssertNoError(t, err)
		Equals(t, "testID", id)
		Equals(t, scw.Zone(""), zone)
	})
	t.Run("idAndZone", func(t *testing.T) {
		id, zone, err := extractIDAndZone("fr-par-1/testID", "")
		AssertNoError(t, err)
		Equals(t, "testID", id)
		Equals(t, scw.ZoneFrPar1, zone)
	})
	t.Run("idAndBadZone", func(t *testing.T) {
		id, zone, err := extractIDAndZone("blabla/testID", "")
		AssertNoError(t, err)
		Equals(t, "testID", id)
		Equals(t, scw.Zone(""), zone)
	})
	t.Run("idAndWrongZone", func(t *testing.T) {
		id, zone, err := extractIDAndZone("fr-ams-1/testID", "")
		AssertNoError(t, err)
		Equals(t, "testID", id)
		Equals(t, scw.Zone("fr-ams-1"), zone)
	})
	t.Run("emptyID", func(t *testing.T) {
		id, zone, err := extractIDAndZone("", "test")
		Equals(t, status.Errorf(codes.InvalidArgument, "test is not provided"), err)
		Equals(t, "", id)
		Equals(t, scw.Zone(""), zone)
	})
	t.Run("wrongFormat", func(t *testing.T) {
		id, zone, err := extractIDAndZone("a/b/c", "test")
		Equals(t, status.Errorf(codes.InvalidArgument, "wrong format for test"), err)
		Equals(t, "", id)
		Equals(t, scw.Zone(""), zone)
	})
}

func Test_chooseZones(t *testing.T) {
	testsBench := []struct {
		req      *csi.TopologyRequirement
		zone     scw.Zone
		expected []scw.Zone
		err      error
	}{
		{
			req:      nil,
			zone:     scw.Zone(""),
			expected: []scw.Zone{},
			err:      nil,
		},
		{
			req:      nil,
			zone:     scw.ZoneFrPar1,
			expected: []scw.Zone{scw.ZoneFrPar1},
			err:      nil,
		},
		{
			req:      &csi.TopologyRequirement{},
			zone:     scw.Zone(""),
			expected: []scw.Zone{},
			err:      nil,
		},
		{
			req: &csi.TopologyRequirement{
				Requisite: []*csi.Topology{
					{
						Segments: map[string]string{},
					},
				},
			},
			zone:     scw.Zone(""),
			expected: []scw.Zone{},
			err:      nil,
		},
		{
			req: &csi.TopologyRequirement{
				Preferred: []*csi.Topology{
					{
						Segments: map[string]string{},
					},
				},
			},
			zone:     scw.Zone(""),
			expected: []scw.Zone{},
			err:      nil,
		},
		{
			req: &csi.TopologyRequirement{
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
			zone:     scw.Zone(""),
			expected: []scw.Zone{},
			err:      nil,
		},
		{
			req: &csi.TopologyRequirement{
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
			zone:     scw.Zone(""),
			expected: []scw.Zone{scw.ZoneFrPar1},
			err:      nil,
		},
		{
			req: &csi.TopologyRequirement{
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
			zone:     scw.Zone(""),
			expected: []scw.Zone{scw.ZoneFrPar1},
			err:      nil,
		},
		{
			req: &csi.TopologyRequirement{
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
			zone:     scw.Zone(""),
			expected: []scw.Zone{scw.ZoneFrPar1, scw.ZoneFrPar2},
			err:      nil,
		},
		{
			req: &csi.TopologyRequirement{
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
			zone:     scw.Zone(""),
			expected: []scw.Zone{scw.ZoneFrPar2, scw.ZoneFrPar1},
			err:      nil,
		},
		{
			req: &csi.TopologyRequirement{
				Preferred: []*csi.Topology{
					{
						Segments: map[string]string{
							ZoneTopologyKey: "fr-par-4",
						},
					},
					{
						Segments: map[string]string{
							ZoneTopologyKey: "fr-par-3",
						},
					},
				},
				Requisite: []*csi.Topology{
					{
						Segments: map[string]string{
							ZoneTopologyKey: "fr-par-4",
						},
					},
					{
						Segments: map[string]string{
							ZoneTopologyKey: "fr-par-3",
						},
					},
				},
			},
			zone:     scw.Zone(""),
			expected: []scw.Zone{"fr-par-4", "fr-par-3"},
			err:      nil,
		},
		{
			req: &csi.TopologyRequirement{
				Preferred: []*csi.Topology{
					{
						Segments: map[string]string{
							ZoneTopologyKey: "fr-ams",
						},
					},
				},
				Requisite: []*csi.Topology{
					{
						Segments: map[string]string{
							ZoneTopologyKey: "fr-par",
						},
					},
				},
			},
			zone:     scw.Zone(""),
			expected: []scw.Zone{},
			err:      nil,
		},
		{
			req: &csi.TopologyRequirement{
				Preferred: []*csi.Topology{
					{
						Segments: map[string]string{
							"test": "fr-ams",
						},
					},
				},
				Requisite: []*csi.Topology{
					{
						Segments: map[string]string{
							"testagain": "fr-par",
						},
					},
				},
			},
			zone:     scw.Zone(""),
			expected: []scw.Zone{},
			err:      nil,
		},
		{
			req: &csi.TopologyRequirement{
				Preferred: []*csi.Topology{
					{
						Segments: map[string]string{
							ZoneTopologyKey: string(scw.ZoneFrPar1),
						},
					},
				},
			},
			zone:     scw.Zone(""),
			expected: []scw.Zone{scw.ZoneFrPar1},
			err:      nil,
		},
		{
			req: &csi.TopologyRequirement{
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
			zone:     scw.ZoneFrPar1,
			expected: []scw.Zone{scw.ZoneFrPar1},
			err:      nil,
		},
		{
			req: &csi.TopologyRequirement{
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
			zone:     scw.ZoneFrPar1,
			expected: []scw.Zone{scw.ZoneFrPar1},
			err:      nil,
		},
		{
			req: &csi.TopologyRequirement{
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
			zone:     scw.ZoneFrPar1,
			expected: []scw.Zone{scw.ZoneFrPar1},
			err:      nil,
		},
		{
			req: &csi.TopologyRequirement{
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
			zone:     scw.ZoneNlAms1,
			expected: nil,
			err:      status.Error(codes.ResourceExhausted, "desired volume content source and desired topology are not compatible, different zones"),
		},
		{
			req: &csi.TopologyRequirement{
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
			zone:     scw.Zone(""),
			expected: nil,
			err:      status.Errorf(codes.InvalidArgument, "%s: %s is specified in preferred but not in requisite", ZoneTopologyKey, scw.ZoneFrPar1),
		},
	}

	for _, test := range testsBench {
		zones, err := chooseZones(test.req, test.zone)
		Equals(t, test.expected, zones)
		Equals(t, test.err, err)
	}
}

func Test_validateVolumeCapabilities(t *testing.T) {
	testsBench := []struct {
		volCaps []*csi.VolumeCapability
		err     error
	}{
		{
			volCaps: nil,
			err:     errVolumeCapabilitiesIsNil,
		},
		{
			volCaps: []*csi.VolumeCapability{
				{
					AccessType: &csi.VolumeCapability_Block{},
				},
			},
			err: errAccessModeNotSupported,
		},
		{
			volCaps: []*csi.VolumeCapability{
				{
					AccessType: &csi.VolumeCapability_Block{},
					AccessMode: &csi.VolumeCapability_AccessMode{
						Mode: csi.VolumeCapability_AccessMode_MULTI_NODE_MULTI_WRITER,
					},
				},
			},
			err: errAccessModeNotSupported,
		},
		{
			volCaps: []*csi.VolumeCapability{
				{
					AccessType: &csi.VolumeCapability_Block{},
					AccessMode: &csi.VolumeCapability_AccessMode{
						Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
					},
				},
			},
			err: nil,
		},
		{
			volCaps: []*csi.VolumeCapability{
				{
					AccessType: &csi.VolumeCapability_Mount{},
					AccessMode: &csi.VolumeCapability_AccessMode{
						Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
					},
				},
			},
			err: nil,
		},
	}

	for _, test := range testsBench {
		err := validateVolumeCapabilities(test.volCaps)
		Equals(t, test.err, err)
	}
}

func Test_getVolumeRequestCapacity(t *testing.T) {
	var min int64 = 1000
	var max int64 = 1000000000
	testsBench := []struct {
		capRange *csi.CapacityRange
		res      int64
		err      error
	}{
		{
			capRange: &csi.CapacityRange{
				RequiredBytes: 0,
				LimitBytes:    0,
			},
			res: min,
			err: nil,
		},
		{
			capRange: &csi.CapacityRange{
				RequiredBytes: min + 10,
				LimitBytes:    0,
			},
			res: min + 10,
			err: nil,
		},
		{
			capRange: &csi.CapacityRange{
				RequiredBytes: 0,
				LimitBytes:    min + 10,
			},
			res: min + 10,
			err: nil,
		},
		{
			capRange: &csi.CapacityRange{
				RequiredBytes: min - 10,
				LimitBytes:    0,
			},
			res: 0,
			err: errRequiredBytesLessThanMinimun,
		},
		{
			capRange: &csi.CapacityRange{
				RequiredBytes: 0,
				LimitBytes:    min - 10,
			},
			res: 0,
			err: errLimitBytesLessThanMinimum,
		},
		{
			capRange: &csi.CapacityRange{
				RequiredBytes: min + 10,
				LimitBytes:    min + 5,
			},
			res: 0,
			err: errLimitBytesLessThanRequiredBytes,
		},
		{
			capRange: &csi.CapacityRange{
				RequiredBytes: min + 10,
				LimitBytes:    min + 5,
			},
			res: 0,
			err: errLimitBytesLessThanRequiredBytes,
		},
		{
			capRange: &csi.CapacityRange{
				RequiredBytes: max + 10,
				LimitBytes:    0,
			},
			res: 0,
			err: errRequiredBytesGreaterThanMaximun,
		},
		{
			capRange: &csi.CapacityRange{
				RequiredBytes: 0,
				LimitBytes:    max + 10,
			},
			res: 0,
			err: errLimitBytesGreaterThanMaximum,
		},
		{
			capRange: &csi.CapacityRange{
				RequiredBytes: min + 10,
				LimitBytes:    min + 10,
			},
			res: min + 10,
			err: nil,
		},
		{
			capRange: &csi.CapacityRange{
				RequiredBytes: min + 10,
				LimitBytes:    min + 20,
			},
			res: min + 10,
			err: nil,
		},
	}

	for _, test := range testsBench {
		res, err := getVolumeRequestCapacity(min, max, test.capRange)
		Equals(t, test.err, err)
		Equals(t, test.res, res)
	}
}
