//go:build cgo

// cmd/transcriber est un bot LiveKit pôle-only (CGO_ENABLED=1, JAMAIS exporté MIT).
// Il rejoint une salle LiveKit en mode caché (hidden:true, CanPublish:false),
// s'abonne aux pistes audio de chaque participant, décode Opus→PCM16 et écrit
// un fichier WAV par locuteur sous --out. À la fermeture (SIGINT/SIGTERM ou
// salle vide), il finalise les WAV et écrit <out>/manifest.json.
//
// Variables d'environnement requises : LIVEKIT_URL, LIVEKIT_API_KEY, LIVEKIT_API_SECRET.
// Arguments CLI : --room <name> --salon <id> [--out <dir>]
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/hazyhaar/assokit/internal/transcriber"
	lktoken "github.com/hazyhaar/assokit/pkg/connectors/livekit"
	"github.com/pion/webrtc/v4"
	lksdk "github.com/livekit/server-sdk-go/v2"
	"github.com/livekit/server-sdk-go/v2/pkg/samplebuilder"
	pioncodecs "github.com/pion/rtp/codecs"
	opus "gopkg.in/hraban/opus.v2"
)

const (
	botIdentity   = "transcriber-bot"
	tokenTTL      = 4 * time.Hour
	maxAudioLate  = 200 // tampons RTP pour le samplebuilder audio
	opusSampleRate = 48000 // Opus travaille à 48 kHz
	opusChannels   = 1
	// Nombre de samples Opus par trame (20 ms à 48 kHz).
	opusFrameSamples = 960
	// Nombre de samples PCM16 produits par trame Opus 20 ms (après resample 48k→16k).
	// Le décodeur Opus peut déjà décoder directement à 16 kHz (sample rate request).
	decodeSampleRate = 16000
	decodeSamples    = decodeSampleRate / 50 // 20 ms à 16 kHz = 320 samples
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stderr, nil))

	room := flag.String("room", "", "nom de la salle LiveKit (obligatoire)")
	salon := flag.String("salon", "", "ID du salon assokit (obligatoire)")
	out := flag.String("out", "", "répertoire de sortie pour les WAV et le manifest")
	flag.Parse()

	if *room == "" || *salon == "" {
		fmt.Fprintln(os.Stderr, "usage: transcriber --room <name> --salon <id> [--out <dir>]")
		os.Exit(1)
	}

	lkURL := os.Getenv("LIVEKIT_URL")
	apiKey := os.Getenv("LIVEKIT_API_KEY")
	apiSecret := os.Getenv("LIVEKIT_API_SECRET")
	if lkURL == "" || apiKey == "" || apiSecret == "" {
		log.Error("variables d'environnement manquantes: LIVEKIT_URL, LIVEKIT_API_KEY, LIVEKIT_API_SECRET")
		os.Exit(1)
	}

	outDir := *out
	if outDir == "" {
		outDir = filepath.Join(os.TempDir(), "assokit-transcribe", *room)
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		log.Error("impossible de créer le répertoire de sortie", "dir", outDir, "err", err)
		os.Exit(1)
	}

	tok, err := lktoken.RoomHiddenToken(apiKey, apiSecret, *room, botIdentity, tokenTTL)
	if err != nil {
		log.Error("forge du jeton caché échouée", "err", err)
		os.Exit(1)
	}

	startedAt := time.Now().UTC()

	bot := &bot{
		log:      log,
		outDir:   outDir,
		roomName: *room,
		salonID:  *salon,
		started:  startedAt,
		writers:  make(map[string]*transcriber.WAVWriter),
		speakers: make(map[string]string),
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	cb := &lksdk.RoomCallback{
		OnDisconnected: func() {
			log.Info("déconnecté de la salle, arrêt")
			cancel()
		},
		ParticipantCallback: lksdk.ParticipantCallback{
			OnTrackSubscribed: func(track *webrtc.TrackRemote, pub *lksdk.RemoteTrackPublication, rp *lksdk.RemoteParticipant) {
				bot.onTrack(ctx, track, rp)
			},
		},
	}
	lkRoom, err := lksdk.ConnectToRoomWithToken(lkURL, tok, cb)
	if err != nil {
		log.Error("connexion LiveKit échouée", "room", *room, "err", err)
		os.Exit(1)
	}

	log.Info("bot connecté", "room", *room, "out", outDir)

	select {
	case <-sigCh:
		log.Info("signal reçu, arrêt propre")
		cancel()
	case <-ctx.Done():
	}

	lkRoom.Disconnect()
	bot.shutdown()
}

