// CLAUDE:SUMMARY Handlers de l'espace éleveur/preneur (V3b) : accueil élevage
// (/account/elevage : conventions + parcelles mises à disposition + redevance en
// lecture seule), déclaration d'entretien (/account/elevage/entretien GET+POST),
// signalement de problème terrain (/account/elevage/probleme GET+POST), export PAC
// RPG (/account/elevage/export-pac.csv) et journal de terrain administration
// (/admin/field-reports). Toutes les vues membre sont rôle-scopées sur le membre
// connecté (requireAuth) ; aucune donnée fabriquée : tout provient des stores réels
// (convention/parcelle/fieldlog), état vide assumé sinon.
package handlers

import (
	"encoding/csv"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/a-h/templ"

	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/internal/webui/views"
	"github.com/hazyhaar/assokit/pkg/convention"
	"github.com/hazyhaar/assokit/pkg/fieldlog"
	"github.com/hazyhaar/assokit/pkg/horui/middleware"
	"github.com/hazyhaar/assokit/pkg/identity"
	"github.com/hazyhaar/assokit/pkg/parcelle"
)

// handleAccountElevage rend l'accueil de l'espace élevage : conventions du preneur,
// parcelles mises à disposition et engagement de redevance (lecture seule). Agrégé
// depuis les stores réels du membre connecté ; aucune donnée détaillée n'est recodée.
func handleAccountElevage(deps app.AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := middleware.UserFromContext(r.Context())
		if u == nil {
			http.Redirect(w, r, "/login?redirect_uri=/account/elevage", http.StatusFound)
			return
		}
		ctx := r.Context()

		convs, err := (&convention.Store{DB: deps.DB}).ListForUser(ctx, u.ID)
		if err != nil {
			deps.Logger.Error("account_elevage_conventions", "err", err.Error())
			http.Error(w, "Erreur interne", http.StatusInternalServerError)
			return
		}
		parcs, err := (&parcelle.Store{DB: deps.DB}).ListForUser(ctx, u.ID)
		if err != nil {
			deps.Logger.Error("account_elevage_parcelles", "err", err.Error())
			http.Error(w, "Erreur interne", http.StatusInternalServerError)
			return
		}

		var surface int64
		for _, p := range parcs {
			surface += p.SurfaceM2
		}
		summary := views.ElevageSummary{
			DisplayName:    u.DisplayName,
			Conventions:    convs,
			Parcelles:      parcs,
			SurfaceTotalM2: surface,
			ExtraCards:     deps.ElevageExtraCards,
		}
		logAccountSelfAccess(r, deps, u.ID)
		renderPageV2(w, r, deps, "Espace élevage", views.ElevagePage(summary))
	}
}

// handleAccountElevageEntretien gère GET (formulaire + entretiens déjà déclarés) et
// POST (enregistrement) sur /account/elevage/entretien. CSRF assuré par le
// middleware global (champ _csrf du formulaire).
func handleAccountElevageEntretien(deps app.AppDeps) http.HandlerFunc {
	return makeFieldLogHandler(deps, fieldlog.KindEntretien,
		"/account/elevage/entretien", "Déclarer un entretien réalisé",
		"Déclaration d'entretien enregistrée.",
		func(parcs []parcelle.Parcelle, entries []fieldlog.Entry, csrf string) templ.Component {
			return views.ElevageEntretienPage(parcs, entries, csrf)
		})
}

// handleAccountElevageProbleme gère GET et POST sur /account/elevage/probleme. Même
// patron que l'entretien (même pkg, même style).
func handleAccountElevageProbleme(deps app.AppDeps) http.HandlerFunc {
	return makeFieldLogHandler(deps, fieldlog.KindProbleme,
		"/account/elevage/probleme", "Signaler un problème terrain",
		"Signalement de problème terrain enregistré.",
		func(parcs []parcelle.Parcelle, entries []fieldlog.Entry, csrf string) templ.Component {
			return views.ElevageProblemePage(parcs, entries, csrf)
		})
}

// makeFieldLogHandler fabrique un handler GET+POST de saisie au journal de terrain,
// commun aux deux variantes (entretien / problème). Le kind est forcé côté serveur :
// le formulaire ne le porte pas, ce qui empêche un membre de croiser les catégories.
func makeFieldLogHandler(
	deps app.AppDeps,
	kind, path, title, successMsg string,
	page func([]parcelle.Parcelle, []fieldlog.Entry, string) templ.Component,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := middleware.UserFromContext(r.Context())
		if u == nil {
			http.Redirect(w, r, "/login?redirect_uri="+path, http.StatusFound)
			return
		}
		ctx := r.Context()
		store := &fieldlog.Store{DB: deps.DB}

		if r.Method == http.MethodPost {
			if err := r.ParseForm(); err != nil {
				http.Error(w, "formulaire invalide", http.StatusBadRequest)
				return
			}
			_, err := store.Create(ctx, fieldlog.Entry{
				UserID:      u.ID,
				Kind:        kind,
				Category:    strings.TrimSpace(r.FormValue("category")),
				ParcelleRef: strings.TrimSpace(r.FormValue("parcelle_ref")),
				EventDate:   strings.TrimSpace(r.FormValue("event_date")),
				Details:     strings.TrimSpace(r.FormValue("details")),
			})
			if err != nil {
				middleware.PushFlash(w, "error", err.Error())
			} else {
				middleware.PushFlash(w, "success", successMsg)
			}
			http.Redirect(w, r, path, http.StatusSeeOther)
			return
		}

		parcs, err := (&parcelle.Store{DB: deps.DB}).ListForUser(ctx, u.ID)
		if err != nil {
			deps.Logger.Error("account_elevage_fieldlog_parcelles", "err", err.Error())
			http.Error(w, "Erreur interne", http.StatusInternalServerError)
			return
		}
		entries, err := store.ListForUserKind(ctx, u.ID, kind)
		if err != nil {
			deps.Logger.Error("account_elevage_fieldlog_list", "err", err.Error())
			http.Error(w, "Erreur interne", http.StatusInternalServerError)
			return
		}
		csrfToken := middleware.CSRFToken(ctx)
		logAccountSelfAccess(r, deps, u.ID)
		renderPageV2(w, r, deps, title, page(parcs, entries, csrfToken))
	}
}

