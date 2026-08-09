package office

import "math"

// TileSize is the pixel size of one map tile (matches map_v2.json).
const TileSize = 32

// SpawnX / SpawnY are the world-pixel spawn point (center of the spawn tile).
// Derived from DefaultMap — do not hardcode map dimensions here.
var SpawnX, SpawnY float64

func init() {
	m := loadMapV2()
	defaultMap = m
	SpawnX, SpawnY = m.SpawnWorld()
}

var defaultMap *OfficeMap

type TileKind string

const (
	TileFloor TileKind = "floor"
	TileWall  TileKind = "wall"
	TileDesk  TileKind = "desk"
	TileChair TileKind = "chair"
	TileDoor  TileKind = "door"
)

type ZoneType string

const (
	ZoneOpen    ZoneType = "open"
	ZoneWall    ZoneType = "wall"
	ZoneMeeting ZoneType = "meeting"
	ZonePrivate ZoneType = "private" // kept for subsystem compatibility; v2 has none
)

type zoneRect struct {
	id   string
	typ  ZoneType
	minX float64
	minY float64
	maxX float64
	maxY float64
}

// OfficeMap is the fixed SyncTune 2.0 office template loaded from map_v2.json.
type OfficeMap struct {
	tileSize int
	tilesW   int
	tilesH   int
	width    float64
	height   float64
	tiles    [][]TileKind
	zones    []zoneRect
	spawnTX  int
	spawnTY  int
}

// DefaultMap returns the shared office map template (map_v2).
func DefaultMap() *OfficeMap {
	return defaultMap
}

// InBounds reports whether world-pixel coordinates lie inside the map.
func (m *OfficeMap) InBounds(x, y float64) bool {
	return x >= 0 && x < m.width && y >= 0 && y < m.height
}

// TileAt returns the tile kind at world-pixel coordinates.
// Out of bounds is treated as wall.
func (m *OfficeMap) TileAt(x, y float64) TileKind {
	if !m.InBounds(x, y) {
		return TileWall
	}
	tx, ty := m.tileCoords(x, y)
	return m.tiles[ty][tx]
}

// IsWalkable reports whether coordinates are on an allow-listed walkable tile.
func (m *OfficeMap) IsWalkable(x, y float64) bool {
	if !m.InBounds(x, y) {
		return false
	}
	k := m.TileAt(x, y)
	return k == TileFloor || k == TileChair || k == TileDoor
}

// ZoneAt returns the zone id and type for world-pixel coordinates.
// Meeting bounds win first; solid walls outside meetings are ZoneWall; else open.
func (m *OfficeMap) ZoneAt(x, y float64) (zoneID string, ztype ZoneType) {
	if !m.InBounds(x, y) {
		return "", ZoneWall
	}
	for _, z := range m.zones {
		if x >= z.minX && x < z.maxX && y >= z.minY && y < z.maxY {
			return z.id, z.typ
		}
	}
	if m.TileAt(x, y) == TileWall {
		return "", ZoneWall
	}
	return "", ZoneOpen
}

// SpawnWorld returns the world-pixel center of the spawn tile.
func (m *OfficeMap) SpawnWorld() (x, y float64) {
	ts := float64(m.tileSize)
	return float64(m.spawnTX)*ts + ts/2, float64(m.spawnTY)*ts + ts/2
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

func (m *OfficeMap) tileCoords(x, y float64) (tx, ty int) {
	ts := float64(m.tileSize)
	return int(math.Floor(x / ts)), int(math.Floor(y / ts))
}
