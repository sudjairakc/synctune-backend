package office

import "fmt"

// MeetingCenter returns the world-pixel center of a meeting zone's bounds.
// Panics if zoneID is missing or not a meeting zone.
func MeetingCenter(m *OfficeMap, zoneID string) (x, y float64) {
	for _, z := range m.zones {
		if z.id == zoneID && z.typ == ZoneMeeting {
			return (z.minX + z.maxX) / 2, (z.minY + z.maxY) / 2
		}
	}
	panic(fmt.Sprintf("office: MeetingCenter: unknown meeting zone %q", zoneID))
}

// FindTileCenter returns the world-pixel center of the first tile of kind (row-major).
func FindTileCenter(m *OfficeMap, kind TileKind) (x, y float64, ok bool) {
	ts := float64(m.tileSize)
	for ty := 0; ty < m.tilesH; ty++ {
		for tx := 0; tx < m.tilesW; tx++ {
			if m.tiles[ty][tx] == kind {
				return float64(tx)*ts + ts/2, float64(ty)*ts + ts/2, true
			}
		}
	}
	return 0, 0, false
}

// MustFindTile is FindTileCenter that panics if no tile of kind exists.
func MustFindTile(m *OfficeMap, kind TileKind) (x, y float64) {
	x, y, ok := FindTileCenter(m, kind)
	if !ok {
		panic(fmt.Sprintf("office: MustFindTile: no %q tile", kind))
	}
	return x, y
}

// DeskProbeOutside returns a walkable neighbor of a desk and that desk's center.
func DeskProbeOutside(m *OfficeMap) (outsideX, outsideY, deskX, deskY float64) {
	ts := float64(m.tileSize)
	deltas := [][2]int{{0, 1}, {0, -1}, {1, 0}, {-1, 0}}
	for ty := 0; ty < m.tilesH; ty++ {
		for tx := 0; tx < m.tilesW; tx++ {
			if m.tiles[ty][tx] != TileDesk {
				continue
			}
			deskX = float64(tx)*ts + ts/2
			deskY = float64(ty)*ts + ts/2
			for _, d := range deltas {
				nx, ny := tx+d[0], ty+d[1]
				if nx < 0 || ny < 0 || nx >= m.tilesW || ny >= m.tilesH {
					continue
				}
				ox := float64(nx)*ts + ts/2
				oy := float64(ny)*ts + ts/2
				if m.IsWalkable(ox, oy) {
					return ox, oy, deskX, deskY
				}
			}
		}
	}
	panic("office: DeskProbeOutside: no desk with walkable neighbor")
}

// MeetingEntryPath returns a walkable open-floor point just south of a meeting
// door and a walkable interior floor point for that meeting (map_v2 doors).
func MeetingEntryPath(m *OfficeMap, zoneID string) (outsideX, outsideY, insideX, insideY float64) {
	ts := float64(m.tileSize)
	var doorTX int
	switch zoneID {
	case "meeting-a":
		doorTX = 8
	case "meeting-b":
		doorTX = 12
	default:
		panic(fmt.Sprintf("office: MeetingEntryPath: unsupported zone %q", zoneID))
	}
	outsideX = float64(doorTX)*ts + ts/2
	outsideY = 9*ts + ts/2
	insideX = outsideX
	insideY = 7*ts + ts/2
	if id, zt := m.ZoneAt(insideX, insideY); zt != ZoneMeeting || id != zoneID {
		panic(fmt.Sprintf("office: MeetingEntryPath: interior not in %s", zoneID))
	}
	if !m.IsWalkable(outsideX, outsideY) || !m.IsWalkable(insideX, insideY) {
		panic("office: MeetingEntryPath: path not walkable")
	}
	return outsideX, outsideY, insideX, insideY
}

// SyntheticPrivateMap returns a tiny in-memory map with one private zone for unit tests.
func SyntheticPrivateMap() *OfficeMap {
	const ts = 32
	const w, h = 5, 5
	tiles := make([][]TileKind, h)
	for ty := 0; ty < h; ty++ {
		tiles[ty] = make([]TileKind, w)
		for tx := 0; tx < w; tx++ {
			if tx == 0 || ty == 0 || tx == w-1 || ty == h-1 {
				tiles[ty][tx] = TileWall
			} else {
				tiles[ty][tx] = TileFloor
			}
		}
	}
	return &OfficeMap{
		tileSize: ts,
		tilesW:   w,
		tilesH:   h,
		width:    float64(w) * ts,
		height:   float64(h) * ts,
		tiles:    tiles,
		zones: []zoneRect{{
			id:   "private-a",
			typ:  ZonePrivate,
			minX: 2 * ts,
			minY: 2 * ts,
			maxX: 3 * ts,
			maxY: 3 * ts,
		}},
		spawnTX: 1,
		spawnTY: 1,
	}
}
