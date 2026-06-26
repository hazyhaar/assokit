package listing

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hazyhaar/assokit/pkg/uid"
)

// ErrOwnerRequired est retourné quand un Listing est créé sans OwnerID.
var ErrOwnerRequired = errors.New("listing: owner_id requis")

// ErrNotFound est retourné quand aucun listing ne correspond à l'id.
var ErrNotFound = errors.New("listing: introuvable")

// ErrNotOwner est retourné quand un appelant tente d'éditer le listing d'un autre.
var ErrNotOwner = errors.New("listing: action réservée au propriétaire")

// ErrIllegalTransition est retourné par SetStatus pour une transition non autorisée.
var ErrIllegalTransition = errors.New("listing: transition de statut illégale")

// Status représente l'état d'un listing.
type Status string

const (
	StatusDraft     Status = "draft"
	StatusPublished Status = "published"
	StatusSold      Status = "sold"
	StatusArchived  Status = "archived"
)

// Listing est l'unité métier du marketplace. Aucun champ spécifique à un vertical.
// Les attributs métier (marque, longueur, etc.) vivent dans Attrs.
type Listing struct {
	ID         string // uid.New() — UUIDv7, trié par création
	Silo       string // identifiant du vertical (ex. "yachts")
	OwnerID    string // identité du vendeur (identity.User.ID)
	Title      string // titre plein texte (indexé FTS5)
	Body       string // description longue (indexée FTS5)
	PriceCents int64  // prix en centimes (0 = non renseigné)
	Status     Status
	Attrs      map[string]any // attributs métier flexibles (JSON)
	Medias     []string       // refs médias ordonnées (URLs/paths) — pas de pipeline upload en P1
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// Store est le point d'accès unique au stockage des listings.
// Single-writer : ne pas partager entre goroutines en écriture concurrente.
type Store struct {
	db   *sql.DB
	silo *SiloDef
}

// NewStore ouvre (ou crée) le schéma listings pour un silo donné.
// db doit être une connexion SQLite ouverte avec modernc.org/sqlite.
func NewStore(ctx context.Context, db *sql.DB, silo *SiloDef) (*Store, error) {
	// Single-writer discipline imposée côté store, pas côté DSN appelant : une
	// seule connexion sérialise écritures et curseurs, supprimant le SQLITE_BUSY
	// inter-connexion (curseur lecteur de refreshFacetCounts vs upsert écrivain,
	// aggravé par des Create concurrents). Robuste quel que soit le DSN injecté
	// à la bordure (pilier core tenant-agnostic). Complète la matérialisation du
	// curseur dans refreshFacetCounts (plus aucun read/write entrelacé).
	db.SetMaxOpenConns(1)
	s := &Store{db: db, silo: silo}
	if err := s.migrate(ctx); err != nil {
		return nil, fmt.Errorf("listing.NewStore migrate: %w", err)
	}
	return s, nil
}

// migrate crée la table listings + colonnes GENERATED + FTS5 + index.
// Idempotent : utilise IF NOT EXISTS et ALTER TABLE … ADD COLUMN … IF NOT EXISTS.
func (s *Store) migrate(ctx context.Context) error {
	// Table de base
	_, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS listings (
		id          TEXT NOT NULL PRIMARY KEY,
		silo        TEXT NOT NULL,
		title       TEXT NOT NULL DEFAULT '',
		body        TEXT NOT NULL DEFAULT '',
		price_cents INTEGER NOT NULL DEFAULT 0,
		status      TEXT NOT NULL DEFAULT 'draft',
		attrs       TEXT NOT NULL DEFAULT '{}',
		created_at  TEXT NOT NULL,
		updated_at  TEXT NOT NULL
	)`)
	if err != nil {
		return fmt.Errorf("create listings: %w", err)
	}

	// Colonnes ajoutées après le bootstrap initial (multi-vendeurs + médias).
	// ALTER TABLE … ADD COLUMN idempotent : on tente et on ignore "duplicate column name"
	// (SQLite ne supporte pas ADD COLUMN IF NOT EXISTS avant 3.37).
	for _, addSQL := range []string{
		`ALTER TABLE listings ADD COLUMN owner_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE listings ADD COLUMN medias TEXT NOT NULL DEFAULT '[]'`,
	} {
		if _, err := s.db.ExecContext(ctx, addSQL); err != nil {
			if !strings.Contains(err.Error(), "duplicate column name") {
				return fmt.Errorf("add column: %w", err)
			}
		}
	}

	// Colonnes GENERATED pour chaque attribut déclaré par le silo
	for _, attr := range s.silo.Attrs {
		colType := "TEXT"
		if attr.Kind == "real" {
			colType = "REAL"
		}
		colName := "gen_" + attr.Name
		// SQLite ne supporte pas ADD COLUMN IF NOT EXISTS avant 3.37 — on tente et ignore
		addSQL := fmt.Sprintf(
			`ALTER TABLE listings ADD COLUMN %s %s GENERATED ALWAYS AS (json_extract(attrs,'%s')) STORED`,
			colName, colType, attr.JSONPath,
		)
		if _, err := s.db.ExecContext(ctx, addSQL); err != nil {
			// Ignore "duplicate column name" — déjà existant
			if !strings.Contains(err.Error(), "duplicate column name") {
				return fmt.Errorf("add generated column %s: %w", colName, err)
			}
		}
		if attr.Indexed {
			idxSQL := fmt.Sprintf(
				`CREATE INDEX IF NOT EXISTS idx_listings_%s ON listings(%s)`,
				colName, colName,
			)
			if _, err := s.db.ExecContext(ctx, idxSQL); err != nil {
				return fmt.Errorf("index %s: %w", colName, err)
			}
		}
	}

	// Index sur silo et status (filtres fréquents)
	for _, idx := range []string{
		`CREATE INDEX IF NOT EXISTS idx_listings_silo ON listings(silo)`,
		`CREATE INDEX IF NOT EXISTS idx_listings_status ON listings(status)`,
		`CREATE INDEX IF NOT EXISTS idx_listings_price ON listings(price_cents)`,
		`CREATE INDEX IF NOT EXISTS idx_listings_owner ON listings(owner_id)`,
	} {
		if _, err := s.db.ExecContext(ctx, idx); err != nil {
			return err
		}
	}

	// Table de cache des compteurs de facettes (avant les triggers qui l'utilisent)
	_, err = s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS listing_facet_counts (
		silo        TEXT NOT NULL,
		attr        TEXT NOT NULL,
		value       TEXT NOT NULL,
		count       INTEGER NOT NULL DEFAULT 0,
		updated_at  TEXT NOT NULL,
		PRIMARY KEY (silo, attr, value)
	)`)
	if err != nil {
		return fmt.Errorf("create listing_facet_counts: %w", err)
	}

	// Table FTS5 contentless (externe) sur title+body
	_, err = s.db.ExecContext(ctx, `CREATE VIRTUAL TABLE IF NOT EXISTS listings_fts
		USING fts5(title, body, content=listings, content_rowid=rowid)`)
	if err != nil {
		return fmt.Errorf("create listings_fts: %w", err)
	}

	// Triggers de synchronisation FTS5 (INSERT / UPDATE / DELETE)
	triggers := []string{
		`CREATE TRIGGER IF NOT EXISTS listings_fts_ai
		 AFTER INSERT ON listings BEGIN
		   INSERT INTO listings_fts(rowid, title, body) VALUES (new.rowid, new.title, new.body);
		 END`,
		`CREATE TRIGGER IF NOT EXISTS listings_fts_ad
		 AFTER DELETE ON listings BEGIN
		   INSERT INTO listings_fts(listings_fts, rowid, title, body)
		   VALUES ('delete', old.rowid, old.title, old.body);
		 END`,
		`CREATE TRIGGER IF NOT EXISTS listings_fts_au
		 AFTER UPDATE ON listings BEGIN
		   INSERT INTO listings_fts(listings_fts, rowid, title, body)
		   VALUES ('delete', old.rowid, old.title, old.body);
		   INSERT INTO listings_fts(rowid, title, body) VALUES (new.rowid, new.title, new.body);
		 END`,
	}
	for _, t := range triggers {
		if _, err := s.db.ExecContext(ctx, t); err != nil {
			return fmt.Errorf("trigger: %w", err)
		}
	}

	// Index d'idempotence des inquiries : une seule par (listing, acheteur).
	// La conversation elle-même vit dans pkg/messaging ; cette table ne mémorise
	// que le lien listing↔acheteur↔message-d'ouverture pour ne pas re-créer.
	_, err = s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS listing_inquiries (
		listing_id  TEXT NOT NULL,
		buyer_id    TEXT NOT NULL,
		owner_id    TEXT NOT NULL,
		message_id  TEXT NOT NULL,
		created_at  TEXT NOT NULL,
		PRIMARY KEY (listing_id, buyer_id)
	)`)
	if err != nil {
		return fmt.Errorf("create listing_inquiries: %w", err)
	}

	if err := s.migrateBuyer(ctx); err != nil {
		return err
	}

	if err := s.migrateModeration(ctx); err != nil {
		return err
	}

	return nil
}

// Create insère un nouveau listing et rafraîchit les compteurs de facettes.
func (s *Store) Create(ctx context.Context, l *Listing) error {
	if strings.TrimSpace(l.OwnerID) == "" {
		return ErrOwnerRequired
	}
	if l.ID == "" {
		l.ID = uid.New()
	}
	l.Silo = s.silo.ID
	now := time.Now().UTC()
	l.CreatedAt = now
	l.UpdatedAt = now
	if l.Status == "" {
		l.Status = StatusDraft
	}
	attrsJSON, err := json.Marshal(l.Attrs)
	if err != nil {
		return fmt.Errorf("listing.Create marshal attrs: %w", err)
	}
	mediasJSON, err := marshalMedias(l.Medias)
	if err != nil {
		return fmt.Errorf("listing.Create marshal medias: %w", err)
	}

	_, err = s.db.ExecContext(ctx, `
		INSERT INTO listings (id, silo, owner_id, title, body, price_cents, status, attrs, medias, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		l.ID, l.Silo, l.OwnerID, l.Title, l.Body, l.PriceCents,
		string(l.Status), string(attrsJSON), mediasJSON,
		now.Format(time.RFC3339), now.Format(time.RFC3339),
	)
	if err != nil {
		return fmt.Errorf("listing.Create insert: %w", err)
	}

	return s.refreshFacetCounts(ctx)
}

