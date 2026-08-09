package office_test

import (
	"testing"

	"github.com/synctune/backend/office"
)

func TestDefaultMap_BoundsAndWall(t *testing.T) {
	m := office.DefaultMap()
	if !m.InBounds(16, 16) {
		t.Fatal("border tile should still be in bounds")
	}
	if m.InBounds(-1, 0) || m.InBounds(1e9, 1e9) {
		t.Fatal("out of bounds should fail")
	}

	// tile (1,1) center — interior open floor
	if !m.IsWalkable(48, 48) {
		t.Fatal("interior open floor should be walkable")
	}

	// tile (0,0) center — outer border wall
	if m.IsWalkable(16, 16) {
		t.Fatal("border wall should not be walkable")
	}
	id, zt := m.ZoneAt(16, 16)
	if zt != office.ZoneWall || id != "" {
		t.Fatalf("border tile should be ZoneWall, got %q %s", id, zt)
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
