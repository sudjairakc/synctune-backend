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
	// Far jump in 100ms with MaxSpeed 240 → max step 24px along vector.
	in := presenceInput(640, 480, 1088, 256, 100*time.Millisecond, 240)
	got := office.AcceptPresence(m, in)

	dx, dy := 1088-640, 256-480
	dist := math.Hypot(float64(dx), float64(dy))
	maxDist := 240 * 0.1
	wantX := 640 + float64(dx)/dist*maxDist
	wantY := 480 + float64(dy)/dist*maxDist

	if !got.Rejected {
		t.Fatal("teleport/speed clamp should set Rejected=true")
	}
	if math.Abs(got.X-wantX) > 1e-6 || math.Abs(got.Y-wantY) > 1e-6 {
		t.Fatalf("clamped pos = (%v,%v), want (%v,%v)", got.X, got.Y, wantX, wantY)
	}
}

func TestAcceptPresence_WallReverts(t *testing.T) {
	m := office.DefaultMap()
	// From interior (48,48) into border wall (16,48); 200ms @ 240px/s allows 48px step.
	in := presenceInput(48, 48, 16, 48, 200*time.Millisecond, 240)
	got := office.AcceptPresence(m, in)

	if !got.Rejected {
		t.Fatal("wall hit should Rejected=true")
	}
	if got.X != 48 || got.Y != 48 {
		t.Fatalf("should revert to prev, got (%v,%v)", got.X, got.Y)
	}
	if got.ZoneType != office.ZoneOpen {
		t.Fatalf("prev zone should remain open, got %s", got.ZoneType)
	}
}

func TestAcceptPresence_PrivateDenyReverts(t *testing.T) {
	m := office.DefaultMap()
	// Approach private-a from open floor just outside (260,200) → (200,200).
	in := presenceInput(260, 200, 200, 200, 500*time.Millisecond, 240)
	in.CanEnterPrivate = func(zoneID string) bool { return false }

	got := office.AcceptPresence(m, in)
	if !got.Rejected {
		t.Fatal("private deny should Rejected=true")
	}
	if got.X != 260 || got.Y != 200 {
		t.Fatalf("should revert to prev, got (%v,%v)", got.X, got.Y)
	}
	if got.ZoneType == office.ZonePrivate {
		t.Fatal("must not accept private zone when denied")
	}
}

func TestAcceptPresence_PrivateNilCallbackDenies(t *testing.T) {
	m := office.DefaultMap()
	in := presenceInput(260, 200, 200, 200, 500*time.Millisecond, 240)
	in.CanEnterPrivate = nil

	got := office.AcceptPresence(m, in)
	if !got.Rejected || got.X != 260 || got.Y != 200 {
		t.Fatalf("nil CanEnterPrivate must deny private, got %+v", got)
	}
}

func TestAcceptPresence_MeetingDetect(t *testing.T) {
	m := office.DefaultMap()
	// Enter meeting-a from adjacent open floor.
	in := presenceInput(900, 256, 980, 256, 500*time.Millisecond, 240)
	got := office.AcceptPresence(m, in)

	if got.Rejected {
		t.Fatal("legal meeting entry should not be rejected")
	}
	if got.ZoneID != "meeting-a" || got.ZoneType != office.ZoneMeeting {
		t.Fatalf("want meeting-a/meeting, got %q %s", got.ZoneID, got.ZoneType)
	}
	if got.X != 980 || got.Y != 256 {
		t.Fatalf("pos = (%v,%v)", got.X, got.Y)
	}
}

func TestAcceptPresence_SpeedClamp(t *testing.T) {
	m := office.DefaultMap()
	// Pure +X move: 100px in 100ms @ 240px/s → clamp to +24px.
	in := presenceInput(640, 480, 740, 480, 100*time.Millisecond, 240)
	got := office.AcceptPresence(m, in)

	if !got.Rejected {
		t.Fatal("speed clamp should Rejected=true")
	}
	if math.Abs(got.X-664) > 1e-9 || got.Y != 480 {
		t.Fatalf("got (%v,%v), want (664,480)", got.X, got.Y)
	}
}

func TestAcceptPresence_IntervalTooSoonIgnored(t *testing.T) {
	m := office.DefaultMap()
	in := presenceInput(640, 480, 700, 480, 10*time.Millisecond, 240)
	got := office.AcceptPresence(m, in)

	// Resolution: ignore update — keep prev, Rejected=false (skip broadcast).
	if got.Rejected {
		t.Fatal("interval skip must Rejected=false")
	}
	if got.X != 640 || got.Y != 480 {
		t.Fatalf("should keep prev, got (%v,%v)", got.X, got.Y)
	}
}
