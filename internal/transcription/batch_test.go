package transcription

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// --- Fake store pour tests batch ---

type fakeBatchStore struct {
	statuses []string
	segments []fakeSegment
	failOn   string // SetStatus value qui déclenche une erreur (vide = jamais)
}

type fakeSegment struct {
	transcriptID, speaker, text string
	t0, t1                      int64
}

func (f *fakeBatchStore) SetStatus(_ context.Context, id, status string) error {
	f.statuses = append(f.statuses, status)
	return nil
}

func (f *fakeBatchStore) AddSegment(_ context.Context, transcriptID, speaker string, t0ms, t1ms int64, text string) error {
	f.segments = append(f.segments, fakeSegment{transcriptID, speaker, text, t0ms, t1ms})
	return nil
}

// --- Helpers ---

// realWhisperFixture retourne le contenu du JSON whisper réel obtenu au sol sur .79.
func realWhisperFixtureBytes(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "whisper_tone.json"))
	if err != nil {
		t.Fatalf("fixture introuvable: %v", err)
	}
	return b
}

// writeManifest écrit un manifest.json minimal dans dir et retourne son chemin.
func writeManifest(t *testing.T, dir string, speakers []manifestSpeaker) string {
	t.Helper()
	mf := manifestDTO{
		Room:      "test-room",
		Salon:     "salon-1",
		StartedAt: time.Now().UTC().Format(time.RFC3339),
		Speakers:  speakers,
	}
	b, _ := json.Marshal(mf)
	p := filepath.Join(dir, "manifest.json")
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatalf("writeManifest: %v", err)
	}
	return p
}

// makeRunWhisperFromFixture retourne un RunWhisper qui copie la fixture JSON
// à l'emplacement attendu (outPrefix+".json"), sans lancer whisper-cli.
// Ce n'est pas de la donnée inventée : la fixture est la sortie réelle de whisper-cli
// obtenue sur .79 (testdata/whisper_tone.json).
func makeRunWhisperFromFixture(fixtureData []byte) func(ctx context.Context, wavPath, outPrefix, model, bin string) error {
	return func(_ context.Context, _, outPrefix, _, _ string) error {
		return os.WriteFile(outPrefix+".json", fixtureData, 0o600)
	}
}

// --- Tests ---

// TestParseWhisperJSON vérifie le parsing du JSON whisper réel (fixture .79).
func TestParseWhisperJSON(t *testing.T) {
	dir := t.TempDir()
	fixturePath := filepath.Join(dir, "out.json")
	if err := os.WriteFile(fixturePath, realWhisperFixtureBytes(t), 0o600); err != nil {
		t.Fatal(err)
	}

	segs, err := parseWhisperJSON(fixturePath)
	if err != nil {
		t.Fatalf("parseWhisperJSON: %v", err)
	}
	if len(segs) != 1 {
		t.Fatalf("attendu 1 segment, obtenu %d", len(segs))
	}
	seg := segs[0]
	if seg.Offsets.From != 0 {
		t.Errorf("t0 attendu 0, obtenu %d", seg.Offsets.From)
	}
	if seg.Offsets.To != 3000 {
		t.Errorf("t1 attendu 3000, obtenu %d", seg.Offsets.To)
	}
	wantText := " *Bip*"
	if seg.Text != wantText {
		t.Errorf("text attendu %q, obtenu %q", wantText, seg.Text)
	}
}

