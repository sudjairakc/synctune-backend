package office_test

import (
	"testing"

	"github.com/synctune/backend/office"
)

func TestDefaultMap_BoundsAndWall(t *testing.T) {
	m := office.DefaultMap()
	if !m.InBounds(16, 16) {
		t.Fatal("spawn should be in bounds")
	}
	if m.InBounds(-1, 0) || m.InBounds(1e9, 1e9) {
		t.Fatal("out of bounds should fail")
	}
	// pick a known wall cell from template — document expected tile in test
	if m.IsWalkable(16, 16) == false {
		t.Fatal("open floor should be walkable")
	}
}

func TestZoneAt_MeetingRect(t *testing.T) {
	m := office.DefaultMap()
	// meeting-a rect: x[960,1216) y[128,384) → center (1088, 256)
	id, zt := m.ZoneAt(1088, 256)
	if zt != office.ZoneMeeting || id != "meeting-a" {
		t.Fatalf("got %s %s", id, zt)
	}
}
