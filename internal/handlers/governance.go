// CLAUDE:SUMMARY Handlers UI gouvernance associative — socle réunion (V2a) : admin
// (liste des assemblées + formulaire de création + déclenchement de la convocation).
package handlers

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/internal/webui/views"
	"github.com/hazyhaar/assokit/pkg/actions/seeds"
	"github.com/hazyhaar/assokit/pkg/governance"
	"github.com/hazyhaar/assokit/pkg/horui/middleware"
	"github.com/hazyhaar/assokit/pkg/identity"
	"github.com/hazyhaar/assokit/pkg/membership"
)

// handleAdminGovernance gère GET (liste + formulaire) et POST (création) sur
// /admin/governance. Gating admin assuré par requireAdmin sur la route.
func handleAdminGovernance(deps app.AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		store := &governance.Store{DB: deps.DB}
		if r.Method == http.MethodPost {
			if err := r.ParseForm(); err != nil {
				http.Error(w, "formulaire invalide", http.StatusBadRequest)
				return
			}
			_, err := store.Create(r.Context(), governance.Assembly{
				Name:        strings.TrimSpace(r.FormValue("name")),
				Agenda:      r.FormValue("agenda"),
				ScheduledAt: r.FormValue("scheduled_at"),
			})
			if err != nil {
				middleware.PushFlash(w, "error", err.Error())
			} else {
				middleware.PushFlash(w, "success", "Assemblée générale créée.")
			}
			http.Redirect(w, r, "/admin/governance", http.StatusSeeOther)
			return
		}

		list, err := store.ListAll(r.Context())
		if err != nil {
			deps.Logger.Error("admin_governance", "err", err.Error())
			http.Error(w, "Erreur interne", http.StatusInternalServerError)
			return
		}
		csrfToken := middleware.CSRFToken(r.Context())
		renderPageV2(w, r, deps, "Assemblées générales", views.GovernanceAdminPage(list, csrfToken))
	}
}

// handleAdminGovernanceConvoke traite POST /admin/governance/{id}/convoke : passe
// l'assemblée en convoquée et courrielle les adhérents actifs. La logique métier
// (transition + envoi + journalisation RGPD) est partagée avec l'action MCP.
func handleAdminGovernanceConvoke(deps app.AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		n, err := seeds.Convoke(r.Context(), deps, id)
		if err != nil {
			middleware.PushFlash(w, "error", err.Error())
		} else {
			middleware.PushFlash(w, "success", convokeFlash(n))
		}
		http.Redirect(w, r, "/admin/governance", http.StatusSeeOther)
	}
}

// handleAdminGovernanceAttendance gère GET /admin/governance/{id}/attendance :
// écran d'émargement (adhérents actifs éligibles + état de présence) et résultat de
// quorum. La population éligible est résolue ici (consommateur) via membership.Store ;
// le décompte est délégué à seeds.CheckQuorum / governance.ComputeQuorum.
func handleAdminGovernanceAttendance(deps app.AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		gov := &governance.Store{DB: deps.DB}
		asm, err := gov.GetByID(r.Context(), id)
		if err != nil {
			middleware.PushFlash(w, "error", err.Error())
			http.Redirect(w, r, "/admin/governance", http.StatusSeeOther)
			return
		}

		// Quorum (seuil par défaut 0,5 ; la règle de seuil reste paramétrable).
		quorum, err := seeds.CheckQuorum(r.Context(), deps, id, 0.5)
		if err != nil {
			deps.Logger.Error("admin_governance_attendance_quorum", "err", err.Error())
			http.Error(w, "Erreur interne", http.StatusInternalServerError)
			return
		}

		// Lignes d'émargement : adhérents actifs réels (jamais fabriqués), nom résolu
		// en compte existant, état présent/absent depuis le registre d'émargement.
		memberships, err := (&membership.Store{DB: deps.DB}).ListAll(r.Context(), "active")
		if err != nil {
			deps.Logger.Error("admin_governance_attendance_members", "err", err.Error())
			http.Error(w, "Erreur interne", http.StatusInternalServerError)
			return
		}
		present, err := gov.PresentMemberIDs(r.Context(), id)
		if err != nil {
			deps.Logger.Error("admin_governance_attendance_present", "err", err.Error())
			http.Error(w, "Erreur interne", http.StatusInternalServerError)
			return
		}
		idStore := &identity.Store{DB: deps.DB}
		seen := make(map[string]struct{}, len(memberships))
		var rows []views.AttendanceRow
		for _, m := range memberships {
			if _, ok := seen[m.UserID]; ok {
				continue
			}
			seen[m.UserID] = struct{}{}
			u, err := idStore.GetByID(r.Context(), m.UserID)
			if err != nil {
				deps.Logger.Error("admin_governance_attendance_user", "err", err.Error())
				http.Error(w, "Erreur interne", http.StatusInternalServerError)
				return
			}
			if u == nil {
				continue // adhésion orpheline (compte supprimé) : ignorée, pas fabriquée
			}
			_, isPresent := present[m.UserID]
			rows = append(rows, views.AttendanceRow{
				UserID:      u.ID,
				DisplayName: u.DisplayName,
				Present:     isPresent,
			})
		}

		csrfToken := middleware.CSRFToken(r.Context())
		renderPageV2(w, r, deps, "Émargement", views.GovernanceAttendancePage(asm, rows, quorum, csrfToken))
	}
}

