package livekit

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestRoomAdminToken_PorteRoomAdmin vérifie que le jeton admin porte roomAdmin=true
// et ne porte PAS roomJoin/CanPublish/CanSubscribe.
func TestRoomAdminToken_PorteRoomAdmin(t *testing.T) {
	const key, secret = "APIadmin", "s3cr3t-admin"
	now := time.Unix(1_700_000_000, 0)
	tok, err := roomAdminTokenAt(key, secret, "salle-1", time.Minute, now)
	if err != nil {
		t.Fatalf("RoomAdminToken: %v", err)
	}
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		t.Fatalf("JWT mal formé: %d segments", len(parts))
	}
	cb, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode claims: %v", err)
	}
	var c claims
	if err := json.Unmarshal(cb, &c); err != nil {
		t.Fatalf("unmarshal claims: %v", err)
	}
	if !c.Video.RoomAdmin {
		t.Fatal("jeton admin: roomAdmin doit être true")
	}
	if c.Video.RoomJoin {
		t.Fatal("jeton admin: roomJoin doit être false (absent)")
	}
	if c.Video.CanPublish {
		t.Fatal("jeton admin: canPublish doit être false (absent)")
	}
	if c.Video.CanSubscribe {
		t.Fatal("jeton admin: canSubscribe doit être false (absent)")
	}
	if c.Video.Room != "salle-1" {
		t.Fatalf("jeton admin: room=%s", c.Video.Room)
	}
}

// TestRoomToken_NePasPorterRoomAdmin vérifie que le jeton participant (RoomToken) ne porte
// PAS roomAdmin (régression : on s'assure que L2a n'a pas été altéré).
func TestRoomToken_NePasPorterRoomAdmin(t *testing.T) {
	const key, secret = "APIpart", "s3cr3t-part"
	now := time.Unix(1_700_000_000, 0)
	tok, err := roomTokenAt(key, secret, "salle-1", "user-42", time.Hour, now)
	if err != nil {
		t.Fatalf("RoomToken: %v", err)
	}
	parts := strings.Split(tok, ".")
	cb, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode claims: %v", err)
	}
	var c claims
	if err := json.Unmarshal(cb, &c); err != nil {
		t.Fatalf("unmarshal claims: %v", err)
	}
	if c.Video.RoomAdmin {
		t.Fatal("jeton participant: roomAdmin doit être false")
	}
	if !c.Video.RoomJoin || !c.Video.CanPublish || !c.Video.CanSubscribe {
		t.Fatalf("jeton participant: grants attendus absents: %+v", c.Video)
	}
}

// ---- Tests du client Twirp via httptest.Server ----

// buildFakeRoomService construit un serveur HTTP qui simule le RoomService LiveKit.
// participants est la liste renvoyée par ListParticipants.
// Les appels sont consignés dans calls.
func buildFakeRoomService(t *testing.T, participants []participantInfo, calls *[]string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method := strings.TrimPrefix(r.URL.Path, "/twirp/livekit.RoomService/")
		*calls = append(*calls, method)
		w.Header().Set("Content-Type", "application/json")
		switch method {
		case "ListParticipants":
			resp := listParticipantsResp{Participants: participants}
			if err := json.NewEncoder(w).Encode(resp); err != nil {
				t.Errorf("fake ListParticipants encode: %v", err)
			}
		case "MutePublishedTrack", "RemoveParticipant", "DeleteRoom":
			w.Write([]byte("{}")) //nolint:errcheck
		default:
			http.Error(w, "méthode inconnue", http.StatusNotFound)
		}
	}))
}

// TestTwirpClient_MuteParticipant_ResolutionTrackSid vérifie que MuteParticipant
// fait d'abord ListParticipants pour résoudre le trackSid audio, puis appelle
// MutePublishedTrack avec le bon SID.
func TestTwirpClient_MuteParticipant_ResolutionTrackSid(t *testing.T) {
	participants := []participantInfo{
		{
			Identity: "user-42",
			Tracks: []trackInfo{
				{Sid: "TR_video_01", Type: "VIDEO"},
				{Sid: "TR_audio_01", Type: "AUDIO"},
			},
		},
	}
	var calls []string
	srv := buildFakeRoomService(t, participants, &calls)
	defer srv.Close()

	cl := newRoomClient(srv.URL, "fake-token")
	ctx := context.Background()

	// MuteParticipant doit d'abord lister, puis muter le bon trackSid AUDIO.
	if err := cl.muteParticipantViaList(ctx, "salle-1", "user-42"); err != nil {
		t.Fatalf("muteParticipantViaList: %v", err)
	}

	if len(calls) != 2 {
		t.Fatalf("attendu 2 appels (ListParticipants + MutePublishedTrack), obtenu %v", calls)
	}
	if calls[0] != "ListParticipants" {
		t.Errorf("premier appel attendu ListParticipants, obtenu %s", calls[0])
	}
	if calls[1] != "MutePublishedTrack" {
		t.Errorf("deuxième appel attendu MutePublishedTrack, obtenu %s", calls[1])
	}
}