// Sort énumère les ordres de tri supportés par Search.
type Sort string

const (
	// SortRecent trie par date de création décroissante (comportement par défaut).
	SortRecent Sort = "recent"
	// SortPriceAsc trie par prix croissant.
	SortPriceAsc Sort = "price_asc"
	// SortPriceDesc trie par prix décroissant.
	SortPriceDesc Sort = "price_desc"
	// SortRelevance trie par pertinence FTS5 (rank) quand une requête plein-texte
	// est présente ; dégrade silencieusement vers SortRecent sinon (rank indéfini
	// sans MATCH).
	SortRelevance Sort = "relevance"
)

// Filter regroupe les critères de recherche pour Search.
type Filter struct {
	// Facettes exactes : map[attrName]value (ex. {"marque":"Jeanneau"})
	Facets map[string]string
	// Ranges : map[attrName ou "price"]{Min, Max}
	Ranges map[string]RangeFilter
	// Texte libre (FTS5)
	Text string
	// Owner filtre par propriétaire (vendeur) ; vide = tous.
	Owner string
	// Status filtre par état ; vide = tous.
	Status Status
	// Sort ordonne les résultats ; vide = SortRecent.
	Sort Sort
	// Pagination
	Limit  int
	Offset int
}

// RangeFilter borne min/max sur un attribut numérique.
// Les pointeurs nil signifient « pas de borne ».
type RangeFilter struct {
	Min *float64
	Max *float64
}

