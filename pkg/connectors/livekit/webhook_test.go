package livekit

import (
	"context"
	"encoding/json"
	"testing"
)

func TestHandleWebhook_RoomStarted_CallsHook(t *testing.T) {
	called := false
	var gotEvent, gotRoom string

	c := NewWithHook(nil, func(ctx context.Context, eventType, roomName string) {
		called = true
		gotEvent = eventType
		gotRoom = roomName
	})

	payload, _ := json.Marshal(map[string]any{
		"event": "room_started",
		"room":  map[string]any{"name": "mon-salon"},
	})

	if err := c.HandleWebhook(context.Background(), "room_started", payload); err != nil {
		t.Fatalf("HandleWebhook erreur inattendue: %v", err)
	}
	if !called {
		t.Fatal("OnRoomEvent non appelé pour room_started")
	}
	if gotEvent != "room_started" {
		t.Errorf("eventType attendu room_started, obtenu %q", gotEvent)
	}
	if gotRoom != "mon-salon" {
		t.Errorf("roomName attendu mon-salon, obtenu %q", gotRoom)
	}
}

func TestHandleWebhook_ParticipantJoined_CallsHook(t *testing.T) {
	called := false
	c := NewWithHook(nil, func(_ context.Context, eventType, roomName string) {
		if eventType == "participant_joined" {
			called = true
		}
	})

	payload, _ := json.Marshal(map[string]any{
		"event":       "participant_joined",
		"room":        map[string]any{"name": "salle-42"},
		"participant": map[string]any{"identity": "user1"},
	})

	if err := c.HandleWebhook(context.Background(), "participant_joined", payload); err != nil {
		t.Fatalf("HandleWebhook erreur: %v", err)
	}
	if !called {
		t.Fatal("OnRoomEvent non appelé pour participant_joined")
	}
}

func TestHandleWebhook_UnknownEvent_NoHook(t *testing.T) {
	called := false
	c := NewWithHook(nil, func(_ context.Context, _, _ string) { called = true })

	payload, _ := json.Marshal(map[string]any{
		"event": "egress_started",
		"room":  map[string]any{"name": "salle"},
	})

	if err := c.HandleWebhook(context.Background(), "egress_started", payload); err != nil {
		t.Fatalf("HandleWebhook erreur: %v", err)
	}
	if called {
		t.Fatal("OnRoomEvent appelé pour un event inconnu (ne doit pas l'être)")
	}
}

func TestHandleWebhook_NilHook_NoError(t *testing.T) {
	c := New(nil)
	payload, _ := json.Marshal(map[string]any{
		"event": "room_started",
		"room":  map[string]any{"name": "salle"},
	})
	if err := c.HandleWebhook(context.Background(), "room_started", payload); err != nil {
		t.Fatalf("HandleWebhook avec hook nil erreur: %v", err)
	}
}
