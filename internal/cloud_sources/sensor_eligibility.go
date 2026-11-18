package cloud_sources

import (
	"slices"
	"strings"

	"github.com/qlustered/qctl/internal/api"
)

// sensorEligibleTypes mirrors atlas SENSOR_ELIGIBLE_SOURCE_TYPES
// (common/common/sensor_eligibility.py). No generated type carries it, so
// re-check it by hand at every regen.
var sensorEligibleTypes = []api.DataSourceType{
	api.DataSourceTypeS3,
	api.DataSourceTypeS3Compatible,
	api.DataSourceTypeSftp,
	api.DataSourceTypeGs,
	api.DataSourceTypeDropbox,
	api.DataSourceTypeSimpleURL,
}

// SensorEligible reports whether t is a source type the sensor routes accept.
func SensorEligible(t api.DataSourceType) bool {
	return slices.Contains(sensorEligibleTypes, t)
}

// SensorEligibleTypeList renders the sensor-eligible types as an English
// list, "a, b and c".
func SensorEligibleTypeList() string {
	names := make([]string, len(sensorEligibleTypes))
	for i, t := range sensorEligibleTypes {
		names[i] = string(t)
	}
	last := len(names) - 1
	return strings.Join(names[:last], ", ") + " and " + names[last]
}
