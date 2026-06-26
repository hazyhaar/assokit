// CLAUDE:SUMMARY Moteur batch whisper-cli : lit le manifest, transcrit chaque piste WAV,
// persiste les segments, purge les WAV. Entièrement CGO_ENABLED=0 (whisper via sous-process).
package transcription

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// manifestDTO est la struct de lecture du manifest.json produit par le bot transcriber.
// Définie ici (CGO=0) pour éviter d'importer le package transcriber (CGO-tagged).
type manifestDTO struct {
	Room      string            `json:"room"`
	Salon     string            `json:"salon"`
	StartedAt string            `json:"started_at"`
	EndedAt   string            `json:"ended_at"`
	Speakers  []manifestSpeaker `json:"speakers"`
}

type manifestSpeaker struct {
	Identity string `json:"identity"`
	WavPath  string `json:"wav_path"`
}

// whisperOutput est la struct de parsing de la sortie JSON de whisper-cli.
// Schéma observé au sol sur .79 (ggml-small, -oj) :
//
//	{
//	  "transcription": [
//	    { "offsets": {"from": 0, "to": 3000}, "text": "..." },
//	    ...
//	  ]
//	}
//
// Les offsets "from"/"to" sont en MILLISECONDES.
type whisperOutput struct {
	Transcription []whisperSegment `json:"transcription"`
}

type whisperSegment struct {
	Offsets struct {
		From int64 `json:"from"`
		To   int64 `json:"to"`
	} `json:"offsets"`
	Text string `json:"text"`
}

// BatchStore est l'interface côté consommateur pour la persistance des segments.
// Définie ici pour rester découplée du producteur pkg/transcript.
type BatchStore interface {
	SetStatus(ctx context.Context, id, status string) error
	AddSegment(ctx context.Context, transcriptID, speaker string, t0ms, t1ms int64, text string) error
}

// Batcher exécute le pipeline batch post-enregistrement.
type Batcher struct {
	Store  BatchStore
	Logger *slog.Logger
	// WhisperBin : chemin du binaire whisper-cli. Défaut : "whisper-cli".
	// Lire depuis env ASSOKIT_WHISPER_BIN à la construction (bordure).
	WhisperBin string
	// WhisperModel : chemin du modèle ggml. Défaut : /opt/whisper.cpp/models/ggml-small.bin.
	WhisperModel string

	// RunWhisper est injecté en test pour fournir une fixture réelle sans lancer
	// le binaire. Si nil, defaultRunWhisper est utilisé.
	// Ce n'est PAS de la donnée mock : le test consomme un JSON whisper réel en fixture.
	RunWhisper func(ctx context.Context, wavPath, outPrefix, model, bin string) error
}

// NewBatcher construit un Batcher avec les valeurs d'env par défaut.
func NewBatcher(store BatchStore, logger *slog.Logger) *Batcher {
	bin := os.Getenv("ASSOKIT_WHISPER_BIN")
	if bin == "" {
		bin = "whisper-cli"
	}
	model := os.Getenv("ASSOKIT_WHISPER_MODEL")
	if model == "" {
		model = "/opt/whisper.cpp/models/ggml-small.bin"
	}
	return &Batcher{
		Store:        store,
		Logger:       logger,
		WhisperBin:   bin,
		WhisperModel: model,
	}
}

func (b *Batcher) logger() *slog.Logger {
	if b.Logger != nil {
		return b.Logger
	}
	return slog.Default()
}

func (b *Batcher) runWhisper() func(ctx context.Context, wavPath, outPrefix, model, bin string) error {
	if b.RunWhisper != nil {
		return b.RunWhisper
	}
	return defaultRunWhisper
}

