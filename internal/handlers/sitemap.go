package handlers

import (
	"encoding/xml"
	"net/http"
	"sync"
	"time"
)

// Sitemap collecte les URLs publiques d'une instance assokit.
type Sitemap struct {
	base    string
	statics []SitemapEntry
	sources []SitemapSource
	mu      sync.RWMutex

	cachedXML   []byte
	cachedUntil time.Time
}

type SitemapEntry struct {
	Loc        string
	LastMod    time.Time
	ChangeFreq string
	Priority   float64
}

type SitemapSource func() []SitemapEntry

func NewSitemap(baseURL string) *Sitemap {
	return &Sitemap{
		base:    baseURL,
		statics: make([]SitemapEntry, 0),
		sources: make([]SitemapSource, 0),
	}
}

func (s *Sitemap) AddStatic(e SitemapEntry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.statics = append(s.statics, e)
}

func (s *Sitemap) AddSource(src SitemapSource) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sources = append(s.sources, src)
}

func (s *Sitemap) generateXML() ([]byte, error) {
	type URL struct {
		Loc        string  `xml:"loc"`
		LastMod    string  `xml:"lastmod,omitempty"`
		ChangeFreq string  `xml:"changefreq,omitempty"`
		Priority   float64 `xml:"priority,omitempty"`
	}

	type URLSet struct {
		XMLName xml.Name `xml:"urlset"`
		Xmlns   string   `xml:"xmlns,attr"`
		URLs    []URL    `xml:"url"`
	}

	set := URLSet{
		Xmlns: "http://www.sitemaps.org/schemas/sitemap/0.9",
		URLs:  make([]URL, 0),
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	// Append statics
	for _, e := range s.statics {
		set.URLs = append(set.URLs, URL{
			Loc:        s.base + e.Loc,
			LastMod:    formatTime(e.LastMod),
			ChangeFreq: e.ChangeFreq,
			Priority:   e.Priority,
		})
	}

	// Append from sources
	for _, src := range s.sources {
		for _, e := range src() {
			set.URLs = append(set.URLs, URL{
				Loc:        s.base + e.Loc,
				LastMod:    formatTime(e.LastMod),
				ChangeFreq: e.ChangeFreq,
				Priority:   e.Priority,
			})
		}
	}

	res, err := xml.MarshalIndent(set, "", "  ")
	if err != nil {
		return nil, err
	}

	return append([]byte(xml.Header), res...), nil
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format("2006-01-02")
}

// Handler renvoie le http.HandlerFunc qui sert /sitemap.xml avec un cache d'une heure.
func (s *Sitemap) Handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.mu.RLock()
		isCached := time.Now().Before(s.cachedUntil) && len(s.cachedXML) > 0
		cache := s.cachedXML
		s.mu.RUnlock()

		if isCached {
			w.Header().Set("Content-Type", "application/xml")
			w.Write(cache)
			return
		}

		res, err := s.generateXML()
		if err != nil {
			http.Error(w, "Error generating sitemap", http.StatusInternalServerError)
			return
		}

		s.mu.Lock()
		s.cachedXML = res
		s.cachedUntil = time.Now().Add(1 * time.Hour)
		s.mu.Unlock()

		w.Header().Set("Content-Type", "application/xml")
		w.Write(res)
	}
}
