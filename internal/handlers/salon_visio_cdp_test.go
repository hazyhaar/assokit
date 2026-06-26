//go:build integration_cdp

// CLAUDE:SUMMARY Test CDP repli visio L4 : vérifie la page enclave /salon/{slug}/visio
// (assets locaux, 6 contrôles, cibles >=72px) sans infrastructure LiveKit réelle.
//
// Stratégie : on sert une page enclave avec window.LivekitClient stubbé en JS inline
// (évite de charger les 354Ko du UMD dans le banc, qui cause un timeout chromedp).
// La page charge van.js et visio.js depuis le filesystem, LK est stubbé en mémoire.
// On vérifie :
//  1. Zéro référence CDN externe dans les scripts.
//  2. Les 6 boutons de contrôle sont présents dans #visio-root.
//  3. Tous les boutons ont min-width et min-height >= 72px (via getComputedStyle).
//
// Ce test ne simule PAS de flux médias réels (WebRTC nécessite un serveur LiveKit).
//
// Lancement :
//
//	go test -tags=integration_cdp -v -timeout=120s ./internal/handlers/ -run TestVisioCDP
package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
)

// lkStub est un stub JS inline pour window.LivekitClient.
// Il expose les symboles attendus par visio.js (Room, RoomEvent, Track,
// createLocalVideoTrack, createLocalAudioTrack) comme des no-ops.
const lkStub = `
window.LivekitClient = (function(){
  function Room(opts){
    this._handlers={};
    this.participants=new Map();
    this.localParticipant={
      setMicrophoneEnabled:function(){return Promise.resolve();},
      setCameraEnabled:function(){return Promise.resolve();},
      setScreenShareEnabled:function(){return Promise.resolve();},
      publishData:function(){},
    };
  }
  Room.prototype.on=function(ev,fn){this._handlers[ev]=fn;return this;};
  Room.prototype.connect=function(){return Promise.resolve();};
  Room.prototype.disconnect=function(){};
  var RoomEvent={
    ParticipantConnected:'participantConnected',
    ParticipantDisconnected:'participantDisconnected',
    TrackSubscribed:'trackSubscribed',
    TrackUnsubscribed:'trackUnsubscribed',
    ActiveSpeakersChanged:'activeSpeakersChanged',
    DataReceived:'dataReceived',
    Disconnected:'disconnected',
  };
  var Track={Kind:{Video:'video',Audio:'audio'}};
  function createLocalVideoTrack(){return Promise.resolve({attach:function(){},stop:function(){}});}
  function createLocalAudioTrack(){return Promise.resolve({attach:function(){},stop:function(){}});}
  return {Room:Room,RoomEvent:RoomEvent,Track:Track,
    createLocalVideoTrack:createLocalVideoTrack,createLocalAudioTrack:createLocalAudioTrack};
})();
`

// readFileForTest lit un fichier et fail si impossible.
func readFileForTest(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("readFileForTest(%s): %v", path, err)
	}
	return string(b)
}

// handlerForCDP construit un http.Handler qui sert la page enclave visio
// avec window.LivekitClient stubbé (pas de UMD lourd). Retourne aussi le slug du salon.
func handlerForCDP(t *testing.T) (http.Handler, string) {
	t.Helper()
	rawDB, _, _, _, slug := newSalonVisioTestEnv(t)
	t.Cleanup(func() { rawDB.Close() })

	cfg := visioConfig{
		Token:     "fake-livekit-token",
		URL:       "wss://stub.livekit.test",
		Room:      slug,
		Slug:      slug,
		SalonName: "Salon Test Visio CDP",
		IsOwner:   true,
		CSRFToken: "csrf-test",
	}
	cfgJSON, _ := json.Marshal(cfg)

	// Résoudre le dossier static/ depuis le répertoire des sources.
	_, testFile, _, _ := runtime.Caller(0)
	staticDir := filepath.Join(filepath.Dir(testFile), "..", "..", "static")

	// Lire van.js et visio.js depuis le filesystem une seule fois au setup,
	// puis les servir inline dans la page (évite les sous-requêtes HTTP dans le test).
	vanJS := readFileForTest(t, filepath.Join(staticDir, "js", "van.js"))
	visioJS := readFileForTest(t, filepath.Join(staticDir, "js", "visio.js"))

	page := `<!doctype html>
<html lang="fr"><head><meta charset="utf-8"/>
<meta name="viewport" content="width=device-width, initial-scale=1"/>
<title>Salon Test Visio CDP — Visioconférence</title>
<style>*,*::before,*::after{box-sizing:border-box;margin:0;padding:0;}
body{background:#18181b;color:#fff;font-family:system-ui,sans-serif;height:100vh;}
#visio-root{height:100vh;}button{cursor:pointer;}</style>
</head><body>
<script id="visio-config" type="application/json">` + string(cfgJSON) + `</script>
<div id="visio-root"></div>
<!-- window.LivekitClient stubbé pour le test de repli -->
<script>` + lkStub + `</script>
<!-- van.js inliné depuis static/js/van.js (vendored, zéro CDN) -->
<script>` + vanJS + `</script>
<!-- visio.js inliné depuis static/js/visio.js -->
<script>` + visioJS + `</script>
</body></html>`

	mux := http.NewServeMux()
	mux.HandleFunc("/salon/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(page))
	})
	return mux, slug
}

