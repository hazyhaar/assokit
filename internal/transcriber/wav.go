//go:build cgo

// Package transcriber fournit l'écrivain WAV PCM16 et le manifest JSON
// utilisés par le bot cmd/transcriber. Pôle-only : jamais exporté dans le kit MIT.
package transcriber

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"
	"time"
)

const (
	// SampleRate est la fréquence d'échantillonnage cible (16 kHz mono).
	SampleRate = 16000
	// NumChannels est le nombre de canaux audio (mono).
	NumChannels = 1
	// BitsPerSample est la profondeur de bit (PCM16).
	BitsPerSample = 16
)

// WAVWriter écrit des échantillons PCM16 dans un fichier WAV sur disque.
// L'en-tête RIFF est corrigé à la fermeture (Close).
type WAVWriter struct {
	mu      sync.Mutex
	f       *os.File
	samples uint32 // nombre d'échantillons écrits (pas d'octets)
}

// NewWAVWriter crée ou ouvre en append un fichier WAV à path.
// Si le fichier existe déjà (ré-abonnement même identité), il est complété.
func NewWAVWriter(path string) (*WAVWriter, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("transcriber: ouvrir WAV %s: %w", path, err)
	}

	w := &WAVWriter{f: f}

	// Si le fichier est vide, écrire le stub d'en-tête (44 octets).
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("transcriber: stat WAV: %w", err)
	}
	if fi.Size() == 0 {
		if err := w.writeHeader(0); err != nil {
			f.Close()
			return nil, err
		}
	} else {
		// Fichier existant : lire le nombre d'échantillons déjà écrits depuis
		// le champ dataSize de l'en-tête (offset 40, uint32 LE, octets).
		var dataSize uint32
		if _, err := f.Seek(40, io.SeekStart); err == nil {
			_ = binary.Read(f, binary.LittleEndian, &dataSize)
			w.samples = dataSize / (BitsPerSample / 8)
		}
		// Positionner à la fin pour appender.
		if _, err := f.Seek(0, io.SeekEnd); err != nil {
			f.Close()
			return nil, fmt.Errorf("transcriber: seek end WAV: %w", err)
		}
	}
	return w, nil
}

// WritesamPCM16 écrit un tampon de samples PCM16 little-endian.
func (w *WAVWriter) WritePCM16(samples []int16) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	buf := make([]byte, len(samples)*2)
	for i, s := range samples {
		binary.LittleEndian.PutUint16(buf[i*2:], uint16(s))
	}
	if _, err := w.f.Write(buf); err != nil {
		return fmt.Errorf("transcriber: écriture PCM16: %w", err)
	}
	w.samples += uint32(len(samples))
	return nil
}

// Close corrige l'en-tête RIFF et ferme le fichier.
func (w *WAVWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if _, err := w.f.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("transcriber: rewind WAV: %w", err)
	}
	if err := w.writeHeader(w.samples); err != nil {
		return err
	}
	return w.f.Close()
}

// writeHeader écrit l'en-tête RIFF WAV (44 octets) pour nSamples échantillons.
func (w *WAVWriter) writeHeader(nSamples uint32) error {
	byteRate := uint32(SampleRate * NumChannels * BitsPerSample / 8)
	blockAlign := uint16(NumChannels * BitsPerSample / 8)
	dataSize := nSamples * uint32(BitsPerSample/8)
	chunkSize := 36 + dataSize

	buf := make([]byte, 44)
	copy(buf[0:4], "RIFF")
	binary.LittleEndian.PutUint32(buf[4:], chunkSize)
	copy(buf[8:12], "WAVE")
	copy(buf[12:16], "fmt ")
	binary.LittleEndian.PutUint32(buf[16:], 16)          // taille du bloc fmt
	binary.LittleEndian.PutUint16(buf[20:], 1)           // PCM = 1
	binary.LittleEndian.PutUint16(buf[22:], NumChannels) // mono
	binary.LittleEndian.PutUint32(buf[24:], SampleRate)
	binary.LittleEndian.PutUint32(buf[28:], byteRate)
	binary.LittleEndian.PutUint16(buf[32:], blockAlign)
	binary.LittleEndian.PutUint16(buf[34:], BitsPerSample)
	copy(buf[36:40], "data")
	binary.LittleEndian.PutUint32(buf[40:], dataSize)

	_, err := w.f.Write(buf)
	return err
}

// Manifest est la structure JSON écrite dans <out>/manifest.json en fin de session.
type Manifest struct {
	Room      string         `json:"room"`
	SalonID   string         `json:"salon_id"`
	StartedAt time.Time      `json:"started_at"`
	EndedAt   time.Time      `json:"ended_at"`
	Speakers  []SpeakerEntry `json:"speakers"`
}

// SpeakerEntry décrit un locuteur capturé.
type SpeakerEntry struct {
	Identity string `json:"identity"`
	WAVPath  string `json:"wav_path"`
}

// WriteManifest sérialise m dans path (crée ou écrase).
func WriteManifest(path string, m *Manifest) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("transcriber: créer manifest: %w", err)
	}
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(m); err != nil {
		f.Close()
		return fmt.Errorf("transcriber: écrire manifest: %w", err)
	}
	return f.Close()
}
