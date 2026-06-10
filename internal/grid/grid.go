// Package grid holds the British National Grid cell geometry shared by the
// collision panel builder and the safety model: deterministic square cells keyed
// "i_j" from an easting/northing, and the inverse centroid lookup (for mapping a
// cell back to a coordinate when plotting).
package grid

import (
	"math"
	"strconv"
	"strings"
)

// CellID is the "i_j" key of the cellM-metre square containing (easting, northing)
// in British National Grid metres.
func CellID(easting, northing, cellM float64) string {
	return strconv.Itoa(int(math.Floor(easting/cellM))) + "_" + strconv.Itoa(int(math.Floor(northing/cellM)))
}

// CellCentroid returns the BNG centre of the cell with the given id, or ok=false
// if the id is malformed.
func CellCentroid(id string, cellM float64) (easting, northing float64, ok bool) {
	parts := strings.SplitN(id, "_", 2)
	if len(parts) != 2 {
		return 0, 0, false
	}
	i, err1 := strconv.Atoi(parts[0])
	j, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil {
		return 0, 0, false
	}
	return (float64(i) + 0.5) * cellM, (float64(j) + 0.5) * cellM, true
}