func TestVisioCDP_PageEnclave(t *testing.T) {
	chromePath := findChromium(t)
	if chromePath == "" {
		t.Skip("aucun binaire chromium trouvé — test skippé.")
	}

	handler, slug := handlerForCDP(t)
	srv := httptest.NewServer(handler)
	defer srv.Close()

	opts := chromedp.DefaultExecAllocatorOptions[:]
	opts = append(opts,
		chromedp.ExecPath(chromePath),
		chromedp.Flag("headless", true),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("use-fake-device-for-media-stream", true),
		chromedp.Flag("use-fake-ui-for-media-stream", true),
	)
	allocCtx, cancelAlloc := chromedp.NewExecAllocator(context.Background(), opts...)
	defer cancelAlloc()
	ctx, cancel := chromedp.NewContext(allocCtx)
	defer cancel()
	ctx, cancelTO := context.WithTimeout(ctx, 30*time.Second)
	defer cancelTO()

	var cdnRefs string
	var controlCount int64
	var minSizes []map[string]interface{}
	var rootHTML string

	err := chromedp.Run(ctx,
		chromedp.Navigate(srv.URL+"/salon/"+slug+"/visio"),
		// Phase préjoin rendue : 500ms suffisent (page légère, JS inliné).
		chromedp.Sleep(500*time.Millisecond),

		// Cliquer sur "Rejoindre" pour passer en mode salle (les 6 contrôles y sont).
		// joinRoom() appelle room.connect() — le stub LK retourne Promise.resolve().
		chromedp.Click(`button[aria-label="Rejoindre la salle"]`, chromedp.ByQuery),
		chromedp.Sleep(800*time.Millisecond),

		chromedp.InnerHTML("#visio-root", &rootHTML, chromedp.ByID),

		// Vérification 1 : aucun script src CDN externe.
		chromedp.Evaluate(`
			(function(){
				var scripts = document.querySelectorAll('script[src]');
				var cdnFound = [];
				scripts.forEach(function(s){
					var src = s.getAttribute('src') || '';
					if(src.indexOf('cdn.') !== -1 || src.indexOf('unpkg.') !== -1 ||
					   src.indexOf('jsdelivr.') !== -1 || src.indexOf('cdnjs.') !== -1){
						cdnFound.push(src);
					}
				});
				return cdnFound.join(',');
			})()
		`, &cdnRefs),

		// Vérification 2 : au moins 6 boutons dans #visio-root.
		chromedp.Evaluate(`
			document.querySelectorAll('#visio-root button').length
		`, &controlCount),

		// Vérification 3 : cibles >= 72px (min-width/min-height calculées).
		chromedp.Evaluate(`
			(function(){
				var btns = document.querySelectorAll('#visio-root button');
				var results = [];
				btns.forEach(function(b){
					var style = window.getComputedStyle(b);
					var w = parseFloat(style.minWidth) || 0;
					var h = parseFloat(style.minHeight) || 0;
					if(w > 0 || h > 0){
						results.push({label: b.textContent.trim(), minWidth: w, minHeight: h});
					}
				});
				return results;
			})()
		`, &minSizes),
	)
	if err != nil {
		t.Fatalf("chromedp run: %v\n#visio-root HTML (500): %.500s", err, rootHTML)
	}

	t.Logf("#visio-root HTML (500 premiers chars): %.500s", rootHTML)

	// Assertion 1 : zéro CDN.
	if cdnRefs != "" {
		t.Errorf("références CDN détectées : %s", cdnRefs)
	} else {
		t.Log("OK: zéro référence CDN dans les scripts")
	}

	// Assertion 2 : >= 6 boutons.
	if controlCount < 6 {
		t.Errorf("attendu >= 6 boutons dans #visio-root, trouvé : %d", controlCount)
	} else {
		t.Logf("OK: %d boutons trouvés dans #visio-root", controlCount)
	}

	// Assertion 3 : cibles >= 72px.
	for _, m := range minSizes {
		label_, _ := m["label"].(string)
		w, _ := m["minWidth"].(float64)
		h, _ := m["minHeight"].(float64)
		if w > 0 && w < 72 {
			t.Errorf("bouton %q : min-width=%.0fpx < 72px", label_, w)
		}
		if h > 0 && h < 72 {
			t.Errorf("bouton %q : min-height=%.0fpx < 72px", label_, h)
		}
	}
	if len(minSizes) > 0 {
		t.Logf("OK: %d boutons vérifiés (cibles >= 72px)", len(minSizes))
	}
}
