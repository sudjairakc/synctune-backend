package office

// DeriveActiveVoiceGroup returns the LiveKit group id for the current voice
// context, or "" when not in bubble or meeting voice.
//
// Invariant:
//
//	bubbleID != ""  →  st:{room}:bubble:{bubbleID}
//	zoneType meeting && zoneID != ""  →  st:{room}:meet:{zoneID}
//	else ""
func DeriveActiveVoiceGroup(roomID, bubbleID, zoneID string, zoneType ZoneType) string {
	if bubbleID != "" {
		return "st:" + roomID + ":bubble:" + bubbleID
	}
	if zoneType == ZoneMeeting && zoneID != "" {
		return "st:" + roomID + ":meet:" + zoneID
	}
	return ""
}
