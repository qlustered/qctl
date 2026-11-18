package cloud_sources

import (
	"slices"
	"testing"

	"github.com/qlustered/qctl/internal/api"
)

// The sensor-eligible set comes from atlas, not from the spec: no generated
// enum carries it. So every DataSourceType the spec declares must be
// classified here by hand, and a regen that adds one fails until someone has
// looked the new type up in atlas.
func TestSensorEligibilityClassifiesEveryDataSourceType(t *testing.T) {
	const recheck = "No generated enum carries the sensor-eligible set: re-check " +
		"SENSOR_ELIGIBLE_SOURCE_TYPES in atlas common/common/sensor_eligibility.py by hand, " +
		"then update sensorEligibleTypes (sensor_eligibility.go) or knownIneligible (this test)."
	wantEligible := []string{"s3", "s3_compatible", "sftp", "gs", "dropbox", "simple_url"}
	knownIneligible := []string{"qluster_ui", "composite"}

	values := api.EnumValues[api.DataSourceType]()
	if len(values) == 0 {
		t.Fatal("api.EnumValues[api.DataSourceType]() is empty; the classification below would pass vacuously")
	}
	for _, v := range values {
		eligible := SensorEligible(api.DataSourceType(v))
		switch {
		case eligible && !slices.Contains(wantEligible, v):
			t.Errorf("DataSourceType %q is sensor-eligible but not in the pinned eligible set. %s", v, recheck)
		case !eligible && !slices.Contains(knownIneligible, v):
			t.Errorf("DataSourceType %q is neither sensor-eligible nor a known ineligible type. %s", v, recheck)
		}
	}
	for _, v := range append(slices.Clone(wantEligible), knownIneligible...) {
		if !slices.Contains(values, v) {
			t.Errorf("%q is classified here but is no longer a DataSourceType. %s", v, recheck)
		}
	}

	got := make([]string, len(sensorEligibleTypes))
	for i, typ := range sensorEligibleTypes {
		got[i] = string(typ)
	}
	if !slices.Equal(got, wantEligible) {
		t.Errorf("sensorEligibleTypes = %v, want %v. %s", got, wantEligible, recheck)
	}
	if SensorEligible("") {
		t.Error("a source with no type set must not be sensor-eligible")
	}
}

func TestSensorEligibleTypeListRendersEveryType(t *testing.T) {
	if got, want := SensorEligibleTypeList(), "s3, s3_compatible, sftp, gs, dropbox and simple_url"; got != want {
		t.Errorf("SensorEligibleTypeList() = %q, want %q", got, want)
	}
}
