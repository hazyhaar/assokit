// CLAUDE:SUMMARY Parcours guidé d'intégration d'un propriétaire (V3c) : admin
// (/admin/onboard-proprietaire GET formulaire guidé + POST). Cascade orchestrée
// sur les stores réutilisés (identity.Register → membership.Create → parcelle.Create
// + parcelle.AddDroit) : validation complète AVANT toute création, rapport d'état
// fail-loud si une étape échoue après une autre réussie (pas de demi-intégration
// silencieuse). Gating admin assuré par requireAdmin sur la route. Aucune donnée
// fabriquée : tout provient du formulaire, persisté via les stores réels.
package handlers

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/internal/webui/views"
	"github.com/hazyhaar/assokit/pkg/gdpr"
	"github.com/hazyhaar/assokit/pkg/horui/middleware"
	"github.com/hazyhaar/assokit/pkg/identity"
	"github.com/hazyhaar/assokit/pkg/membership"
	"github.com/hazyhaar/assokit/pkg/parcelle"
)

// onboardParcelleInput est une parcelle saisie dans le formulaire d'intégration,
// avant validation. SurfaceM2 reste textuelle tant qu'elle n'est pas validée.
type onboardParcelleInput struct {
	CommuneCode    string
	Section        string
	NumeroParcelle string
	SurfaceText    string
	Nature         string
}

// onboardForm regroupe les entrées validées du formulaire d'intégration.
type onboardForm struct {
	Email       string
	DisplayName string
	Password    string
	PeriodStart string
	PeriodEnd   string
	AmountCents int64
	Parcelles   []onboardParcelleValid
}

// onboardParcelleValid est une parcelle dont tous les champs ont été validés.
type onboardParcelleValid struct {
	parcelle.Parcelle
	Nature string
}

// handleAdminOnboardProprietaire gère GET (formulaire guidé) et POST (intégration en
// cascade) sur /admin/onboard-proprietaire. Le POST valide INTÉGRALEMENT les entrées
// avant toute écriture ; en cas d'échec après création partielle, un rapport d'état
// explicite est remonté (fail-loud), sans demi-intégration silencieuse.
func handleAdminOnboardProprietaire(deps app.AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			if err := r.ParseForm(); err != nil {
				http.Error(w, "formulaire invalide", http.StatusBadRequest)
				return
			}
			form, verr := parseOnboardForm(r)
			if verr != nil {
				middleware.PushFlash(w, "error", "Validation : "+verr.Error())
				http.Redirect(w, r, "/admin/onboard-proprietaire", http.StatusSeeOther)
				return
			}
			report := runOnboard(r.Context(), deps, form)
			if report.err != nil {
				middleware.PushFlash(w, "error", report.message())
			} else {
				middleware.PushFlash(w, "success", report.message())
				// Intégration nominale d'un propriétaire : accès nominatif journalisé.
				logOnboardAccess(r, deps, report.userID)
			}
			http.Redirect(w, r, "/admin/onboard-proprietaire", http.StatusSeeOther)
			return
		}

		csrfToken := middleware.CSRFToken(r.Context())
		renderPageV2(w, r, deps, "Intégrer un propriétaire",
			views.OnboardProprietairePage(parcelle.NaturesDuDroit, csrfToken))
	}
}

