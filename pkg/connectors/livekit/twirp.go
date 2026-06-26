// Package livekit — client Twirp HTTP minimal vers le RoomService LiveKit.
//
// Le protocole Twirp est hand-rolled (zéro SDK externe) :
//   - POST <serverURL>/twirp/livekit.RoomService/<Method>
//   - Header : Authorization: Bearer <admin token>, Content-Type: application/json
//   - Corps : JSON des paramètres
//   - Réponse : JSON décodé, ou erreur typée si HTTP status != 200.
package livekit

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// twirpError est retourné quand le serveur LiveKit répond avec un statut HTTP non-200.
type twirpError struct {
	Status int
	Body   string
}

func (e *twirpError) Error() string {
	return fmt.Sprintf("livekit RoomService: HTTP %d: %s", e.Status, e.Body)
}

// roomClient est le client Twirp RoomService.
type roomClient struct {
	serverURL  string // ex. "wss://livekit.example.org" — converti en https:// pour Twirp
	adminToken string
	httpClient *http.Client
}

// newRoomClient construit un client avec un httpClient à timeout court.
func newRoomClient(serverURL, adminToken string) *roomClient {
	// LiveKit expose Twirp sur HTTPS même si le SDK WS utilise WSS.
	// On normalise l'URL de base : remplacer wss:// ou ws:// par https:// ou http://.
	base := serverURL
	base = strings.TrimRight(base, "/")
	base = strings.Replace(base, "wss://", "https://", 1)
	base = strings.Replace(base, "ws://", "http://", 1)
	return &roomClient{
		serverURL:  base,
		adminToken: adminToken,
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

// call effectue un appel Twirp POST.
func (c *roomClient) call(ctx context.Context, method string, req, resp any) error {
	body, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("livekit twirp: marshal %s: %w", method, err)
	}

	url := c.serverURL + "/twirp/livekit.RoomService/" + method
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("livekit twirp: new request %s: %w", method, err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.adminToken)

	httpResp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("livekit twirp: %s: %w", method, err)
	}
	defer httpResp.Body.Close() //nolint:errcheck

	respBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return fmt.Errorf("livekit twirp: read body %s: %w", method, err)
	}

	if httpResp.StatusCode != http.StatusOK {
		return &twirpError{Status: httpResp.StatusCode, Body: strings.TrimSpace(string(respBody))}
	}

	if resp != nil {
		if err := json.Unmarshal(respBody, resp); err != nil {
			return fmt.Errorf("livekit twirp: unmarshal %s: %w", method, err)
		}
	}
	return nil
}

// --- Structures des requêtes / réponses RoomService ---

type listParticipantsReq struct {
	Room string `json:"room"`
}

type trackInfo struct {
	Sid    string `json:"sid"`
	Type   string `json:"type"` // "AUDIO" | "VIDEO" | "DATA"
	Muted  bool   `json:"muted"`
	Source string `json:"source"`
}

type participantInfo struct {
	Identity string      `json:"identity"`
	Tracks   []trackInfo `json:"tracks"`
}

type listParticipantsResp struct {
	Participants []participantInfo `json:"participants"`
}

type mutePublishedTrackReq struct {
	Room     string `json:"room"`
	Identity string `json:"identity"`
	TrackSid string `json:"track_sid"`
	Muted    bool   `json:"muted"`
}

type removeParticipantReq struct {
	Room     string `json:"room"`
	Identity string `json:"identity"`
}

type deleteRoomReq struct {
	Room string `json:"room"`
}

// --- Méthodes RoomService ---

// listParticipants appelle ListParticipants et retourne les infos.
func (c *roomClient) listParticipants(ctx context.Context, room string) ([]participantInfo, error) {
	var resp listParticipantsResp
	if err := c.call(ctx, "ListParticipants", listParticipantsReq{Room: room}, &resp); err != nil {
		return nil, err
	}
	return resp.Participants, nil
}

// mutePublishedTrack appelle MutePublishedTrack.
func (c *roomClient) mutePublishedTrack(ctx context.Context, room, identity, trackSid string, muted bool) error {
	return c.call(ctx, "MutePublishedTrack", mutePublishedTrackReq{
		Room:     room,
		Identity: identity,
		TrackSid: trackSid,
		Muted:    muted,
	}, nil)
}

// removeParticipant appelle RemoveParticipant.
func (c *roomClient) removeParticipant(ctx context.Context, room, identity string) error {
	return c.call(ctx, "RemoveParticipant", removeParticipantReq{Room: room, Identity: identity}, nil)
}

// deleteRoom appelle DeleteRoom.
func (c *roomClient) deleteRoom(ctx context.Context, room string) error {
	return c.call(ctx, "DeleteRoom", deleteRoomReq{Room: room}, nil)
}
