package eventsink_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hazyhaar/assokit/pkg/eventsink"
)

func TestNopSink(t *testing.T) {
	if err := (eventsink.NopSink{}).Emit(context.Background(), eventsink.Event{Type: "x"}); err != nil {
		t.Fatalf("NopSink.Emit: %v", err)
	}
}

func TestHTTPSink_PostsSignedJSON(t *testing.T) {
	const secret = "s3cr3t"
	var gotBody []byte
	var gotSig string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		gotSig = r.Header.Get("X-Assokit-Signature")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	sink := eventsink.HTTPSink{URL: srv.URL, Secret: secret}
	ctx := eventsink.WithTimestamp(context.Background(), "2026-05-31T00:00:00Z")
	err := sink.Emit(ctx, eventsink.Event{
		Type:    "feedback.created",
		Payload: map[string]any{"page_url": "/x"},
	})
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}

	// Le corps contient le type + l'horodatage injecté.
	var decoded map[string]any
	if err := json.Unmarshal(gotBody, &decoded); err != nil {
		t.Fatalf("corps non-JSON: %v", err)
	}
	if decoded["type"] != "feedback.created" {
		t.Fatalf("type=%v", decoded["type"])
	}
	if decoded["timestamp"] != "2026-05-31T00:00:00Z" {
		t.Fatalf("timestamp=%v", decoded["timestamp"])
	}

	// Signature HMAC-SHA256 valide.
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(gotBody)
	want := hex.EncodeToString(mac.Sum(nil))
	if gotSig != want {
		t.Fatalf("signature: got %s want %s", gotSig, want)
	}
}

func TestHTTPSink_ErrorOnBadStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	if err := (eventsink.HTTPSink{URL: srv.URL}).Emit(context.Background(), eventsink.Event{Type: "x"}); err == nil {
		t.Fatal("attendu une erreur sur status 500")
	}
}
