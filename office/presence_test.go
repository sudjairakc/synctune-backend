package office_test

import (
	"math"
	"testing"
	"time"

	"github.com/synctune/backend/office"
)

func presenceInput(prevX, prevY, x, y float64, dt time.Duration, maxSpeed float64) office.SanityInput {
	now := time.Unix(1_700_000_000, 0)
	return office.SanityInput{
		PrevX:           prevX,
		PrevY:           prevY,
		PrevTime:        now.Add(-dt),
		X:               x,
		Y:               y,
		Now:             now,
		MaxSpeedPxS:     maxSpeed,
		MinInterval:     66 * time.Millisecond,
		CanEnterPrivate: func(string) bool { return true },
	}
}

func TestAcceptPresence_SpeedClampAntiTeleport(t *testing.T) {
	m := office.DefaultMap()
	sx, sy := m.SpawnWorld()
	targetX, targetY := sx+200, sy
	in := presenceInput(sx, sy, targetX, targetY, 100*time.Millisecond, 240)
	got := office.AcceptPresence(m, in)

	dx, dy := targetX-sx, targetY-sy
	dist := math.Hypot(dx, dy)
	maxDist := 240 * 0.1
	wantX := sx + dx/dist*maxDist
	wantY := sy + dy/dist*maxDist

	if !got.Rejected {
		t.Fatal("teleport/speed clamp should set Rejected=true")
	}
	if math.Abs(got.X-wantX) > 1e-6 || math.Abs(got.Y-wantY) > 1e-6 {
		t.Fatalf("clamped pos = (%v,%v), want (%v,%v)", got.X, got.Y, wantX, wantY)
	}
}

func TestAcceptPresence_WallReverts(t *testing.T) {
	m := office.DefaultMap()
	// Open-office aisle (tx=2,ty=9) into outer wall (tx=0).
	prevX, prevY := 2*float64(office.TileSize)+float64(office.TileSize)/2, 9*float64(office.TileSize)+float64(office.TileSize)/2
	wallX, wallY := float64(office.TileSize)/2, prevY
	in := presenceInput(prevX, prevY, wallX, wallY, 500*time.Millisecond, 240)
	got := office.AcceptPresence(m, in)

	if !got.Rejected {
		t.Fatal("wall hit should Rejected=true")
	}
	if got.X != prevX || got.Y != prevY {
		t.Fatalf("should revert to prev, got (%v,%v)", got.X, got.Y)
	}
	if got.ZoneType != office.ZoneOpen {
		t.Fatalf("prev zone should remain open, got %s", got.ZoneType)
	}
}

func TestAcceptPresence_PrivateDenyReverts(t *testing.T) {
	t.Skip("map v2 has no private zones; private entry path deferred to Task 3")
}

func TestAcceptPresence_PrivateNilCallbackDenies(t *testing.T) {
	t.Skip("map v2 has no private zones; private entry path deferred to Task 3")
}

func TestAcceptPresence_PrivateAllowHappy(t *testing.T) {
	t.Skip("map v2 has no private zones; private entry path deferred to Task 3")
}

func TestAcceptPresence_OOBClampedThenWallRevert(t *testing.T) {
	m := office.DefaultMap()
	// Start near west wall so OOB clamp lands on the wall tile (not mid-floor after speed clamp).
	ts := float64(office.TileSize)
	prevX, prevY := 1*ts+ts/2, 9*ts+ts/2
	in := presenceInput(prevX, prevY, -40, prevY, 500*time.Millisecond, 240)
	got := office.AcceptPresence(m, in)

	if !got.Rejected {
		t.Fatal("OOB→wall should Rejected=true")
	}
	if got.X != prevX || got.Y != prevY {
		t.Fatalf("should revert to prev, got (%v,%v)", got.X, got.Y)
	}
}

func TestAcceptPresence_MeetingDetect(t *testing.T) {
	m := office.DefaultMap()
	// Enter meeting-a via door (8,8): open floor (8,9) → interior floor (8,7).
	ts := float64(office.TileSize)
	prevX, prevY := 8*ts+ts/2, 9*ts+ts/2
	destX, destY := 8*ts+ts/2, 7*ts+ts/2
	in := presenceInput(prevX, prevY, destX, destY, 500*time.Millisecond, 240)
	got := office.AcceptPresence(m, in)

	if got.Rejected {
		t.Fatal("legal meeting entry should not be rejected")
	}
	if got.ZoneID != "meeting-a" || got.ZoneType != office.ZoneMeeting {
		t.Fatalf("want meeting-a/meeting, got %q %s", got.ZoneID, got.ZoneType)
	}
	if got.X != destX || got.Y != destY {
		t.Fatalf("pos = (%v,%v)", got.X, got.Y)
	}
}

func TestAcceptPresence_SpeedClamp(t *testing.T) {
	m := office.DefaultMap()
	sx, sy := m.SpawnWorld()
	// Pure +X move: 100px in 100ms @ 240px/s → clamp to +24px.
	in := presenceInput(sx, sy, sx+100, sy, 100*time.Millisecond, 240)
	got := office.AcceptPresence(m, in)

	if !got.Rejected {
		t.Fatal("speed clamp should Rejected=true")
	}
	if math.Abs(got.X-(sx+24)) > 1e-9 || got.Y != sy {
		t.Fatalf("got (%v,%v), want (%v,%v)", got.X, got.Y, sx+24, sy)
	}
}

func TestAcceptPresence_IntervalTooSoonIgnored(t *testing.T) {
	m := office.DefaultMap()
	sx, sy := m.SpawnWorld()
	in := presenceInput(sx, sy, sx+60, sy, 10*time.Millisecond, 240)
	got := office.AcceptPresence(m, in)

	// Resolution: ignore update — keep prev, Rejected=false (skip broadcast).
	if got.Rejected {
		t.Fatal("interval skip must Rejected=false")
	}
	if got.X != sx || got.Y != sy {
		t.Fatalf("should keep prev, got (%v,%v)", got.X, got.Y)
	}
}
