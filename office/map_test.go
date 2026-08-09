package office_test

import (
	"testing"

	"github.com/synctune/backend/office"
)

// Fixture tile centers from map_v2.json (first occurrence of each kind).
func tileCenter(tx, ty int) (x, y float64) {
	ts := float64(office.TileSize)
	return float64(tx)*ts + ts/2, float64(ty)*ts + ts/2
}

func TestMapV2_WalkContract(t *testing.T) {
	m := office.DefaultMap()

	cases := []struct {
		name     string
		kind     office.TileKind
		walkable bool
	}{
		{"floor", office.TileFloor, true},
		{"wall", office.TileWall, false},
		{"desk", office.TileDesk, false},
		{"chair", office.TileChair, true},
		{"door", office.TileDoor, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			x, y := office.MustFindTile(m, tc.kind)
			if got := m.TileAt(x, y); got != tc.kind {
				t.Fatalf("TileAt first %s = %q, want %q", tc.kind, got, tc.kind)
			}
			if got := m.IsWalkable(x, y); got != tc.walkable {
				t.Fatalf("IsWalkable first %s = %v, want %v", tc.kind, got, tc.walkable)
			}
		})
	}
}

func TestMapV2_SpawnOpenFloor(t *testing.T) {
	m := office.DefaultMap()
	x, y := m.SpawnWorld()
	if !m.IsWalkable(x, y) {
		t.Fatalf("spawn (%v,%v) must be walkable", x, y)
	}
	id, zt := m.ZoneAt(x, y)
	if zt != office.ZoneOpen || id != "" {
		t.Fatalf("spawn zone = %q %s, want open with empty id", id, zt)
	}
	if m.TileAt(x, y) != office.TileFloor {
		t.Fatalf("spawn tile = %q, want floor", m.TileAt(x, y))
	}
}

func TestMapV2_MeetingZones(t *testing.T) {
	m := office.DefaultMap()
	for _, id := range []string{"meeting-a", "meeting-b"} {
		x, y := office.MeetingCenter(m, id)
		gotID, zt := m.ZoneAt(x, y)
		if zt != office.ZoneMeeting || gotID != id {
			t.Fatalf("MeetingCenter(%s): ZoneAt=%q %s, want %q meeting", id, gotID, zt, id)
		}
	}
}

func TestMapV2_NoPrivateZones(t *testing.T) {
	m := office.DefaultMap()
	if m.IsPrivateZone("meeting-a") || m.IsPrivateZone("private-a") || m.IsPrivateZone("") {
		t.Fatal("v2 map must not report private zones")
	}
}

func TestMapV2_BoundsAndOOB(t *testing.T) {
	m := office.DefaultMap()
	x, y := tileCenter(0, 0)
	if !m.InBounds(x, y) {
		t.Fatal("border tile should still be in bounds")
	}
	if m.InBounds(-1, 0) || m.InBounds(1e9, 1e9) {
		t.Fatal("out of bounds should fail")
	}
	id, zt := m.ZoneAt(-1, 48)
	if zt != office.ZoneWall || id != "" {
		t.Fatalf("OOB should be ZoneWall, got %q %s", id, zt)
	}
	if m.IsWalkable(-1, 48) {
		t.Fatal("OOB must not be walkable")
	}
	// Legacy world size must not be assumed walkable.
	if m.IsWalkable(1280, 480) {
		t.Fatal("legacy 1280x960 coords must be OOB on v2")
	}
}

func TestMapV2_WallOutsideMeetingIsZoneWall(t *testing.T) {
	m := office.DefaultMap()
	x, y := tileCenter(0, 0)
	id, zt := m.ZoneAt(x, y)
	if zt != office.ZoneWall || id != "" {
		t.Fatalf("outer wall should be ZoneWall, got %q %s", id, zt)
	}
}

func TestSpawnXY_MatchSpawnWorld(t *testing.T) {
	x, y := office.DefaultMap().SpawnWorld()
	if office.SpawnX != x || office.SpawnY != y {
		t.Fatalf("SpawnX/Y=(%v,%v), SpawnWorld=(%v,%v)", office.SpawnX, office.SpawnY, x, y)
	}
}
