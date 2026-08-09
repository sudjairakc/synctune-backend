package office

import "math"

const TileSize = 32
const MapTilesW = 40
const MapTilesH = 30 // world = 1280 x 960
const SpawnX = 640.0
const SpawnY = 480.0

// Zone rects (min inclusive, max exclusive), world px:
// meeting-a:  x=960..1216  y=128..384
// private-a:  x=64..256    y=128..320
// private-b:  x=64..256    y=640..832
// Outer border tiles (x=0, x=39, y=0, y=29) = walls

type ZoneType string

const (
	ZoneOpen    ZoneType = "open"
	ZoneWall    ZoneType = "wall"
	ZoneMeeting ZoneType = "meeting"
	ZonePrivate ZoneType = "private"
)

type zoneRect struct {
	id   string
	typ  ZoneType
	minX float64
	minY float64
	maxX float64
	maxY float64
}

// OfficeMap is the fixed SyncTune 2.0 office template.
type OfficeMap struct {
	width  float64
	height float64
	zones  []zoneRect
}

// DefaultMap returns the shared office map template.
func DefaultMap() *OfficeMap {
	return &OfficeMap{
		width:  float64(MapTilesW * TileSize),
		height: float64(MapTilesH * TileSize),
		zones: []zoneRect{
			{id: "meeting-a", typ: ZoneMeeting, minX: 960, minY: 128, maxX: 1216, maxY: 384},
			{id: "private-a", typ: ZonePrivate, minX: 64, minY: 128, maxX: 256, maxY: 320},
			{id: "private-b", typ: ZonePrivate, minX: 64, minY: 640, maxX: 256, maxY: 832},
		},
	}
}

// InBounds reports whether world-pixel coordinates lie inside the map.
func (m *OfficeMap) InBounds(x, y float64) bool {
	return x >= 0 && x < m.width && y >= 0 && y < m.height
}

// IsWalkable reports whether coordinates are on walkable floor (in bounds and not a wall).
func (m *OfficeMap) IsWalkable(x, y float64) bool {
	if !m.InBounds(x, y) {
		return false
	}
	_, zt := m.ZoneAt(x, y)
	return zt != ZoneWall
}

// ZoneAt returns the zone id and type for world-pixel coordinates.
func (m *OfficeMap) ZoneAt(x, y float64) (zoneID string, ztype ZoneType) {
	if !m.InBounds(x, y) {
		return "", ZoneWall
	}
	tx, ty := tileCoords(x, y)
	if isBorderWall(tx, ty) {
		return "", ZoneWall
	}
	for _, z := range m.zones {
		if x >= z.minX && x < z.maxX && y >= z.minY && y < z.maxY {
			return z.id, z.typ
		}
	}
	return "", ZoneOpen
}

// IsPrivateZone reports whether zoneID is a private zone on this map.
func (m *OfficeMap) IsPrivateZone(zoneID string) bool {
	if zoneID == "" {
		return false
	}
	for _, z := range m.zones {
		if z.id == zoneID && z.typ == ZonePrivate {
			return true
		}
	}
	return false
}

func tileCoords(x, y float64) (tx, ty int) {
	return int(math.Floor(x / TileSize)), int(math.Floor(y / TileSize))
}

func isBorderWall(tx, ty int) bool {
	return tx == 0 || tx == MapTilesW-1 || ty == 0 || ty == MapTilesH-1
}
