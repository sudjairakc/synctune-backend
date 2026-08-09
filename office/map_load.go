package office

import (
	_ "embed"
	"encoding/json"
	"fmt"
)

//go:embed map_v2.json
var mapV2JSON []byte

type mapV2File struct {
	SchemaVersion int          `json:"schemaVersion"`
	MapID         string       `json:"mapId"`
	TileSize      int          `json:"tileSize"`
	TilesW        int          `json:"tilesW"`
	TilesH        int          `json:"tilesH"`
	Tiles         [][]TileKind `json:"tiles"`
	Zones         []mapV2Zone  `json:"zones"`
	Spawn         mapV2Spawn   `json:"spawn"`
}

type mapV2Zone struct {
	ID     string      `json:"id"`
	Type   ZoneType    `json:"type"`
	Bounds mapV2Bounds `json:"bounds"`
}

type mapV2Bounds struct {
	TX int `json:"tx"`
	TY int `json:"ty"`
	TW int `json:"tw"`
	TH int `json:"th"`
}

type mapV2Spawn struct {
	TX int `json:"tx"`
	TY int `json:"ty"`
}

func loadMapV2() *OfficeMap {
	var raw mapV2File
	if err := json.Unmarshal(mapV2JSON, &raw); err != nil {
		panic(fmt.Sprintf("office: map_v2.json: %v", err))
	}
	if raw.TileSize <= 0 {
		panic("office: map_v2.json: tileSize must be > 0")
	}
	if raw.TilesW <= 0 || raw.TilesH <= 0 {
		panic("office: map_v2.json: tilesW/tilesH must be > 0")
	}
	if len(raw.Tiles) != raw.TilesH {
		panic(fmt.Sprintf("office: map_v2.json: tiles rows=%d want tilesH=%d", len(raw.Tiles), raw.TilesH))
	}
	for ty, row := range raw.Tiles {
		if len(row) != raw.TilesW {
			panic(fmt.Sprintf("office: map_v2.json: tiles[%d] len=%d want tilesW=%d", ty, len(row), raw.TilesW))
		}
	}
	if raw.Spawn.TX < 0 || raw.Spawn.TX >= raw.TilesW || raw.Spawn.TY < 0 || raw.Spawn.TY >= raw.TilesH {
		panic("office: map_v2.json: spawn out of bounds")
	}

	ts := float64(raw.TileSize)
	zones := make([]zoneRect, 0, len(raw.Zones))
	for _, z := range raw.Zones {
		b := z.Bounds
		if b.TW <= 0 || b.TH <= 0 {
			panic(fmt.Sprintf("office: map_v2.json: zone %q empty bounds", z.ID))
		}
		if b.TX < 0 || b.TY < 0 || b.TX+b.TW > raw.TilesW || b.TY+b.TH > raw.TilesH {
			panic(fmt.Sprintf("office: map_v2.json: zone %q bounds out of range", z.ID))
		}
		zones = append(zones, zoneRect{
			id:   z.ID,
			typ:  z.Type,
			minX: float64(b.TX) * ts,
			minY: float64(b.TY) * ts,
			maxX: float64(b.TX+b.TW) * ts,
			maxY: float64(b.TY+b.TH) * ts,
		})
	}

	return &OfficeMap{
		tileSize: raw.TileSize,
		tilesW:   raw.TilesW,
		tilesH:   raw.TilesH,
		width:    float64(raw.TilesW) * ts,
		height:   float64(raw.TilesH) * ts,
		tiles:    raw.Tiles,
		zones:    zones,
		spawnTX:  raw.Spawn.TX,
		spawnTY:  raw.Spawn.TY,
	}
}