// parseOnboardForm extrait et VALIDE intégralement le formulaire avant toute
// création. Toute incohérence (identité incomplète, période invalide, parcelle mal
// formée) fait échouer la validation amont, garantissant qu'aucune écriture n'est
// tentée si l'ensemble n'est pas cohérent.
func parseOnboardForm(r *http.Request) (onboardForm, error) {
	var f onboardForm
	f.Email = strings.TrimSpace(r.FormValue("email"))
	f.DisplayName = strings.TrimSpace(r.FormValue("display_name"))
	f.Password = r.FormValue("password")
	f.PeriodStart = strings.TrimSpace(r.FormValue("period_start"))
	f.PeriodEnd = strings.TrimSpace(r.FormValue("period_end"))

	if f.Email == "" || !strings.Contains(f.Email, "@") {
		return f, fmt.Errorf("adresse électronique du propriétaire requise")
	}
	if f.DisplayName == "" {
		return f, fmt.Errorf("nom du propriétaire requis")
	}
	if len(f.Password) < 8 {
		return f, fmt.Errorf("mot de passe initial d'au moins 8 caractères requis")
	}
	if f.PeriodStart == "" || f.PeriodEnd == "" {
		return f, fmt.Errorf("période d'adhésion invalide (la fin doit être postérieure au début)")
	}
	start, errStart := time.Parse("2006-01-02", f.PeriodStart)
	end, errEnd := time.Parse("2006-01-02", f.PeriodEnd)
	if errStart != nil || errEnd != nil {
		return f, fmt.Errorf("période d'adhésion invalide (dates attendues au format AAAA-MM-JJ)")
	}
	if !end.After(start) {
		return f, fmt.Errorf("période d'adhésion invalide (la fin doit être postérieure au début)")
	}

	amountText := strings.TrimSpace(r.FormValue("amount_cents"))
	if amountText != "" {
		cents, err := strconv.ParseInt(amountText, 10, 64)
		if err != nil || cents < 0 {
			return f, fmt.Errorf("montant de cotisation invalide (entier de centimes positif)")
		}
		f.AmountCents = cents
	}

	communes := r.Form["parcelle_commune"]
	sections := r.Form["parcelle_section"]
	numeros := r.Form["parcelle_numero"]
	surfaces := r.Form["parcelle_surface"]
	natures := r.Form["parcelle_nature"]
	if len(communes) == 0 {
		return f, fmt.Errorf("au moins une parcelle est requise")
	}
	if len(sections) != len(communes) || len(numeros) != len(communes) ||
		len(surfaces) != len(communes) || len(natures) != len(communes) {
		return f, fmt.Errorf("formulaire de parcelles incohérent")
	}
	naturesAutorisees := make(map[string]struct{}, len(parcelle.NaturesDuDroit))
	for _, n := range parcelle.NaturesDuDroit {
		naturesAutorisees[n] = struct{}{}
	}
	for i := range communes {
		in := onboardParcelleInput{
			CommuneCode:    strings.TrimSpace(communes[i]),
			Section:        strings.TrimSpace(sections[i]),
			NumeroParcelle: strings.TrimSpace(numeros[i]),
			SurfaceText:    strings.TrimSpace(surfaces[i]),
			Nature:         strings.TrimSpace(natures[i]),
		}
		// Ligne entièrement vide : ignorée (le formulaire propose des lignes vierges).
		if in.CommuneCode == "" && in.Section == "" && in.NumeroParcelle == "" && in.SurfaceText == "" {
			continue
		}
		if in.CommuneCode == "" || in.Section == "" || in.NumeroParcelle == "" {
			return f, fmt.Errorf("parcelle %d : commune, section et numéro requis", i+1)
		}
		surface, err := strconv.ParseInt(in.SurfaceText, 10, 64)
		if err != nil || surface < 0 {
			return f, fmt.Errorf("parcelle %d : surface invalide (entier de mètres carrés positif)", i+1)
		}
		if _, ok := naturesAutorisees[in.Nature]; !ok {
			return f, fmt.Errorf("parcelle %d : nature du droit invalide", i+1)
		}
		f.Parcelles = append(f.Parcelles, onboardParcelleValid{
			Parcelle: parcelle.Parcelle{
				CommuneCode:    in.CommuneCode,
				Section:        in.Section,
				NumeroParcelle: in.NumeroParcelle,
				SurfaceM2:      surface,
				StatutMad:      "libre",
			},
			Nature: in.Nature,
		})
	}
	if len(f.Parcelles) == 0 {
		return f, fmt.Errorf("au moins une parcelle complète est requise")
	}
	return f, nil
}