// muteParticipantViaList est une version testable de la logique MuteParticipant
// (sans passer par le Connector et le Vault). Elle reproduit la même logique
// que Connector.MuteParticipant mais opère directement sur roomClient.
func (c *roomClient) muteParticipantViaList(ctx context.Context, room, identity string) error {
	participants, err := c.listParticipants(ctx, room)
	if err != nil {
		return err
	}
	for _, p := range participants {
		if p.Identity != identity {
			continue
		}
		for _, track := range p.Tracks {
			if track.Type == "AUDIO" {
				return c.mutePublishedTrack(ctx, room, identity, track.Sid, true)
			}
		}
		return nil // pas de piste audio : no-op
	}
	return nil // participant absent : no-op
}

// TestTwirpClient_RemoveParticipant_AppelEndpoint vérifie que RemoveParticipant
// appelle le bon endpoint.
func TestTwirpClient_RemoveParticipant_AppelEndpoint(t *testing.T) {
	var calls []string
	srv := buildFakeRoomService(t, nil, &calls)
	defer srv.Close()

	cl := newRoomClient(srv.URL, "fake-token")
	if err := cl.removeParticipant(context.Background(), "salle-1", "user-42"); err != nil {
		t.Fatalf("removeParticipant: %v", err)
	}
	if len(calls) != 1 || calls[0] != "RemoveParticipant" {
		t.Fatalf("appel attendu RemoveParticipant, obtenu %v", calls)
	}
}

// TestTwirpClient_EndRoom_AppelEndpoint vérifie que deleteRoom appelle DeleteRoom.
func TestTwirpClient_EndRoom_AppelEndpoint(t *testing.T) {
	var calls []string
	srv := buildFakeRoomService(t, nil, &calls)
	defer srv.Close()

	cl := newRoomClient(srv.URL, "fake-token")
	if err := cl.deleteRoom(context.Background(), "salle-1"); err != nil {
		t.Fatalf("deleteRoom: %v", err)
	}
	if len(calls) != 1 || calls[0] != "DeleteRoom" {
		t.Fatalf("appel attendu DeleteRoom, obtenu %v", calls)
	}
}

// TestTwirpClient_ErreurHTTP_TypeeeTwirpError vérifie qu'une réponse non-200
// retourne une erreur typée twirpError avec le statut et le corps.
func TestTwirpClient_ErreurHTTP_TypeeeTwirpError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"code":"not_found","msg":"room not found"}`, http.StatusNotFound)
	}))
	defer srv.Close()

	cl := newRoomClient(srv.URL, "fake-token")
	err := cl.deleteRoom(context.Background(), "inexistant")
	if err == nil {
		t.Fatal("attendu une erreur, obtenu nil")
	}
	te, ok := err.(*twirpError)
	if !ok {
		t.Fatalf("attendu *twirpError, obtenu %T: %v", err, err)
	}
	if te.Status != http.StatusNotFound {
		t.Errorf("status attendu %d, obtenu %d", http.StatusNotFound, te.Status)
	}
	if !strings.Contains(te.Body, "room not found") {
		t.Errorf("corps attendu contenir 'room not found', obtenu %q", te.Body)
	}
}

// TestTwirpClient_MuteNoAudioTrack_NoOp vérifie que MuteParticipant est no-op
// quand le participant n'a pas de piste audio.
func TestTwirpClient_MuteNoAudioTrack_NoOp(t *testing.T) {
	participants := []participantInfo{
		{
			Identity: "user-42",
			Tracks: []trackInfo{
				{Sid: "TR_video_01", Type: "VIDEO"},
			},
		},
	}
	var calls []string
	srv := buildFakeRoomService(t, participants, &calls)
	defer srv.Close()

	cl := newRoomClient(srv.URL, "fake-token")
	if err := cl.muteParticipantViaList(context.Background(), "salle-1", "user-42"); err != nil {
		t.Fatalf("muteParticipantViaList: %v", err)
	}
	// Seul ListParticipants doit être appelé (pas MutePublishedTrack).
	if len(calls) != 1 || calls[0] != "ListParticipants" {
		t.Fatalf("attendu [ListParticipants] seul, obtenu %v", calls)
	}
}
