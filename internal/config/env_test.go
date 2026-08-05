package config

import "testing"

// TestCookieSecure garde le dérivé qui pilote l'attribut Secure des cookies de
// session et CSRF : une instance HTTPS doit l'activer, une instance HTTP locale non.
// Régression auditée 2026-06-13 (cookie de session émis sans Secure sur /login).
func TestCookieSecure(t *testing.T) {
	for _, tc := range []struct {
		baseURL string
		want    bool
	}{
		{"https://assokit.example.com", true},
		{"https://example.org/", true},
		{"http://localhost:8080", false},
		{"", false},
	} {
		if got := (Config{BaseURL: tc.baseURL}).CookieSecure(); got != tc.want {
			t.Errorf("CookieSecure(%q) = %v, want %v", tc.baseURL, got, tc.want)
		}
	}
}
