//go:build cgo

package transcriber_test

import (
	"encoding/binary"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hazyhaar/assokit/internal/transcriber"
)

// TestWAVWriter_HeaderAndSamples écrit N samples PCM16 (sinus 440 Hz)
// puis relit le fichier pour vérifier la structure RIFF et la cohérence des données.
func TestWAVWriter_HeaderAndSamples(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wav")

	w, err := transcriber.NewWAVWriter(path)
	if err != nil {
		t.Fatalf("NewWAVWriter: %v", err)
	}

	// Générer 1 seconde de sinus 440 Hz à 16 kHz, amplitude 50%.
	const nSamples = transcriber.SampleRate
	samples := make([]int16, nSamples)
	for i := range samples {
		samples[i] = int16(math.Sin(2*math.Pi*440*float64(i)/transcriber.SampleRate) * math.MaxInt16 / 2)
	}

	if err := w.WritePCM16(samples); err != nil {
		t.Fatalf("WritePCM16: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Relire et vérifier.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if len(data) < 44 {
		t.Fatalf("fichier trop court: %d octets", len(data))
	}

	// Vérifier le magic RIFF.
	if string(data[0:4]) != "RIFF" {
		t.Errorf("magic RIFF absent: %q", data[0:4])
	}
	if string(data[8:12]) != "WAVE" {
		t.Errorf("magic WAVE absent: %q", data[8:12])
	}
	if string(data[12:16]) != "fmt " {
		t.Errorf("chunk fmt absent: %q", data[12:16])
	}

	// Vérifier les champs numériques.
	audioFmt := binary.LittleEndian.Uint16(data[20:])
	if audioFmt != 1 {
		t.Errorf("format audio: got %d want 1 (PCM)", audioFmt)
	}
	channels := binary.LittleEndian.Uint16(data[22:])
	if channels != transcriber.NumChannels {
		t.Errorf("canaux: got %d want %d", channels, transcriber.NumChannels)
	}
	sampleRate := binary.LittleEndian.Uint32(data[24:])
	if sampleRate != transcriber.SampleRate {
		t.Errorf("sample rate: got %d want %d", sampleRate, transcriber.SampleRate)
	}
	bitsPerSample := binary.LittleEndian.Uint16(data[34:])
	if bitsPerSample != transcriber.BitsPerSample {
		t.Errorf("bits/sample: got %d want %d", bitsPerSample, transcriber.BitsPerSample)
	}

	// Vérifier la taille du chunk data.
	if string(data[36:40]) != "data" {
		t.Errorf("chunk data absent: %q", data[36:40])
	}
	dataSize := binary.LittleEndian.Uint32(data[40:])
	expectedDataSize := uint32(nSamples * transcriber.BitsPerSample / 8)
	if dataSize != expectedDataSize {
		t.Errorf("dataSize: got %d want %d", dataSize, expectedDataSize)
	}

	// Vérifier la taille totale du fichier.
	expectedLen := 44 + int(expectedDataSize)
	if len(data) != expectedLen {
		t.Errorf("taille fichier: got %d want %d", len(data), expectedLen)
	}
}

// TestWAVWriter_Silence vérifie qu'un fichier avec 0 sample est toujours valide.
func TestWAVWriter_Silence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "silence.wav")

	w, err := transcriber.NewWAVWriter(path)
	if err != nil {
		t.Fatalf("NewWAVWriter: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if len(data) != 44 {
		t.Errorf("fichier silence: taille %d want 44", len(data))
	}
	dataSize := binary.LittleEndian.Uint32(data[40:])
	if dataSize != 0 {
		t.Errorf("dataSize silence: got %d want 0", dataSize)
	}
}

// TestWriteManifest vérifie la sérialisation JSON du manifest.
func TestWriteManifest(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "manifest.json")

	now := time.Date(2026, 6, 13, 10, 0, 0, 0, time.UTC)
	m := &transcriber.Manifest{
		Room:      "salle-ag",
		SalonID:   "salon-42",
		StartedAt: now,
		EndedAt:   now.Add(30 * time.Minute),
		Speakers: []transcriber.SpeakerEntry{
			{Identity: "alice", WAVPath: "/tmp/alice.wav"},
			{Identity: "bob", WAVPath: "/tmp/bob.wav"},
		},
	}

	if err := transcriber.WriteManifest(path, m); err != nil {
		t.Fatalf("WriteManifest: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	var got transcriber.Manifest
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.Room != m.Room {
		t.Errorf("Room: got %q want %q", got.Room, m.Room)
	}
	if got.SalonID != m.SalonID {
		t.Errorf("SalonID: got %q want %q", got.SalonID, m.SalonID)
	}
	if len(got.Speakers) != 2 {
		t.Fatalf("Speakers: got %d want 2", len(got.Speakers))
	}
	if got.Speakers[0].Identity != "alice" {
		t.Errorf("speaker[0]: got %q want alice", got.Speakers[0].Identity)
	}
}