// handleAccountElevageExportPAC produit un export CSV des parcelles exploitées par
// le membre connecté (déclaration PAC / Registre Parcellaire Graphique). Données
// réelles issues du store parcelle ; aucune parcelle fabriquée. Un membre sans
// parcelle obtient un CSV à en-tête seul (état vide assumé).
func handleAccountElevageExportPAC(deps app.AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := middleware.UserFromContext(r.Context())
		if u == nil {
			http.Redirect(w, r, "/login?redirect_uri=/account/elevage", http.StatusFound)
			return
		}
		ctx := r.Context()

		parcs, err := (&parcelle.Store{DB: deps.DB}).ListForUser(ctx, u.ID)
		if err != nil {
			deps.Logger.Error("account_elevage_export_pac", "err", err.Error())
			http.Error(w, "Erreur interne", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition",
			fmt.Sprintf(`attachment; filename="export-pac-%s.csv"`, time.Now().UTC().Format("20060102-1504")))
		// UTF-8 BOM pour Excel/LibreOffice France (patron donations_export).
		w.Write([]byte{0xEF, 0xBB, 0xBF}) //nolint:errcheck

		writer := csv.NewWriter(w)
		defer writer.Flush()

		header := []string{"commune_code", "section", "numero_parcelle", "surface_m2", "surface_ha", "statut_mad"}
		if err := writer.Write(header); err != nil {
			deps.Logger.Error("account_elevage_export_pac_header", "err", err.Error())
			return
		}
		for _, p := range parcs {
			ha := strconv.FormatFloat(float64(p.SurfaceM2)/10000, 'f', 4, 64)
			row := []string{
				sanitizeCSVField(p.CommuneCode), sanitizeCSVField(p.Section), sanitizeCSVField(p.NumeroParcelle),
				strconv.FormatInt(p.SurfaceM2, 10), ha, sanitizeCSVField(p.StatutMad),
			}
			if err := writer.Write(row); err != nil {
				deps.Logger.Error("account_elevage_export_pac_row", "err", err.Error())
				return
			}
		}
		logAccountSelfAccess(r, deps, u.ID)
		deps.Logger.Info("account_elevage_export_pac",
			"actor_user_id", u.ID, "rows_count", len(parcs))
	}
}

// sanitizeCSVField neutralise l'injection de formule (formula injection) dans les
// exports CSV ouverts par Excel/LibreOffice. Une cellule dont la valeur commence
// par '=', '+', '-' ou '@' (éventuellement après des espaces, tabulations ou
// retours-chariot) peut être interprétée comme une formule. Le préfixe d'une
// apostrophe force le tableur à traiter la cellule comme du texte littéral.
func sanitizeCSVField(s string) string {
	trimmed := strings.TrimLeft(s, " \t\r\n")
	if trimmed == "" {
		return s
	}
	switch trimmed[0] {
	case '=', '+', '-', '@':
		return "'" + s
	default:
		return s
	}
}

// handleAdminFieldReports affiche le journal de terrain consolidé (vue
// administration), via fieldlog.Store.ListAll. Gating admin assuré par requireAdmin
// sur la route.
func handleAdminFieldReports(deps app.AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		entries, err := (&fieldlog.Store{DB: deps.DB}).ListAll(r.Context(), "")
		if err != nil {
			deps.Logger.Error("admin_field_reports", "err", err.Error())
			http.Error(w, "Erreur interne", http.StatusInternalServerError)
			return
		}
		// Résolution UserID → nom affichable : la colonne « Membre » ne doit pas
		// exposer un UUID brut. Le store fieldlog ne porte que l'identifiant ; le nom
		// est lu par lot via identity, mémoïsé pour éviter une requête par ligne. Un
		// compte introuvable (supprimé) retombe sur l'identifiant, jamais sur vide.
		idStore := &identity.Store{DB: deps.DB}
		names := make(map[string]string, len(entries))
		rows := make([]views.FieldReportRow, 0, len(entries))
		for _, e := range entries {
			name, ok := names[e.UserID]
			if !ok {
				name = e.UserID
				if u, uerr := idStore.GetByID(r.Context(), e.UserID); uerr == nil && u.DisplayName != "" {
					name = u.DisplayName
				}
				names[e.UserID] = name
			}
			rows = append(rows, views.FieldReportRow{Entry: e, DisplayName: name})
		}
		renderPageV2(w, r, deps, "Journal de terrain", views.AdminFieldReportsPage(rows))
	}
}