// handleAdminGovernanceSign traite POST /admin/governance/{id}/sign : marque un
// membre présent (émargement idempotent), puis revient à l'écran d'émargement.
func handleAdminGovernanceSign(deps app.AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		if err := r.ParseForm(); err != nil {
			http.Error(w, "formulaire invalide", http.StatusBadRequest)
			return
		}
		memberID := strings.TrimSpace(r.FormValue("member_user_id"))
		if _, err := (&governance.Store{DB: deps.DB}).SignAttendance(r.Context(), id, memberID, "manual"); err != nil {
			middleware.PushFlash(w, "error", err.Error())
		} else {
			middleware.PushFlash(w, "success", "Émargement enregistré.")
		}
		http.Redirect(w, r, "/admin/governance/"+id+"/attendance", http.StatusSeeOther)
	}
}

// handleAdminGovernanceResolutions gère GET (liste des résolutions + dépouillement +
// formulaire de vote) et POST (ouverture d'une résolution) sur
// /admin/governance/{id}/resolutions. L'AG doit être convoquée (garde dans OpenResolution).
func handleAdminGovernanceResolutions(deps app.AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		gov := &governance.Store{DB: deps.DB}
		if r.Method == http.MethodPost {
			if err := r.ParseForm(); err != nil {
				http.Error(w, "formulaire invalide", http.StatusBadRequest)
				return
			}
			if _, err := gov.OpenResolution(r.Context(), id, strings.TrimSpace(r.FormValue("label"))); err != nil {
				middleware.PushFlash(w, "error", err.Error())
			} else {
				middleware.PushFlash(w, "success", "Résolution ouverte au vote.")
			}
			http.Redirect(w, r, "/admin/governance/"+id+"/resolutions", http.StatusSeeOther)
			return
		}

		asm, err := gov.GetByID(r.Context(), id)
		if err != nil {
			middleware.PushFlash(w, "error", err.Error())
			http.Redirect(w, r, "/admin/governance", http.StatusSeeOther)
			return
		}

		resolutions, err := gov.ListResolutions(r.Context(), id)
		if err != nil {
			deps.Logger.Error("admin_governance_resolutions", "err", err.Error())
			http.Error(w, "Erreur interne", http.StatusInternalServerError)
			return
		}
		var rows []views.ResolutionRow
		for _, res := range resolutions {
			t, err := gov.Tally(r.Context(), res.ID)
			if err != nil {
				deps.Logger.Error("admin_governance_resolutions_tally", "err", err.Error())
				http.Error(w, "Erreur interne", http.StatusInternalServerError)
				return
			}
			rows = append(rows, views.ResolutionRow{Resolution: res, Tally: t})
		}

		// Votants proposés : adhérents actifs réels (jamais fabriqués), résolus en compte
		// existant. L'éligibilité reste contrôlée à l'enregistrement (par-votant).
		voters, err := activeMemberRows(r, deps)
		if err != nil {
			deps.Logger.Error("admin_governance_resolutions_voters", "err", err.Error())
			http.Error(w, "Erreur interne", http.StatusInternalServerError)
			return
		}

		csrfToken := middleware.CSRFToken(r.Context())
		renderPageV2(w, r, deps, "Vote", views.GovernanceResolutionsPage(asm, rows, voters, csrfToken))
	}
}

