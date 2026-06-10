package grid

import "testing"

func TestCellIDFloorsToCell(t *testing.T) {
	// 2km cells: (531000, 181000) -> floor(531000/2000)=265, floor(181000/2000)=90.
	if got := CellID(531000, 181000, 2000); got != "265_90" {
		t.Errorf("CellID = %q, want 265_90", got)
	}
	// Points in the same 2km square share an id.
	if CellID(531000, 181000, 2000) != CellID(531999, 181999, 2000) {
		t.Error("points in the same cell should share an id")
	}
}

func TestCellCentroidRoundTrips(t *testing.T) {
	e, n, ok := CellCentroid("265_90", 2000)
	if !ok || e != 531000 || n != 181000 {
		t.Errorf("centroid = (%v,%v,%v), want (531000,181000,true)", e, n, ok)
	}
	// The centroid of a cell maps back to the same cell.
	if CellID(e, n, 2000) != "265_90" {
		t.Error("centroid should fall inside its own cell")
	}
	if _, _, ok := CellCentroid("garbage", 2000); ok {
		t.Error("malformed id should return ok=false")
	}
}
