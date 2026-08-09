package office

import (
	"math"
	"time"
)

// SanityInput is the client intent plus last-accepted presence for validation.
type SanityInput struct {
	PrevX, PrevY float64
	PrevTime     time.Time
	X, Y         float64
	Now          time.Time
	MaxSpeedPxS  float64
	MinInterval  time.Duration
	// CanEnterPrivate: true if connection may occupy derived private zone.
	// nil denies all private zones.
	CanEnterPrivate func(zoneID string) bool
}

// SanityResult is the server-accepted position and derived zone.
// Rejected is true when the client intent was clamped or reverted
// (bounds / speed / wall / private). Interval-too-soon ignores keep
// Rejected=false so callers can skip broadcast.
type SanityResult struct {
	X, Y     float64
	ZoneID   string
	ZoneType ZoneType
	Rejected bool
}

// AcceptPresence validates a presence update against map geometry, speed,
// and private-zone access. Zone is always derived server-side from the
// accepted point.
func AcceptPresence(m *OfficeMap, in SanityInput) SanityResult {
	if in.Now.Sub(in.PrevTime) < in.MinInterval {
		return resultAt(m, in.PrevX, in.PrevY, false)
	}

	x, y := in.X, in.Y
	rejected := false

	if x < 0 {
		x = 0
		rejected = true
	} else if x >= m.width {
		x = math.Nextafter(m.width, 0)
		rejected = true
	}
	if y < 0 {
		y = 0
		rejected = true
	} else if y >= m.height {
		y = math.Nextafter(m.height, 0)
		rejected = true
	}

	dt := in.Now.Sub(in.PrevTime).Seconds()
	if dt < 0 {
		dt = 0
	}
	dx := x - in.PrevX
	dy := y - in.PrevY
	dist := math.Hypot(dx, dy)
	maxDist := in.MaxSpeedPxS * dt
	if dist > maxDist {
		if dist > 0 {
			scale := maxDist / dist
			x = in.PrevX + dx*scale
			y = in.PrevY + dy*scale
		} else {
			x, y = in.PrevX, in.PrevY
		}
		rejected = true
	}

	if !m.IsWalkable(x, y) {
		return resultAt(m, in.PrevX, in.PrevY, true)
	}

	zoneID, zt := m.ZoneAt(x, y)
	if zt == ZonePrivate {
		allowed := in.CanEnterPrivate != nil && in.CanEnterPrivate(zoneID)
		if !allowed {
			return resultAt(m, in.PrevX, in.PrevY, true)
		}
	}

	return SanityResult{X: x, Y: y, ZoneID: zoneID, ZoneType: zt, Rejected: rejected}
}

func resultAt(m *OfficeMap, x, y float64, rejected bool) SanityResult {
	id, zt := m.ZoneAt(x, y)
	return SanityResult{X: x, Y: y, ZoneID: id, ZoneType: zt, Rejected: rejected}
}