// Search effectue une recherche combinée facettes + ranges + FTS5 en une seule requête.
// Les colonnes GENERATED indexées sont utilisées directement par le moteur SQLite.
func (s *Store) Search(ctx context.Context, f Filter) ([]Listing, error) {
	if f.Limit <= 0 {
		f.Limit = 20
	}

	// hidden=0 : exclusion des annonces masquées par la modération, quel que soit
	// leur Status vendeur (un published+hidden est invisible en recherche mais reste
	// published pour son owner). Filtre de modération, distinct du filtre Status.
	where := []string{"l.silo = ?", "l.hidden = 0"}
	args := []any{s.silo.ID}

	if f.Owner != "" {
		where = append(where, "l.owner_id = ?")
		args = append(args, f.Owner)
	}
	if f.Status != "" {
		where = append(where, "l.status = ?")
		args = append(args, string(f.Status))
	}

	// Facettes exactes via colonnes GENERATED
	for _, attr := range s.silo.Attrs {
		if val, ok := f.Facets[attr.Name]; ok && val != "" {
			where = append(where, fmt.Sprintf("l.gen_%s = ?", attr.Name))
			args = append(args, val)
		}
	}

	// Ranges via colonnes GENERATED ou price_cents
	for _, rd := range s.silo.Ranges {
		rf, ok := f.Ranges[rd.Attr]
		if !ok {
			continue
		}
		col := "l.gen_" + rd.Attr
		if rd.Attr == "price" {
			col = "l.price_cents"
		}
		if rf.Min != nil {
			where = append(where, fmt.Sprintf("%s >= ?", col))
			if rd.Attr == "price" {
				args = append(args, int64(*rf.Min))
			} else {
				args = append(args, *rf.Min)
			}
		}
		if rf.Max != nil {
			where = append(where, fmt.Sprintf("%s <= ?", col))
			if rd.Attr == "price" {
				args = append(args, int64(*rf.Max))
			} else {
				args = append(args, *rf.Max)
			}
		}
	}

	// Texte FTS5 : filtrage via une jointure rowid (external content). La jointure
	// — plutôt qu'une sous-requête IN — expose la colonne rank pour le tri par
	// pertinence ; les arguments MATCH précèdent ceux du WHERE, d'où le préfixe.
	hasText := false
	join := ""
	if f.Text != "" {
		if q := sanitizeQuery(f.Text); q != "" {
			hasText = true
			join = " JOIN listings_fts fts ON fts.rowid = l.rowid AND listings_fts MATCH ?"
			args = append([]any{q}, args...)
		}
	}

	whereClause := strings.Join(where, " AND ")

	// ORDER BY paramétré. relevance n'est honoré que si une requête plein-texte est
	// présente (rank n'existe pas sans MATCH) ; sinon on retombe sur recent.
	// l.id en tie-break : l'id est un UUIDv7 monotonique (trié par création), il
	// départage des created_at de même seconde de façon stable et cohérente avec
	// l'ordre de création (created_at n'a qu'une résolution seconde en RFC3339).
	orderBy := "l.created_at DESC, l.id DESC"
	switch f.Sort {
	case SortPriceAsc:
		orderBy = "l.price_cents ASC, l.id DESC"
	case SortPriceDesc:
		orderBy = "l.price_cents DESC, l.id DESC"
	case SortRelevance:
		if hasText {
			orderBy = "fts.rank ASC, l.id DESC"
		}
	}

	args = append(args, f.Limit, f.Offset)

	query := fmt.Sprintf(`
		SELECT l.id, l.silo, l.owner_id, l.title, l.body, l.price_cents, l.status, l.attrs, l.medias, l.created_at, l.updated_at
		FROM listings l%s
		WHERE %s
		ORDER BY %s
		LIMIT ? OFFSET ?
	`, join, whereClause, orderBy)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		if isFTSSyntaxError(err) {
			return []Listing{}, nil
		}
		return nil, fmt.Errorf("listing.Search: %w", err)
	}
	defer rows.Close()

	return scanListings(rows)
}