// onboardReport décrit l'état atteint par la cascade d'intégration. Il permet de
// remonter un message clair en cas d'échec après création partielle (fail-loud).
type onboardReport struct {
	userID         string
	memberCreated  bool
	membershipDone bool
	parcellesDone  int
	parcellesTotal int
	err            error
}

// message produit un compte rendu lisible de l'état atteint par la cascade.
func (rep onboardReport) message() string {
	if rep.err == nil {
		return fmt.Sprintf("Propriétaire intégré : membre créé, adhésion enregistrée, %d parcelle(s) rattachée(s).", rep.parcellesDone)
	}
	var b strings.Builder
	b.WriteString("Intégration interrompue : ")
	b.WriteString(rep.err.Error())
	b.WriteString(". État atteint : ")
	if !rep.memberCreated {
		b.WriteString("aucune création (le membre n'a pas pu être créé).")
		return b.String()
	}
	b.WriteString("membre créé")
	if rep.membershipDone {
		b.WriteString(", adhésion enregistrée")
	}
	b.WriteString(fmt.Sprintf(", %d/%d parcelle(s) rattachée(s).", rep.parcellesDone, rep.parcellesTotal))
	return b.String()
}

// runOnboard exécute la cascade d'intégration sur les stores réutilisés. Les stores
// ne partageant pas d'exécuteur transactionnel commun (chacun reçoit *sql.DB), la
// cohérence repose sur la validation amont stricte (parseOnboardForm) ; en cas
// d'échec d'une étape après une autre réussie, l'état atteint est rapporté sans
// poursuivre (fail-loud). Aucune entité n'est recodée : identity.Register,
// membership.Create, parcelle.Create et parcelle.AddDroit sont appelés tels quels.
func runOnboard(ctx context.Context, deps app.AppDeps, f onboardForm) onboardReport {
	rep := onboardReport{parcellesTotal: len(f.Parcelles)}

	user, err := (&identity.Store{DB: deps.DB}).Register(ctx, f.Email, f.Password, f.DisplayName)
	if err != nil {
		rep.err = err
		return rep
	}
	rep.userID = user.ID
	rep.memberCreated = true

	_, err = (&membership.Store{DB: deps.DB}).Create(ctx, membership.Membership{
		UserID:      user.ID,
		PeriodStart: f.PeriodStart,
		PeriodEnd:   f.PeriodEnd,
		AmountCents: f.AmountCents,
		Status:      "active",
		Note:        "Adhésion créée à l'intégration du propriétaire.",
	})
	if err != nil {
		rep.err = err
		return rep
	}
	rep.membershipDone = true

	parcStore := &parcelle.Store{DB: deps.DB}
	for _, pv := range f.Parcelles {
		pid, err := parcStore.Create(ctx, pv.Parcelle)
		if err != nil {
			rep.err = fmt.Errorf("parcelle %s %s %s : %w", pv.CommuneCode, pv.Section, pv.NumeroParcelle, err)
			return rep
		}
		if _, err := parcStore.AddDroit(ctx, pid, user.ID, pv.Nature); err != nil {
			rep.err = fmt.Errorf("droit sur parcelle %s %s %s : %w", pv.CommuneCode, pv.Section, pv.NumeroParcelle, err)
			return rep
		}
		rep.parcellesDone++
	}
	return rep
}

// logOnboardAccess journalise l'intégration nominale d'un propriétaire (acteur =
// administrateur courant, sujet = membre intégré). N'échoue jamais la requête.
func logOnboardAccess(r *http.Request, deps app.AppDeps, userID string) {
	u := middleware.UserFromContext(r.Context())
	if u == nil || userID == "" {
		return
	}
	gdpr.LogAccess(r.Context(), &gdpr.Store{DB: deps.DB}, deps.Logger, gdpr.AccessLog{
		UserID:      userID,
		SubjectKind: gdpr.SubjectUser,
		SubjectID:   userID,
		ActorID:     u.ID,
		Action:      gdpr.ActionView,
	})
}
