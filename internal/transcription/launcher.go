// CLAUDE:SUMMARY Launcher transcripteur — OnRoomEvent déclenche le bot transcriber via exec.Cmd sur consentement RGPD. Spawner injectable pour tests.
// CLAUDE:WARN Secrets LiveKit via ENV du sous-process (LIVEKIT_URL/API_KEY/API_SECRET), jamais en argv ni en log.
package transcription

import (
	"context"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
)

// SalonStore est l'interface côté consommateur vers pkg/salon.Store.
// Définie ici (côté consommateur) pour rester découplée du producteur.
type SalonStore interface {
	ShouldTranscribe(ctx context.Context, slug string) (bool, error)
	GetSalonID(ctx context.Context, slug string) (string, error)
}

// TranscriptStore est l'interface côté consommateur vers pkg/transcript.Store.
type TranscriptStore interface {
	CreateTranscript(ctx context.Context, salonID string) (TranscriptResult, error)
	SetStatus(ctx context.Context, id, status string) error
}

// TranscriptResult est le sous-ensemble retourné par CreateTranscript.
type TranscriptResult struct {
	ID string
}

// SpawnFn est la fonction de spawn injectable (pour tests : remplace exec.Cmd).
// Elle doit retourner un stopper (func() pour annulation / attente) ou une erreur.
type SpawnFn func(ctx context.Context, transcriptID, slug, salonID, outDir, bin string, env []string) (stop func(), err error)

// Launcher gère le déclenchement du bot transcripteur.
// Le connecteur livekit l'appelle via OnRoomEvent (hook).
// Le Launcher ne connaît pas le SDK LiveKit ni os/exec directement
// (celui-ci est injecté via SpawnFn pour la testabilité).
type Launcher struct {
	Salon      SalonStore
	Transcript TranscriptStore
	Logger     *slog.Logger

	// TranscriberBin : chemin du binaire transcriber. Défaut : "transcriber" dans PATH.
	// Lire depuis env ASSOKIT_TRANSCRIBER_BIN à la construction (bordure).
	TranscriberBin string

	// OutDir : répertoire de sortie des WAV. Défaut : /tmp/assokit-transcribe.
	// Lire depuis env ASSOKIT_TRANSCRIBE_DIR à la construction (bordure).
	OutDir string

	// LiveKitURL/APIKey/APISecret : injectés dans l'env du sous-process.
	// Ne jamais passer en argv ni logguer.
	LiveKitURL    string
	LiveKitAPIKey string
	// LiveKitAPISecret est stocké séparé, zéroïsé si possible après usage.
	LiveKitAPISecret string

	// Spawn : fonction de lancement effective. Si nil, defaultSpawn est utilisé.
	Spawn SpawnFn

	// Batch : moteur de transcription post-enregistrement (optionnel).
	// Si non nil, appelé dans la goroutine d'attente après la fin du bot.
	Batch *Batcher

	mu      sync.Mutex
	running map[string]func() // slug → stopper
}

// NewLauncher construit un Launcher avec les valeurs d'env par défaut.
func NewLauncher(salon SalonStore, transcript TranscriptStore, logger *slog.Logger) *Launcher {
	bin := os.Getenv("ASSOKIT_TRANSCRIBER_BIN")
	if bin == "" {
		bin = "transcriber"
	}
	outDir := os.Getenv("ASSOKIT_TRANSCRIBE_DIR")
	if outDir == "" {
		outDir = "/tmp/assokit-transcribe"
	}
	return &Launcher{
		Salon:      salon,
		Transcript: transcript,
		Logger:     logger,
		TranscriberBin: bin,
		OutDir:     outDir,
		running:    make(map[string]func()),
	}
}

func (l *Launcher) logger() *slog.Logger {
	if l.Logger != nil {
		return l.Logger
	}
	return slog.Default()
}

func (l *Launcher) spawn() SpawnFn {
	if l.Spawn != nil {
		return l.Spawn
	}
	return defaultSpawn
}

// OnRoomEvent est le hook appelé par le connecteur livekit.
// Il traite room_started / participant_joined (déclenche le bot si consentement)
// et room_finished (retire l'entrée de la map).
func (l *Launcher) OnRoomEvent(ctx context.Context, eventType, roomName string) {
	switch eventType {
	case "room_started", "participant_joined":
		l.handleStart(ctx, roomName)
	case "room_finished":
		l.handleFinished(roomName)
	}
}