// TestTranscribe_PurgeWAV vérifie qu'après une transcription réussie, le WAV est supprimé.
func TestTranscribe_PurgeWAV(t *testing.T) {
	dir := t.TempDir()

	// Crée un vrai WAV minimal (16kHz, 1s, silence).
	wavPath := filepath.Join(dir, "speaker-alice.wav")
	wavData := makeMinimalWAV()
	if err := os.WriteFile(wavPath, wavData, 0o600); err != nil {
		t.Fatal(err)
	}

	manifestPath := writeManifest(t, dir, []manifestSpeaker{
		{Identity: "alice", WavPath: wavPath},
	})

	store := &fakeBatchStore{}
	b := &Batcher{
		Store:        store,
		WhisperBin:   "whisper-cli",
		WhisperModel: "/opt/whisper.cpp/models/ggml-small.bin",
		RunWhisper:   makeRunWhisperFromFixture(realWhisperFixtureBytes(t)),
	}

	if err := b.Transcribe(context.Background(), "tid-1", manifestPath); err != nil {
		t.Fatalf("Transcribe: %v", err)
	}

	// Le WAV doit avoir été supprimé.
	if _, err := os.Stat(wavPath); !os.IsNotExist(err) {
		t.Error("WAV aurait dû être supprimé après transcription réussie")
	}

	// Les segments doivent être persistés.
	if len(store.segments) != 1 {
		t.Fatalf("attendu 1 segment, obtenu %d", len(store.segments))
	}
	seg := store.segments[0]
	if seg.speaker != "alice" {
		t.Errorf("speaker attendu 'alice', obtenu %q", seg.speaker)
	}
	if seg.t0 != 0 || seg.t1 != 3000 {
		t.Errorf("offsets attendus 0/3000, obtenu %d/%d", seg.t0, seg.t1)
	}
	if seg.text != "*Bip*" {
		t.Errorf("texte attendu '*Bip*', obtenu %q", seg.text)
	}

	// Statuts : "transcribing" puis "done".
	if len(store.statuses) < 2 || store.statuses[0] != "transcribing" || store.statuses[len(store.statuses)-1] != "done" {
		t.Errorf("statuts inattendus: %v", store.statuses)
	}
}

// TestTranscribe_KeepWAVOnFailure vérifie qu'en cas d'erreur whisper, le WAV est conservé.
func TestTranscribe_KeepWAVOnFailure(t *testing.T) {
	dir := t.TempDir()

	wavPath := filepath.Join(dir, "speaker-bob.wav")
	wavData := makeMinimalWAV()
	if err := os.WriteFile(wavPath, wavData, 0o600); err != nil {
		t.Fatal(err)
	}

	manifestPath := writeManifest(t, dir, []manifestSpeaker{
		{Identity: "bob", WavPath: wavPath},
	})

	store := &fakeBatchStore{}
	b := &Batcher{
		Store:        store,
		WhisperBin:   "whisper-cli",
		WhisperModel: "/opt/whisper.cpp/models/ggml-small.bin",
		RunWhisper: func(_ context.Context, _, _, _, _ string) error {
			return os.ErrNotExist // simule une erreur whisper
		},
	}

	err := b.Transcribe(context.Background(), "tid-2", manifestPath)
	if err == nil {
		t.Fatal("attendu une erreur")
	}

	// Le WAV doit avoir été CONSERVÉ pour diagnostic.
	if _, err := os.Stat(wavPath); os.IsNotExist(err) {
		t.Error("WAV aurait dû être conservé en cas d'erreur")
	}

	// Statut "failed" posé.
	found := false
	for _, s := range store.statuses {
		if s == "failed" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("statut 'failed' attendu, statuts: %v", store.statuses)
	}
}

// makeMinimalWAV retourne un WAV 16kHz mono 16-bit valide (silence, 0.1s).
func makeMinimalWAV() []byte {
	sampleRate := uint32(16000)
	numSamples := uint32(1600) // 0.1s
	dataSize := numSamples * 2
	buf := make([]byte, 44+dataSize)
	copy(buf[0:], "RIFF")
	le32(buf[4:], 36+dataSize)
	copy(buf[8:], "WAVE")
	copy(buf[12:], "fmt ")
	le32(buf[16:], 16)
	le16(buf[20:], 1) // PCM
	le16(buf[22:], 1) // mono
	le32(buf[24:], sampleRate)
	le32(buf[28:], sampleRate*2)
	le16(buf[32:], 2)
	le16(buf[34:], 16)
	copy(buf[36:], "data")
	le32(buf[40:], dataSize)
	// données = zéros (silence)
	return buf
}

func le32(b []byte, v uint32) {
	b[0] = byte(v)
	b[1] = byte(v >> 8)
	b[2] = byte(v >> 16)
	b[3] = byte(v >> 24)
}

func le16(b []byte, v uint16) {
	b[0] = byte(v)
	b[1] = byte(v >> 8)
}
