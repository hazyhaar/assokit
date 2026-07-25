package testkit_test

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/hazyhaar/assokit/internal/testkit"
)

// gardedRoute est une route admin gardee par requireAdmin : 200 pour un admin
// authentifie, 403 sinon. Elle sert de preuve que l'auth du helper est reelle.
const gardedRoute = "/admin"

// TestNewAuthedInstance_ClientEstAuthentifie prouve C1+C2 : un GET sur une route
// admin gardee via le client du helper rend 200, tandis que le MEME GET sans
// cookie rend 403 — la session du helper n'est donc jamais contournee.
func TestNewAuthedInstance_ClientEstAuthentifie(t *testing.T) {
	inst := testkit.NewAuthedInstance(t)

	if inst.AdminUserID == "" {
		t.Fatal("AdminUserID vide — le bootstrap admin n'a pas ete resolu")
	}
	if len(inst.Secret) < 32 {
		t.Fatalf("secret d'instance trop court: %d octets", len(inst.Secret))
	}

	// Avec cookie de session admin → 200.
	respAuth, err := inst.Client.Get(inst.Server.URL + gardedRoute)
	if err != nil {
		t.Fatalf("GET %s (authentifie): %v", gardedRoute, err)
	}
	defer respAuth.Body.Close()
	_, _ = io.Copy(io.Discard, respAuth.Body)
	if respAuth.StatusCode != http.StatusOK {
		t.Errorf("GET %s authentifie: status = %d, want 200", gardedRoute, respAuth.StatusCode)
	}

	// Sans cookie (client neuf, ne suivant pas les redirections) → 302 ou 403.
	bare := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	respBare, err := bare.Get(inst.Server.URL + gardedRoute)
	if err != nil {
		t.Fatalf("GET %s (anonyme): %v", gardedRoute, err)
	}
	defer respBare.Body.Close()
	_, _ = io.Copy(io.Discard, respBare.Body)
	if respBare.StatusCode != http.StatusForbidden && respBare.StatusCode != http.StatusFound {
		t.Errorf("GET %s anonyme: status = %d, want 302 ou 403 (auth reelle, non contournee)", gardedRoute, respBare.StatusCode)
	}
}

// TestNewAuthedInstance_CustomDBPathNettoieSidecars prouve C3 : avec un DBPath
// fichier fourni hors t.TempDir, le teardown supprime les sidecars WAL.
func TestNewAuthedInstance_CustomDBPathNettoieSidecars(t *testing.T) {
	// Repertoire hors t.TempDir de l'instance : cree ici, nettoye ici, pour
	// verifier le nettoyage explicite des sidecars par le helper.
	dir := t.TempDir() // TempDir du test courant, distinct de celui de l'instance
	dbPath := filepath.Join(dir, "custom.db")

	// Sous-test imbrique : son t.Cleanup (donc le teardown de l'instance) tourne
	// a la sortie de t.Run, avant l'assertion de purge des sidecars ci-dessous.
	t.Run("instance", func(t *testing.T) {
		inst := testkit.NewAuthedInstance(t, testkit.WithDBPath(dbPath))
		resp, err := inst.Client.Get(inst.Server.URL + gardedRoute)
		if err != nil {
			t.Fatalf("GET %s: %v", gardedRoute, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("status = %d, want 200", resp.StatusCode)
		}
	})

	// Apres le sous-test, le Cleanup de l'instance a tourne : sidecars purges.
	for _, suffix := range []string{"-wal", "-shm"} {
		if _, err := os.Stat(dbPath + suffix); !os.IsNotExist(err) {
			t.Errorf("sidecar %s non supprime au teardown (err=%v)", dbPath+suffix, err)
		}
	}
}