// FacetCounts retourne les compteurs de facettes mis en cache pour un attribut donné.
func (s *Store) FacetCounts(ctx context.Context, attrName string) (map[string]int, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT value, count FROM listing_facet_counts
		WHERE silo = ? AND attr = ?
		ORDER BY count DESC
	`, s.silo.ID, attrName)
	if err != nil {
		return nil, fmt.Errorf("listing.FacetCounts: %w", err)
	}
	defer rows.Close()

	counts := map[string]int{}
	for rows.Next() {
		var val string
		var cnt int
		if err := rows.Scan(&val, &cnt); err != nil {
			return nil, err
		}
		counts[val] = cnt
	}
	return counts, rows.Err()
}

// refreshFacetCounts recalcule et upsert les compteurs de facettes textuelles.
// Appelé après chaque Create. Linéaire sur le nombre de facettes × distinct values.
func (s *Store) refreshFacetCounts(ctx context.Context) error {
	now := time.Now().UTC().Format(time.RFC3339)
	for _, attr := range s.silo.Attrs {
		if attr.Kind != "text" {
			continue // les facettes numériques n'ont pas de compteur par valeur
		}
		colName := "gen_" + attr.Name
		query := fmt.Sprintf(`
			SELECT %s AS val, COUNT(*) AS cnt
			FROM listings
			WHERE silo = ? AND %s IS NOT NULL
			GROUP BY %s
		`, colName, colName, colName)
		rows, err := s.db.QueryContext(ctx, query, s.silo.ID)
		if err != nil {
			return fmt.Errorf("refreshFacetCounts query %s: %w", attr.Name, err)
		}
		// Drainer entièrement le curseur AVANT tout upsert : aucun curseur lecteur
		// ne reste ouvert pendant une écriture, ce qui supprime le SQLITE_BUSY
		// read/write entrelacé sur le pool de connexions, indépendamment du DSN.
		type facetCount struct {
			val string
			cnt int
		}
		var pending []facetCount
		for rows.Next() {
			var fc facetCount
			if err := rows.Scan(&fc.val, &fc.cnt); err != nil {
				rows.Close()
				return err
			}
			pending = append(pending, fc)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()

		for _, fc := range pending {
			_, err = s.db.ExecContext(ctx, `
				INSERT INTO listing_facet_counts (silo, attr, value, count, updated_at)
				VALUES (?, ?, ?, ?, ?)
				ON CONFLICT(silo, attr, value) DO UPDATE SET count=excluded.count, updated_at=excluded.updated_at
			`, s.silo.ID, attr.Name, fc.val, fc.cnt, now)
			if err != nil {
				return fmt.Errorf("refreshFacetCounts upsert: %w", err)
			}
		}
	}
	return nil
}

func scanListings(rows *sql.Rows) ([]Listing, error) {
	var out []Listing
	for rows.Next() {
		var l Listing
		var attrsRaw, mediasRaw string
		var createdAt, updatedAt string
		var status string
		if err := rows.Scan(&l.ID, &l.Silo, &l.OwnerID, &l.Title, &l.Body, &l.PriceCents, &status, &attrsRaw, &mediasRaw, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		l.Status = Status(status)
		if err := json.Unmarshal([]byte(attrsRaw), &l.Attrs); err != nil {
			return nil, fmt.Errorf("scanListings unmarshal attrs: %w", err)
		}
		medias, err := unmarshalMedias(mediasRaw)
		if err != nil {
			return nil, fmt.Errorf("scanListings unmarshal medias: %w", err)
		}
		l.Medias = medias
		if t, err := time.Parse(time.RFC3339, createdAt); err == nil {
			l.CreatedAt = t
		}
		if t, err := time.Parse(time.RFC3339, updatedAt); err == nil {
			l.UpdatedAt = t
		}
		out = append(out, l)
	}
	if out == nil {
		return []Listing{}, rows.Err()
	}
	return out, rows.Err()
}

// marshalMedias sérialise une liste ordonnée de refs médias en JSON.
// Une liste nil/vide est persistée comme "[]" (jamais "null") pour un round-trip stable.
func marshalMedias(medias []string) (string, error) {
	if medias == nil {
		return "[]", nil
	}
	b, err := json.Marshal(medias)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// unmarshalMedias désérialise la colonne medias en préservant l'ordre.
func unmarshalMedias(raw string) ([]string, error) {
	if raw == "" {
		return nil, nil
	}
	var out []string
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, err
	}
	return out, nil
}

// sanitizeQuery nettoie une query FTS5 pour éviter les erreurs de parsing.
// Pattern répliqué depuis pkg/search (non-exporté là-bas).
func sanitizeQuery(q string) string {
	q = strings.TrimSpace(q)
	if q == "" {
		return ""
	}
	problematic := []string{`"`, `(`, `)`, `*`, `^`, `+`, `AND`, `OR`, `NOT`}
	for _, p := range problematic {
		if strings.Contains(strings.ToUpper(q), p) {
			q = strings.ReplaceAll(q, `"`, `""`)
			return `"` + q + `"`
		}
	}
	return q
}

func isFTSSyntaxError(err error) bool {
	return err != nil && (strings.Contains(err.Error(), "fts5:") || strings.Contains(err.Error(), "syntax error"))
}