// handleAdminGovernanceVote traite POST /admin/governance/resolutions/{rid}/vote :
// enregistre un bulletin (gardes dans CastVoteWithChecker), journalise l'accès
// nominatif, puis revient à l'écran de vote de l'assemblée.
func handleAdminGovernanceVote(deps app.AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rid := chi.URLParam(r, "rid")
		if err := r.ParseForm(); err != nil {
			http.Error(w, "formulaire invalide", http.StatusBadRequest)
			return
		}
		gov := &governance.Store{DB: deps.DB}
		members := &membership.Store{DB: deps.DB}
		voterID := strings.TrimSpace(r.FormValue("voter_user_id"))
		choice := strings.TrimSpace(r.FormValue("choice"))
		if _, err := gov.CastVoteWithChecker(r.Context(), members, rid, voterID, choice); err != nil {
			middleware.PushFlash(w, "error", err.Error())
		} else {
			middleware.PushFlash(w, "success", "Vote enregistré.")
		}
		http.Redirect(w, r, governanceResolutionBackURL(deps, r, rid), http.StatusSeeOther)
	}
}

// handleAdminGovernanceCloseResolution traite POST
// /admin/governance/resolutions/{rid}/close : clôt le scrutin, puis revient à l'écran
// de vote.
func handleAdminGovernanceCloseResolution(deps app.AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rid := chi.URLParam(r, "rid")
		if err := (&governance.Store{DB: deps.DB}).CloseResolution(r.Context(), rid); err != nil {
			middleware.PushFlash(w, "error", err.Error())
		} else {
			middleware.PushFlash(w, "success", "Scrutin clos.")
		}
		http.Redirect(w, r, governanceResolutionBackURL(deps, r, rid), http.StatusSeeOther)
	}
}

// handleAdminGovernancePV gère GET /admin/governance/{id}/pv : écran de trace d'une
// assemblée — registre des délibérations consignées, résolutions closes restant à
// consigner, et procès-verbal (généré ou non).
func handleAdminGovernancePV(deps app.AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		gov := &governance.Store{DB: deps.DB}
		asm, err := gov.GetByID(r.Context(), id)
		if err != nil {
			middleware.PushFlash(w, "error", err.Error())
			http.Redirect(w, r, "/admin/governance", http.StatusSeeOther)
			return
		}

		register, err := gov.ListRegister(r.Context(), id)
		if err != nil {
			deps.Logger.Error("admin_governance_pv_register", "err", err.Error())
			http.Error(w, "Erreur interne", http.StatusInternalServerError)
			return
		}
		// Résolutions closes non encore consignées : candidates à la consignation.
		consigned := make(map[string]struct{}, len(register))
		for _, d := range register {
			consigned[d.ResolutionID] = struct{}{}
		}
		resolutions, err := gov.ListResolutions(r.Context(), id)
		if err != nil {
			deps.Logger.Error("admin_governance_pv_resolutions", "err", err.Error())
			http.Error(w, "Erreur interne", http.StatusInternalServerError)
			return
		}
		var closable []views.ResolutionRow
		for _, res := range resolutions {
			if res.Status != "closed" {
				continue
			}
			if _, ok := consigned[res.ID]; ok {
				continue
			}
			t, err := gov.Tally(r.Context(), res.ID)
			if err != nil {
				deps.Logger.Error("admin_governance_pv_tally", "err", err.Error())
				http.Error(w, "Erreur interne", http.StatusInternalServerError)
				return
			}
			closable = append(closable, views.ResolutionRow{Resolution: res, Tally: t})
		}

		var minutesBody string
		if m, err := gov.GetMinutes(r.Context(), id); err == nil {
			minutesBody = m.Body
		} else if !errors.Is(err, governance.ErrMinutesIntrouvable) {
			deps.Logger.Error("admin_governance_pv_minutes", "err", err.Error())
			http.Error(w, "Erreur interne", http.StatusInternalServerError)
			return
		}

		csrfToken := middleware.CSRFToken(r.Context())
		renderPageV2(w, r, deps, "Procès-verbal", views.GovernancePVPage(asm, register, closable, minutesBody, csrfToken))
	}
}