// Transcribe lit le manifest.json, transcrit chaque piste WAV via whisper-cli,
// persiste les segments dans le store, puis purge les WAV et fichiers JSON whisper.
// En cas d'échec sur une piste, le transcript est marqué "failed" et les WAV sont
// conservés pour diagnostic.
func (b *Batcher) Transcribe(ctx context.Context, transcriptID, manifestPath string) error {
	// Lecture du manifest.
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		return fmt.Errorf("batch.Transcribe: lecture manifest: %w", err)
	}
	var mf manifestDTO
	if err := json.Unmarshal(raw, &mf); err != nil {
		return fmt.Errorf("batch.Transcribe: parse manifest: %w", err)
	}

	if err := b.Store.SetStatus(ctx, transcriptID, "transcribing"); err != nil {
		return fmt.Errorf("batch.Transcribe: SetStatus transcribing: %w", err)
	}

	// Répertoire de travail whisper = même dossier que le manifest.
	outDir := filepath.Dir(manifestPath)

	var whisperJSONs []string // fichiers .json whisper produits, à purger après succès.
	var wavPaths []string     // WAV à purger après succès.

	for _, sp := range mf.Speakers {
		wavPath := sp.WavPath
		if !filepath.IsAbs(wavPath) {
			wavPath = filepath.Join(outDir, wavPath)
		}
		wavPaths = append(wavPaths, wavPath)

		// Préfixe de sortie whisper : même dossier, même basename sans .wav.
		base := strings.TrimSuffix(filepath.Base(wavPath), ".wav")
		outPrefix := filepath.Join(outDir, base+"_whisper")
		whisperJSON := outPrefix + ".json"
		whisperJSONs = append(whisperJSONs, whisperJSON)

		if err := b.runWhisper()(ctx, wavPath, outPrefix, b.WhisperModel, b.WhisperBin); err != nil {
			b.logger().Error("batch.Transcribe: whisper-cli erreur",
				"speaker", sp.Identity, "wav", wavPath, "err", err)
			// Marque failed, conserve les WAV pour diagnostic.
			_ = b.Store.SetStatus(ctx, transcriptID, "failed")
			return fmt.Errorf("batch.Transcribe: whisper %s: %w", sp.Identity, err)
		}

		segs, err := parseWhisperJSON(whisperJSON)
		if err != nil {
			b.logger().Error("batch.Transcribe: parse whisper JSON",
				"speaker", sp.Identity, "file", whisperJSON, "err", err)
			_ = b.Store.SetStatus(ctx, transcriptID, "failed")
			return fmt.Errorf("batch.Transcribe: parseWhisperJSON %s: %w", sp.Identity, err)
		}

		for _, seg := range segs {
			text := strings.TrimSpace(seg.Text)
			if text == "" {
				continue
			}
			if err := b.Store.AddSegment(ctx, transcriptID, sp.Identity,
				seg.Offsets.From, seg.Offsets.To, text); err != nil {
				b.logger().Error("batch.Transcribe: AddSegment",
					"speaker", sp.Identity, "err", err)
				_ = b.Store.SetStatus(ctx, transcriptID, "failed")
				return fmt.Errorf("batch.Transcribe: AddSegment: %w", err)
			}
		}
	}

	// Toutes les pistes traitées avec succès.
	if err := b.Store.SetStatus(ctx, transcriptID, "done"); err != nil {
		return fmt.Errorf("batch.Transcribe: SetStatus done: %w", err)
	}

	// Purge : WAV + JSON whisper.
	for i, wav := range wavPaths {
		if err := os.Remove(wav); err != nil && !os.IsNotExist(err) {
			b.logger().Warn("batch.Transcribe: purge WAV", "path", wav, "err", err)
		}
		if i < len(whisperJSONs) {
			if err := os.Remove(whisperJSONs[i]); err != nil && !os.IsNotExist(err) {
				b.logger().Warn("batch.Transcribe: purge whisper JSON",
					"path", whisperJSONs[i], "err", err)
			}
		}
	}

	b.logger().Info("batch.Transcribe: done", "transcript_id", transcriptID,
		"speakers", len(mf.Speakers))
	return nil
}

// parseWhisperJSON lit et désérialise la sortie JSON de whisper-cli.
func parseWhisperJSON(path string) ([]whisperSegment, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("parseWhisperJSON: %w", err)
	}
	var out whisperOutput
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("parseWhisperJSON: unmarshal: %w", err)
	}
	return out.Transcription, nil
}

// defaultRunWhisper invoque whisper-cli en sous-process.
// Sortie : <outPrefix>.json (champ -of, flag -oj).
func defaultRunWhisper(ctx context.Context, wavPath, outPrefix, model, bin string) error {
	cmd := exec.CommandContext(ctx, bin,
		"-m", model,
		"-f", wavPath,
		"-l", "fr",
		"-oj",
		"-of", outPrefix,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("whisper-cli: %w: %s", err, string(out))
	}
	return nil
}
