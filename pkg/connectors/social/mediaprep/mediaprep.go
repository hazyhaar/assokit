// CLAUDE:SUMMARY Préparation média pour publication verticale 9:16 (réseaux sociaux)
// via le binaire système ffmpeg : recadrage couvrant + incrustation logo optionnelle.
// CLAUDE:WARN ffmpeg est un binaire SYSTÈME (exec.Command), pas une dépendance Go.
// Absence de ffmpeg = ErrFFmpegMissing (les tests skippent proprement).
package mediaprep

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

// Dimensions cibles d'une vidéo verticale plein cadre 9:16 (Reels/Shorts/Stories).
const (
	TargetWidth  = 1080
	TargetHeight = 1920
)

// ErrFFmpegMissing est retourné quand le binaire ffmpeg est introuvable dans le PATH.
var ErrFFmpegMissing = errors.New("mediaprep: ffmpeg introuvable dans le PATH")

// ErrUnsafePath est retourné quand un chemin média est rejeté avant tout exec :
// soit il commence par '-' (interprété par ffmpeg comme une option, injection
// d'argument), soit il évade l'arborescence ('..' ou chemin non stable par Clean).
var ErrUnsafePath = errors.New("mediaprep: chemin non sûr")

// validatePath rejette un chemin avant qu'il n'atteigne l'argv de ffmpeg.
// Un chemin commençant par '-' serait pris pour une option ; un chemin contenant
// un segment '..' ou non stable par filepath.Clean trahit une tentative d'évasion.
func validatePath(name, p string) error {
	if strings.HasPrefix(p, "-") {
		return fmt.Errorf("mediaprep: %s commence par '-': %w", name, ErrUnsafePath)
	}
	if filepath.Clean(p) != p {
		return fmt.Errorf("mediaprep: %s non canonique: %w", name, ErrUnsafePath)
	}
	for _, seg := range strings.Split(p, string(filepath.Separator)) {
		if seg == ".." {
			return fmt.Errorf("mediaprep: %s contient '..': %w", name, ErrUnsafePath)
		}
	}
	return nil
}

// ReformatVertical recadre la vidéo inPath au format vertical 9:16 (1080x1920) et
// l'écrit dans outPath. Le recadrage est « couvrant puis centré » : la source est
// mise à l'échelle pour couvrir le cadre cible (force_original_aspect_ratio=increase)
// puis recadrée au centre — aucune bande noire, le sujet central est préservé.
//
// Si logoPath est non vide, le logo est mis à l'échelle (largeur 200 px, ratio
// conservé) et incrusté en haut à gauche avec une marge de 40 px.
//
// ffmpeg est invoqué via exec.CommandContext : l'annulation du contexte tue le
// process. Retourne ErrFFmpegMissing si ffmpeg est absent du PATH.
func ReformatVertical(ctx context.Context, inPath, outPath, logoPath string) error {
	if inPath == "" || outPath == "" {
		return fmt.Errorf("mediaprep.ReformatVertical: inPath et outPath requis")
	}
	if err := validatePath("inPath", inPath); err != nil {
		return err
	}
	if err := validatePath("outPath", outPath); err != nil {
		return err
	}
	if logoPath != "" {
		if err := validatePath("logoPath", logoPath); err != nil {
			return err
		}
	}
	bin, err := exec.LookPath("ffmpeg")
	if err != nil {
		return ErrFFmpegMissing
	}

	args := buildArgs(inPath, outPath, logoPath)
	cmd := exec.CommandContext(ctx, bin, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("mediaprep.ReformatVertical: ffmpeg: %w — %s", err, string(out))
	}
	return nil
}

// buildArgs construit la ligne d'arguments ffmpeg. Sans logo, un simple -vf ;
// avec logo, un -filter_complex à deux entrées et un overlay.
func buildArgs(inPath, outPath, logoPath string) []string {
	cover := fmt.Sprintf(
		"scale=%d:%d:force_original_aspect_ratio=increase,crop=%d:%d",
		TargetWidth, TargetHeight, TargetWidth, TargetHeight)

	if logoPath == "" {
		return []string{
			"-y",
			"-i", inPath,
			"-vf", cover,
			"-c:a", "copy",
			outPath,
		}
	}

	filter := fmt.Sprintf(
		"[0:v]%s[bg];[1:v]scale=200:-1[logo];[bg][logo]overlay=40:40[out]", cover)
	return []string{
		"-y",
		"-i", inPath,
		"-i", logoPath,
		"-filter_complex", filter,
		"-map", "[out]",
		"-map", "0:a?",
		"-c:a", "copy",
		outPath,
	}
}
