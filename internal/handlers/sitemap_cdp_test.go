//go:build integration_cdp

package handlers_test

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chromedp/cdproto/runtime"
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

	// 2. DB + config test
	dbPath := filepath.Join(t.TempDir(), "sitemap-cdp.db")

	// 3. Lancer le binaire en sous-process
	cmd := exec.CommandContext(context.Background(), "/data/GITREMOTE/assokit/dist/assokit-linux-amd64")
	cmd.Env = append(os.Environ(),
		"PORT=8091",
		"BASE_URL=http://localhost:8091",
		"DB_PATH="+dbPath,
		"BRANDING_DIR=/devhoros/nonpossumus/config",
		"ADMIN_EMAIL=test@example.test",
		"COOKIE_SECRET=0123456789abcdef0123456789abcdef",
	)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start binary: %v", err)
	}
	defer cmd.Process.Kill()

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

	// 4. Récupérer sitemap.xml
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

	// 5. Chromium headless
	allocOpts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.ExecPath(chromePath),
		chromedp.Flag("headless", true),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-dev-shm-usage", true),
	)
	allocCtx, allocCancel := chromedp.NewExecAllocator(context.Background(), allocOpts...)
	if allocCtx == nil {
		t.Fatalf("chromedp.NewExecAllocator: %v", err)
	}
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
		case *runtime.EventExceptionThrown:
			mu.Lock()
			defer mu.Unlock()
			url := ""
			if e.ExceptionDetails != nil && e.ExceptionDetails.URL() != "" {
				url = e.ExceptionDetails.URL()
			}
			msg := ""
			if e.ExceptionDetails != nil && e.ExceptionDetails.ExceptionDescription != nil {
				msg = e.ExceptionDetails.ExceptionDescription.Text()
			}
			if msg != "" {
				jsErrors = append(jsErrors, jsError{URL: url, Msg: msg})
			}
		}
	})

	// 7. Visiter chaque URL du sitemap
	for _, u := range set.URLs {
		targetURL := strings.Replace(u.Loc, "http://localhost:8091", "http://localhost:8091", 1)

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

	// 8. Reporter les erreurs JS
	for _, je := range jsErrors {
		t.Errorf("JS error sur %s: %s", je.URL, je.Msg)
	}

	t.Logf("sitemap crawl OK — %d URLs visitées sans erreur critique", len(set.URLs))
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
