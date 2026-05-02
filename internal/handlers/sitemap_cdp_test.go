//go:build integration_cdp

package handlers_test

import (
	"context"
	"encoding/xml"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	cdprotoruntime "github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
)

type sitemapURLSet struct {
	URLs []sitemapURL `xml:"url"`
}

type sitemapURL struct {
	Loc string `xml:"loc"`
}

func TestSitemapCDPCrawl(t *testing.T) {
	// 1. Vérifier chromium
	chromePath := findChromium(t)
	if chromePath == "" {
		t.Skip("aucun binaire chromium trouvé — test skippé")
	}
	t.Logf("chromium : %s", chromePath)

	// 2. Repo root
	repoRoot := findRepoRoot(t)
	t.Logf("repo root: %s", repoRoot)

	// 3. Build le binaire dans t.TempDir()
	binPath := filepath.Join(t.TempDir(), "assokit-test")
	buildCmd := exec.Command("go", "build", "-o", binPath, "github.com/hazyhaar/assokit/cmd/assokit")
	buildCmd.Dir = repoRoot
	if out, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("build assokit: %v\n%s", err, out)
	}
	t.Logf("binaire build: %s", binPath)

	// 4. DB + config test
	dbPath := filepath.Join(t.TempDir(), "sitemap-cdp.db")
	brandingDir := filepath.Join(repoRoot, "internal/handlers/testdata/branding-minimal")

	// 5. Lancer le binaire en sous-process
	cmd := exec.CommandContext(context.Background(), binPath)
	cmd.Env = append(os.Environ(),
		"PORT=8091",
		"BASE_URL=http://localhost:8091",
		"DB_PATH="+dbPath,
		"BRANDING_DIR="+brandingDir,
		"ADMIN_EMAIL=test@example.test",
		"COOKIE_SECRET=0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
	)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start binary: %v", err)
	}
	defer func() {
		if cmd.Process != nil {
			cmd.Process.Kill()
			cmd.Wait()
		}
	}()

	// Poll jusqu'à ready (max 5s)
	ready := false
	for i := 0; i < 50; i++ {
		resp, err := http.Get("http://localhost:8091/")
		if err == nil && resp.StatusCode == 200 {
			ready = true
			resp.Body.Close()
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !ready {
		t.Fatal("server n'a pas démarré dans le temps")
	}

	// 6. Récupérer sitemap.xml
	resp, err := http.Get("http://localhost:8091/sitemap.xml")
	if err != nil {
		t.Fatalf("GET sitemap: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var set sitemapURLSet
	if err := xml.Unmarshal(body, &set); err != nil {
		t.Fatalf("parse sitemap: %v", err)
	}
	if len(set.URLs) == 0 {
		t.Fatal("sitemap vide — au moins / doit être listé")
	}
	t.Logf("sitemap contient %d URLs", len(set.URLs))

	// 7. Chromium headless
	allocOpts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.ExecPath(chromePath),
		chromedp.Flag("headless", true),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-dev-shm-usage", true),
	)
	allocCtx, allocCancel := chromedp.NewExecAllocator(context.Background(), allocOpts...)
	defer allocCancel()
	browserCtx, browserCancel := chromedp.NewContext(allocCtx)
	defer browserCancel()
	ctx, cancel := context.WithTimeout(browserCtx, 60*time.Second)
	defer cancel()

	// 6. Capturer les erreurs JS console
	type jsError struct {
		URL string
		Msg string
	}
	var jsErrors []jsError
	var mu sync.Mutex
	chromedp.ListenTarget(ctx, func(ev interface{}) {
		switch e := ev.(type) {
		case *cdprotoruntime.EventExceptionThrown:
			mu.Lock()
			defer mu.Unlock()
			url := ""
			if e.ExceptionDetails != nil && e.ExceptionDetails.URL != "" {
				url = e.ExceptionDetails.URL
			}
			msg := ""
			if e.ExceptionDetails != nil && e.ExceptionDetails.Exception != nil {
				msg = e.ExceptionDetails.Exception.Description
			}
			if msg != "" {
				jsErrors = append(jsErrors, jsError{URL: url, Msg: msg})
			}
		}
	})

	// 9. Visiter chaque URL du sitemap
	for _, u := range set.URLs {
		targetURL := u.Loc

		var title string
		var bodyText string
		err := chromedp.Run(ctx,
			chromedp.Navigate(targetURL),
			chromedp.WaitReady("body", chromedp.ByQuery),
			chromedp.Title(&title),
			chromedp.Text("body", &bodyText, chromedp.ByQuery),
		)
		if err != nil {
			t.Errorf("URL %s: chromedp run error: %v", targetURL, err)
			continue
		}

		// Assertions
		if title == "" {
			t.Errorf("URL %s: title vide", targetURL)
		}
		if strings.Contains(strings.ToLower(bodyText), "panic:") ||
			strings.Contains(strings.ToLower(bodyText), "internal server error") ||
			strings.Contains(strings.ToLower(bodyText), "erreur 500") {
			t.Errorf("URL %s: page contient un message d'erreur visible", targetURL)
		}
	}

	// 10. Reporter les erreurs JS
	for _, je := range jsErrors {
		t.Errorf("JS error sur %s: %s", je.URL, je.Msg)
	}

	t.Logf("sitemap crawl OK — %d URLs visitées sans erreur critique", len(set.URLs))
}

func findRepoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	dir := filepath.Dir(file)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod introuvable")
		}
		dir = parent
	}
}

func findChromium(t *testing.T) string {
	t.Helper()
	candidates := []string{
		"/snap/bin/chromium",
		"/usr/bin/chromium",
		"/usr/bin/chromium-browser",
		"/usr/bin/google-chrome",
		"/usr/bin/google-chrome-stable",
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return ""
}
