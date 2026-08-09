package office_test

import (
	"testing"

	"github.com/synctune/backend/office"
)

func TestDeriveActiveVoiceGroup_BubbleWinsOverMeeting(t *testing.T) {
	got := office.DeriveActiveVoiceGroup("room1", "b1", "meeting-a", office.ZoneMeeting)
	want := "st:room1:bubble:b1"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestDeriveActiveVoiceGroup_OpenZoneEmpty(t *testing.T) {
	got := office.DeriveActiveVoiceGroup("room1", "", "some-id", office.ZoneOpen)
	if got != "" {
		t.Fatalf("open zone should yield empty, got %q", got)
	}
}

func TestDeriveActiveVoiceGroup_EmptyBubbleFallsThroughToMeeting(t *testing.T) {
	got := office.DeriveActiveVoiceGroup("room1", "", "meeting-a", office.ZoneMeeting)
	want := "st:room1:meet:meeting-a"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestDeriveActiveVoiceGroup_BubbleOnly(t *testing.T) {
	got := office.DeriveActiveVoiceGroup("r", "bubble-x", "", office.ZoneOpen)
	want := "st:r:bubble:bubble-x"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestDeriveActiveVoiceGroup_MeetingEmptyZoneID(t *testing.T) {
	got := office.DeriveActiveVoiceGroup("room1", "", "", office.ZoneMeeting)
	if got != "" {
		t.Fatalf("meeting with empty zoneID should yield empty, got %q", got)
	}
}

func TestDeriveActiveVoiceGroup_PrivateZoneEmpty(t *testing.T) {
	got := office.DeriveActiveVoiceGroup("room1", "", "private-a", office.ZonePrivate)
	if got != "" {
		t.Fatalf("private zone should yield empty, got %q", got)
	}
}

func TestDeriveActiveVoiceGroup_AllEmpty(t *testing.T) {
	got := office.DeriveActiveVoiceGroup("room1", "", "", office.ZoneOpen)
	if got != "" {
		t.Fatalf("all empty should yield empty, got %q", got)
	}
}
