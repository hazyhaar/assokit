package transcription

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
)

// fakeSalon est une implémentation en mémoire de SalonStore pour les tests.
type fakeSalon struct {
	shouldTranscribe bool
	salonID          string
	err              error
}

func (f *fakeSalon) ShouldTranscribe(_ context.Context, _ string) (bool, error) {
	return f.shouldTranscribe, f.err
}

func (f *fakeSalon) GetSalonID(_ context.Context, _ string) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	return f.salonID, nil
}

// fakeTranscript est une implémentation en mémoire de TranscriptStore.
type fakeTranscript struct {
	created []string // salon IDs ayant déclenché CreateTranscript
}

func (f *fakeTranscript) CreateTranscript(_ context.Context, salonID string) (TranscriptResult, error) {
	f.created = append(f.created, salonID)
	return TranscriptResult{ID: "tr-test-" + salonID}, nil
}

func (f *fakeTranscript) SetStatus(_ context.Context, _, _ string) error { return nil }

func TestLauncher_ConsentOff_NoSpawn(t *testing.T) {
	var spawned int32
	l := &Launcher{
		Salon:      &fakeSalon{shouldTranscribe: false, salonID: "salon-1"},
		Transcript: &fakeTranscript{},
		running:    make(map[string]func()),
		Spawn: func(_ context.Context, _, _, _, _, _ string, _ []string) (func(), error) {
			atomic.AddInt32(&spawned, 1)
			return func() {}, nil
		},
	}

	l.OnRoomEvent(context.Background(), "room_started", "mon-salon")

	if atomic.LoadInt32(&spawned) != 0 {
		t.Errorf("spawner appelé alors que consentement=false (spawned=%d)", spawned)
	}
}

func TestLauncher_ConsentOn_Spawn(t *testing.T) {
	var spawned int32
	tr := &fakeTranscript{}
	l := &Launcher{
		Salon:      &fakeSalon{shouldTranscribe: true, salonID: "salon-42"},
		Transcript: tr,
		running:    make(map[string]func()),
		OutDir:     t.TempDir(),
		Spawn: func(_ context.Context, _, _, _, _, _ string, _ []string) (func(), error) {
			atomic.AddInt32(&spawned, 1)
			// Retourne un stopper qui termine immédiatement.
			return func() {}, nil
		},
	}

	l.OnRoomEvent(context.Background(), "room_started", "mon-salon")

	if atomic.LoadInt32(&spawned) != 1 {
		t.Errorf("spawner doit être appelé une fois (spawned=%d)", spawned)
	}
	if len(tr.created) != 1 {
		t.Errorf("CreateTranscript doit être appelé une fois (got %d)", len(tr.created))
	}
}

func TestLauncher_ConsentOn_Idempotent(t *testing.T) {
	var spawned int32
	tr := &fakeTranscript{}
	l := &Launcher{
		Salon:      &fakeSalon{shouldTranscribe: true, salonID: "salon-42"},
		Transcript: tr,
		running:    make(map[string]func()),
		OutDir:     t.TempDir(),
		Spawn: func(_ context.Context, _, _, _, _, _ string, _ []string) (func(), error) {
			atomic.AddInt32(&spawned, 1)
			// Stopper bloquant fictif : ne termine pas tant que pas appelé.
			done := make(chan struct{})
			return func() { <-done }, nil
		},
	}

	l.OnRoomEvent(context.Background(), "room_started", "mon-salon")
	l.OnRoomEvent(context.Background(), "participant_joined", "mon-salon") // idempotent

	if atomic.LoadInt32(&spawned) != 1 {
		t.Errorf("spawner doit être appelé une seule fois même avec 2 events (spawned=%d)", spawned)
	}
}

func TestLauncher_SpawnError_StatusFailed(t *testing.T) {
	tr := &fakeTranscript{}
	l := &Launcher{
		Salon:      &fakeSalon{shouldTranscribe: true, salonID: "salon-99"},
		Transcript: tr,
		running:    make(map[string]func()),
		OutDir:     t.TempDir(),
		Spawn: func(_ context.Context, _, _, _, _, _ string, _ []string) (func(), error) {
			return nil, errors.New("binaire introuvable")
		},
	}

	l.OnRoomEvent(context.Background(), "room_started", "salle-err")
	// Le launcher doit avoir créé un transcript puis appelé SetStatus("failed").
	// La fakeTranscript.SetStatus est no-op ; on vérifie juste qu'il n'y a pas de panic.
	if len(tr.created) != 1 {
		t.Errorf("CreateTranscript attendu même en cas d'erreur spawn (got %d)", len(tr.created))
	}
}
