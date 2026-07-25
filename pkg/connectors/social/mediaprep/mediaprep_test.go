package mediaprep

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestReformatVerticalUnsafePath vérifie que tout chemin malicieux (préfixe '-'
// pris pour une option ffmpeg, ou évasion '..') est rejeté par ErrUnsafePath
// AVANT tout appel à ffmpeg — y compris quand ffmpeg est absent du PATH.
func TestReformatVerticalUnsafePath(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name          string
		in, out, logo string
		wantUnsafe    bool
	}{
		{"in préfixe tiret", "-evil.mp4", "out.mp4", "", true},
		{"in évasion absolue", "../../etc/passwd", "out.mp4", "", true},
		{"in évasion interne", "a/../../b.mp4", "out.mp4", "", true},
		{"out préfixe tiret", "in.mp4", "-x.mp4", "", true},
		{"out évasion", "in.mp4", "../out.mp4", "", true},
		{"logo préfixe tiret", "in.mp4", "out.mp4", "-payload.png", true},
		{"logo évasion", "in.mp4", "out.mp4", "../../logo.png", true},
		{"chemins propres relatifs", "media/clip.mp4", "out.mp4", "", false},
		{"chemins propres avec logo", "media/clip.mp4", "media/out.mp4", "assets/logo.png", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := ReformatVertical(ctx, tc.in, tc.out, tc.logo)
			gotUnsafe := errors.Is(err, ErrUnsafePath)
			if gotUnsafe != tc.wantUnsafe {
				t.Fatalf("ReformatVertical(%q,%q,%q) ErrUnsafePath=%v (err=%v), attendu %v",
					tc.in, tc.out, tc.logo, gotUnsafe, err, tc.wantUnsafe)
			}
			if !tc.wantUnsafe && errors.Is(err, ErrUnsafePath) {
				t.Fatalf("chemin propre rejeté à tort: %v", err)
			}
		})
	}
}

// TestReformatVerticalRealVideo génère une vidéo RÉELLE via ffmpeg (testsrc 16:9),
// la reformate en 9:16 et vérifie les dimensions de sortie via ffprobe. Skippe
// proprement si ffmpeg/ffprobe sont absents du PATH, mais code la commande réelle.
func TestReformatVerticalRealVideo(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg absent du PATH")
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe absent du PATH")
	}
	ctx := context.Background()
	dir := t.TempDir()
	in := filepath.Join(dir, "in.mp4")
	out := filepath.Join(dir, "out.mp4")

	gen := exec.CommandContext(ctx, "ffmpeg", "-f", "lavfi",
		"-i", "testsrc=duration=1:size=1280x720:rate=30", "-y", in)
	if b, err := gen.CombinedOutput(); err != nil {
		t.Fatalf("génération source: %v — %s", err, string(b))
	}

	if err := ReformatVertical(ctx, in, out, ""); err != nil {
		t.Fatalf("ReformatVertical: %v", err)
	}

	w, h := probeDimensions(t, out)
	if w != TargetWidth || h != TargetHeight {
		t.Fatalf("dimensions de sortie = %dx%d, attendu %dx%d", w, h, TargetWidth, TargetHeight)
	}
}

func probeDimensions(t *testing.T, path string) (int, int) {
	t.Helper()
	cmd := exec.Command("ffprobe", "-v", "error",
		"-select_streams", "v:0",
		"-show_entries", "stream=width,height",
		"-of", "csv=s=x:p=0", path)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("ffprobe: %v", err)
	}
	parts := strings.Split(strings.TrimSpace(string(out)), "x")
	if len(parts) != 2 {
		t.Fatalf("sortie ffprobe inattendue: %q", string(out))
	}
	return atoi(t, parts[0]), atoi(t, parts[1])
}

func atoi(t *testing.T, s string) int {
	t.Helper()
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			t.Fatalf("entier invalide: %q", s)
		}
		n = n*10 + int(r-'0')
	}
	return n
}