// bot encapsule l'état du transcripteur.
type bot struct {
	log      *slog.Logger
	outDir   string
	roomName string
	salonID  string
	started  time.Time

	mu       sync.Mutex
	writers  map[string]*transcriber.WAVWriter // identity → WAVWriter
	speakers map[string]string                 // identity → wav_path
	wg       sync.WaitGroup
}

// onTrack est appelé par OnTrackSubscribed pour chaque piste reçue.
func (b *bot) onTrack(ctx context.Context, track *webrtc.TrackRemote, rp *lksdk.RemoteParticipant) {
	if !strings.EqualFold(track.Codec().MimeType, "audio/opus") {
		// Ignorer les pistes non-audio.
		return
	}

	identity := rp.Identity()
	b.log.Info("piste audio souscrite", "identity", identity)

	b.mu.Lock()
	w, exists := b.writers[identity]
	if !exists {
		wavPath := filepath.Join(b.outDir, sanitizeIdentity(identity)+".wav")
		var err error
		w, err = transcriber.NewWAVWriter(wavPath)
		if err != nil {
			b.mu.Unlock()
			b.log.Error("création WAVWriter échouée", "identity", identity, "err", err)
			return
		}
		b.writers[identity] = w
		b.speakers[identity] = wavPath
	}
	b.mu.Unlock()

	b.wg.Add(1)
	go func() {
		defer b.wg.Done()
		b.drainAudio(ctx, track, w, identity)
	}()
}

// drainAudio lit les paquets RTP de la piste, assemble les trames Opus via
// samplebuilder, décode en PCM16 16 kHz et écrit dans le WAVWriter.
func (b *bot) drainAudio(ctx context.Context, track *webrtc.TrackRemote, w *transcriber.WAVWriter, identity string) {
	defer func() {
		if r := recover(); r != nil {
			b.log.Error("panic dans drainAudio", "identity", identity, "recover", r)
		}
	}()

	dec, err := opus.NewDecoder(decodeSampleRate, opusChannels)
	if err != nil {
		b.log.Error("création décodeur Opus échouée", "identity", identity, "err", err)
		return
	}

	sb := samplebuilder.New(maxAudioLate, &pioncodecs.OpusPacket{}, track.Codec().ClockRate)
	pcmBuf := make([]int16, decodeSamples*10) // tampon généreux

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		pkt, _, err := track.ReadRTP()
		if err != nil {
			// La piste est fermée (locuteur parti ou salle déconnectée).
			b.log.Info("fin de piste audio", "identity", identity)
			return
		}
		sb.Push(pkt)

		for _, assembled := range sb.PopPackets() {
			// assembled.Payload contient le payload Opus brut.
			n, err := dec.Decode(assembled.Payload, pcmBuf)
			if err != nil {
				b.log.Warn("décodage Opus échoué", "identity", identity, "err", err)
				continue
			}
			if err := w.WritePCM16(pcmBuf[:n]); err != nil {
				b.log.Error("écriture WAV échouée", "identity", identity, "err", err)
				return
			}
		}
	}
}

// shutdown ferme tous les WAVWriter et écrit le manifest.
func (b *bot) shutdown() {
	// Attendre la fin de toutes les goroutines de drain.
	b.wg.Wait()

	b.mu.Lock()
	defer b.mu.Unlock()

	for identity, w := range b.writers {
		if err := w.Close(); err != nil {
			b.log.Error("fermeture WAV échouée", "identity", identity, "err", err)
		}
	}

	endedAt := time.Now().UTC()
	speakers := make([]transcriber.SpeakerEntry, 0, len(b.speakers))
	for id, path := range b.speakers {
		speakers = append(speakers, transcriber.SpeakerEntry{
			Identity: id,
			WAVPath:  path,
		})
	}

	m := &transcriber.Manifest{
		Room:      b.roomName,
		SalonID:   b.salonID,
		StartedAt: b.started,
		EndedAt:   endedAt,
		Speakers:  speakers,
	}
	manifestPath := filepath.Join(b.outDir, "manifest.json")
	if err := transcriber.WriteManifest(manifestPath, m); err != nil {
		b.log.Error("écriture manifest échouée", "err", err)
	} else {
		b.log.Info("manifest écrit", "path", manifestPath, "speakers", len(speakers))
	}
}

// sanitizeIdentity remplace les caractères non-sûrs pour un nom de fichier.
func sanitizeIdentity(id string) string {
	var sb strings.Builder
	for _, r := range id {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			sb.WriteRune(r)
		} else {
			sb.WriteRune('_')
		}
	}
	if sb.Len() == 0 {
		return "unknown"
	}
	return sb.String()
}
