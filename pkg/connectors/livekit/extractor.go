// CLAUDE:SUMMARY Extracteur event_id pour webhooks LiveKit : parse {event, room.name} → eventID unique.
package livekit

import (
	"encoding/json"
	"errors"
	"fmt"
)

// ExtractWebhookEventID parse un payload webhook LiveKit et retourne
// (event_id, event_type, error) pour le receiver générique.
//
// Format LiveKit : {"event":"room_started","room":{"name":"slug"},...}
// event_id = "<event>:<room.name>" pour garantir unicité par event+room.
func ExtractWebhookEventID(payload []byte) (eventID, eventType string, err error) {
	var p webhookPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return "", "", fmt.Errorf("livekit.ExtractWebhookEventID: parse: %w", err)
	}
	if p.Event == "" {
		return "", "", errors.New("livekit.ExtractWebhookEventID: champ event absent")
	}
	if p.Room.Name == "" {
		// Events sans room (ex: egress events) : utiliser event seul comme ID.
		return p.Event + ":_", p.Event, nil
	}
	return p.Event + ":" + p.Room.Name, p.Event, nil
}