// handleAdminGovernanceRecordDeliberation traite POST
// /admin/governance/resolutions/{rid}/record : consigne au registre le dépouillement
// figé d'une résolution close, puis revient à l'écran de trace de l'assemblée.
func handleAdminGovernanceRecordDeliberation(deps app.AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rid := chi.URLParam(r, "rid")
		if _, err := (&governance.Store{DB: deps.DB}).RecordDeliberation(r.Context(), rid); err != nil {
			middleware.PushFlash(w, "error", err.Error())
		} else {
			middleware.PushFlash(w, "success", "Délibération consignée au registre.")
		}
		http.Redirect(w, r, governancePVBackURL(deps, r, rid), http.StatusSeeOther)
	}
}

// handleAdminGovernanceGenerateMinutes traite POST /admin/governance/{id}/minutes :
// génère le procès-verbal (immuable) de l'assemblée, puis revient à l'écran de trace.
func handleAdminGovernanceGenerateMinutes(deps app.AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		if _, err := (&governance.Store{DB: deps.DB}).GeneratePV(r.Context(), id); err != nil {
			middleware.PushFlash(w, "error", err.Error())
		} else {
			middleware.PushFlash(w, "success", "Procès-verbal généré.")
		}
		http.Redirect(w, r, "/admin/governance/"+id+"/pv", http.StatusSeeOther)
	}
}

// governancePVBackURL retrouve l'assemblée d'une résolution pour revenir à son écran
// de trace. À défaut (résolution introuvable), retombe sur la liste des AG.
func governancePVBackURL(deps app.AppDeps, r *http.Request, resolutionID string) string {
	res, err := (&governance.Store{DB: deps.DB}).GetResolution(r.Context(), resolutionID)
	if err != nil {
		return "/admin/governance"
	}
	return "/admin/governance/" + res.AssemblyID + "/pv"
}

// activeMemberRows résout les votants réellement éligibles en lignes (id + nom) pour la
// liste déroulante de vote. La population affichée doit coïncider exactement avec la garde
// d'enregistrement (CastVoteWithChecker → membership.CurrentStatus) : status='active' ET
// période courante. ListAll(active) ne filtre que sur le statut, sans la période ; chaque
// candidat est donc re-contrôlé par CurrentStatus, la même fonction que la garde, afin
// qu'un adhérent actif mais hors période n'apparaisse pas pour être rejeté à la soumission.
func activeMemberRows(r *http.Request, deps app.AppDeps) ([]views.AttendanceRow, error) {
	members := &membership.Store{DB: deps.DB}
	memberships, err := members.ListAll(r.Context(), "active")
	if err != nil {
		return nil, err
	}
	idStore := &identity.Store{DB: deps.DB}
	seen := make(map[string]struct{}, len(memberships))
	var rows []views.AttendanceRow
	for _, m := range memberships {
		if _, ok := seen[m.UserID]; ok {
			continue
		}
		seen[m.UserID] = struct{}{}
		// Re-contrôle par la même fonction que la garde de vote (statut + période).
		status, err := members.CurrentStatus(r.Context(), m.UserID)
		if err != nil {
			return nil, err
		}
		if status != "active" {
			continue // actif au statut mais hors période : non éligible, écarté de la liste
		}
		u, err := idStore.GetByID(r.Context(), m.UserID)
		if err != nil {
			return nil, err
		}
		if u == nil {
			continue // adhésion orpheline (compte supprimé) : ignorée, pas fabriquée
		}
		rows = append(rows, views.AttendanceRow{UserID: u.ID, DisplayName: u.DisplayName})
	}
	return rows, nil
}

// governanceResolutionBackURL retrouve l'assemblée d'une résolution pour revenir à son
// écran de vote. À défaut (résolution introuvable), retombe sur la liste des AG.
func governanceResolutionBackURL(deps app.AppDeps, r *http.Request, resolutionID string) string {
	res, err := (&governance.Store{DB: deps.DB}).GetResolution(r.Context(), resolutionID)
	if err != nil {
		return "/admin/governance"
	}
	return "/admin/governance/" + res.AssemblyID + "/resolutions"
}

// convokeFlash compose le message de confirmation de convocation.
func convokeFlash(n int) string {
	if n == 0 {
		return "Assemblée convoquée. Aucun adhérent actif à contacter."
	}
	if n == 1 {
		return "Assemblée convoquée. 1 convocation envoyée."
	}
	return "Assemblée convoquée. " + strconv.Itoa(n) + " convocations envoyées."
}