func (l *Launcher) handleStart(ctx context.Context, slug string) {
	if slug == "" {
		l.logger().Warn("transcription_launcher: roomName vide, ignoré")
		return
	}

	// Idempotence : si un bot tourne déjà pour ce slug, ne pas en lancer un second.
	l.mu.Lock()
	_, already := l.running[slug]
	l.mu.Unlock()
	if already {
		l.logger().Info("transcription_launcher: bot déjà actif", "slug", slug)
		return
	}

	ok, err := l.Salon.ShouldTranscribe(ctx, slug)
	if err != nil {
		l.logger().Warn("transcription_launcher: ShouldTranscribe erreur", "slug", slug, "err", err.Error())
		return
	}
	if !ok {
		l.logger().Info("transcription_launcher: consentement absent, aucun spawn", "slug", slug)
		return
	}

	salonID, err := l.Salon.GetSalonID(ctx, slug)
	if err != nil {
		l.logger().Warn("transcription_launcher: GetSalonID erreur", "slug", slug, "err", err.Error())
		return
	}

	tr, err := l.Transcript.CreateTranscript(ctx, salonID)
	if err != nil {
		l.logger().Warn("transcription_launcher: CreateTranscript erreur", "slug", slug, "err", err.Error())
		return
	}

	if err := os.MkdirAll(l.OutDir, 0o700); err != nil {
		l.logger().Warn("transcription_launcher: MkdirAll outDir", "out_dir", l.OutDir, "err", err.Error())
	}

	env := []string{
		"LIVEKIT_URL=" + l.LiveKitURL,
		"LIVEKIT_API_KEY=" + l.LiveKitAPIKey,
		"LIVEKIT_API_SECRET=" + l.LiveKitAPISecret,
	}

	stop, err := l.spawn()(ctx, tr.ID, slug, salonID, l.OutDir, l.TranscriberBin, env)
	if err != nil {
		l.logger().Warn("transcription_launcher: spawn erreur", "slug", slug, "transcript_id", tr.ID, "err", err.Error())
		_ = l.Transcript.SetStatus(ctx, tr.ID, "failed")
		return
	}

	l.mu.Lock()
	l.running[slug] = stop
	l.mu.Unlock()

	l.logger().Info("transcription_launcher: bot lancé", "slug", slug, "transcript_id", tr.ID)

	// Goroutine d'attente : après la fin du bot, lance le batch de transcription
	// si un Batcher est configuré ; sinon marque "done" directement.
	outDir := l.OutDir
	transcriptID := tr.ID
	go func() {
		stop()
		l.mu.Lock()
		delete(l.running, slug)
		l.mu.Unlock()

		if l.Batch != nil {
			manifestPath := filepath.Join(outDir, "manifest.json")
			if err := l.Batch.Transcribe(ctx, transcriptID, manifestPath); err != nil {
				l.logger().Error("transcription_launcher: batch erreur",
					"slug", slug, "transcript_id", transcriptID, "err", err)
				// SetStatus("failed") déjà posé par Batch.Transcribe en cas d'erreur.
				return
			}
		} else {
			_ = l.Transcript.SetStatus(ctx, transcriptID, "done")
		}
		l.logger().Info("transcription_launcher: bot terminé", "slug", slug, "transcript_id", transcriptID)
	}()
}

func (l *Launcher) handleFinished(slug string) {
	// Le bot s'auto-termine quand LiveKit le déconnecte.
	// On retire simplement l'entrée de la map si elle existe encore.
	l.mu.Lock()
	delete(l.running, slug)
	l.mu.Unlock()
	l.logger().Info("transcription_launcher: room_finished, map nettoyée", "slug", slug)
}

// defaultSpawn lance le binaire transcriber via exec.CommandContext.
// Il retourne un stopper = cmd.Wait() bloquant (appelé dans une goroutine).
func defaultSpawn(ctx context.Context, transcriptID, slug, salonID, outDir, bin string, env []string) (stop func(), err error) {
	cmd := exec.CommandContext(ctx, bin,
		"--room", slug,
		"--salon", salonID,
		"--transcript", transcriptID,
		"--out", outDir,
	)
	// Secrets LiveKit via env uniquement, jamais en argv.
	// On hérite l'env parent et on ajoute/surcharge les vars LiveKit.
	cmd.Env = append(os.Environ(), env...)
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	// stop = wait bloquant ; la goroutine appelante le tient.
	return func() { _ = cmd.Wait() }, nil
}
