// CLAUDE:SUMMARY Trace : procès-verbal et registre append-only (V2d) — gabarit
// gdpr/store.go (Log + List*, AUCUN Update/Delete). RecordDeliberation (fige le tally
// d'une résolution close en colonnes ; ErrScrutinNonClos si open/inexistante ;
// ErrDejaConsigne sur double consignation), GeneratePV (snapshot texte d'une AG
// convoquée ; ErrPVDejaGenere sur régénération), ListRegister/GetMinutes (lecture).
// Import-clean (publiable).
package governance

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hazyhaar/assokit/pkg/uid"
)

// ErrScrutinNonClos est retourné quand une consignation au registre est tentée sur
// une résolution qui n'est pas close (ouverte ou inexistante) : on ne consigne au
// registre que le dépouillement d'un scrutin clos.
var ErrScrutinNonClos = errors.New("governance: le scrutin n'est pas clos")

// ErrDejaConsigne est retourné quand une résolution a déjà été consignée au registre :
// une délibération est immuable, une seconde consignation n'écrase rien et est rejetée.
var ErrDejaConsigne = errors.New("governance: cette résolution est déjà consignée au registre")

// ErrPVDejaGenere est retourné quand un procès-verbal existe déjà pour l'assemblée :
// le PV est immuable, une régénération n'écrase rien et est rejetée.
var ErrPVDejaGenere = errors.New("governance: un procès-verbal existe déjà pour cette assemblée")

// ErrMinutesIntrouvable est retourné quand aucun procès-verbal n'a encore été généré
// pour l'assemblée ciblée.
var ErrMinutesIntrouvable = errors.New("governance: procès-verbal introuvable")

// ErrDeliberationsPendantes est retourné quand la génération d'un procès-verbal est
// tentée alors qu'au moins une résolution close de l'assemblée n'a pas été consignée au
// registre. Le PV étant immuable, il ne doit jamais figer un registre incomplet :
// chaque résolution close doit d'abord être consignée (RecordDeliberation).
var ErrDeliberationsPendantes = errors.New("governance: des résolutions closes ne sont pas consignées au registre")

// RecordDeliberation consigne au registre le dépouillement FIGÉ d'une résolution close.
// Le tally est lu une fois (Tally) puis stocké en colonnes (pour/contre/abstention/
// total) : la délibération ne référence pas un décompte recalculé, c'est une trace
// immuable. Gardes, toutes AVANT l'insert :
//   - la résolution doit exister ET être close (chargée via GetResolution ; rejet
//     ErrScrutinNonClos si ouverte OU inexistante) ;
//   - au plus une consignation par résolution (UNIQUE(resolution_id) ; un conflit est
//     traduit en ErrDejaConsigne, fail-loud, jamais d'écrasement).
//
// L'assemblée et le libellé sont repris de la résolution (cohérence). Retourne
// l'identifiant de l'entrée de registre créée.
func (s *Store) RecordDeliberation(ctx context.Context, resolutionID string) (string, error) {
	resolutionID = strings.TrimSpace(resolutionID)

	res, err := s.GetResolution(ctx, resolutionID)
	if err != nil {
		if errors.Is(err, ErrResolutionIntrouvable) {
			return "", ErrScrutinNonClos // un scrutin inconnu n'est pas un scrutin clos
		}
		return "", fmt.Errorf("governance.RecordDeliberation résolution: %w", err)
	}
	if res.Status != "closed" {
		return "", ErrScrutinNonClos
	}

	// Fige le dépouillement au moment de la consignation : lu une fois, copié en
	// colonnes. La résolution étant close, le tally ne peut plus bouger.
	t, err := s.Tally(ctx, resolutionID)
	if err != nil {
		return "", fmt.Errorf("governance.RecordDeliberation tally: %w", err)
	}

	id := uid.New()
	if _, err := s.DB.ExecContext(ctx,
		`INSERT INTO deliberations(id, assembly_id, resolution_id, label, pour, contre, abstention, total)
		 VALUES(?,?,?,?,?,?,?,?)`,
		id, res.AssemblyID, resolutionID, res.Label, t.Pour, t.Contre, t.Abstention, t.Total,
	); err != nil {
		if isUniqueViolation(err) {
			return "", ErrDejaConsigne
		}
		return "", fmt.Errorf("governance.RecordDeliberation: %w", err)
	}
	return id, nil
}

// ListRegister retourne les délibérations consignées d'une assemblée, dans l'ordre de
// consignation (les plus anciennes d'abord : ordre de lecture d'un procès-verbal).
// Lecture seule. Une assemblée sans délibération renvoie une liste vide, sans erreur.
func (s *Store) ListRegister(ctx context.Context, assemblyID string) ([]Deliberation, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT id, assembly_id, resolution_id, label, pour, contre, abstention, total, recorded_at
		FROM deliberations WHERE assembly_id=?
		ORDER BY recorded_at ASC, id ASC
	`, assemblyID)
	if err != nil {
		return nil, fmt.Errorf("governance.ListRegister: %w", err)
	}
	defer rows.Close()
	var out []Deliberation
	for rows.Next() {
		var d Deliberation
		var recordedAt sql.NullString
		if err := rows.Scan(&d.ID, &d.AssemblyID, &d.ResolutionID, &d.Label,
			&d.Pour, &d.Contre, &d.Abstention, &d.Total, &recordedAt); err != nil {
			return nil, fmt.Errorf("governance.ListRegister scan: %w", err)
		}
		if recordedAt.Valid {
			d.RecordedAt, _ = time.Parse(tsLayout, recordedAt.String)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// ClosedUnconsignedResolutions retourne les identifiants des résolutions closes d'une
// assemblée qui n'ont PAS encore été consignées au registre des délibérations (status
// 'closed' ET absentes de deliberations). Une liste vide signifie que toute résolution
// close est consignée — condition nécessaire à la génération d'un procès-verbal complet.
// Lecture seule.
func (s *Store) ClosedUnconsignedResolutions(ctx context.Context, assemblyID string) ([]string, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT r.id
		FROM resolutions r
		LEFT JOIN deliberations d ON d.resolution_id = r.id
		WHERE r.assembly_id = ? AND r.status = 'closed' AND d.id IS NULL
		ORDER BY r.created_at ASC, r.id ASC
	`, strings.TrimSpace(assemblyID))
	if err != nil {
		return nil, fmt.Errorf("governance.ClosedUnconsignedResolutions: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("governance.ClosedUnconsignedResolutions scan: %w", err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// GeneratePV génère le procès-verbal d'une assemblée et le fige (immuable).
// Gardes, toutes AVANT l'insert :
//   - l'AG doit être convoquée (GetByID puis rejet ErrAssembleeNonConvoquee, comme
//     RecordMandate/SignAttendance) ;
//   - toute résolution close de l'AG doit être consignée au registre (sinon le PV
//     immuable figerait un registre incomplet — rejet ErrDeliberationsPendantes,
//     via ClosedUnconsignedResolutions) ;
//   - au plus un PV par assemblée (UNIQUE(assembly_id) ; un conflit est traduit en
//     ErrPVDejaGenere, fail-loud, jamais d'écrasement).
//
// Le corps est un instantané texte : nom de l'AG, ordre du jour, et la liste des
// délibérations consignées avec leurs dépouillements (lus via ListRegister). Le PV
// reflète l'état du registre au moment de la génération ; il n'est jamais régénéré.
// Retourne l'identifiant du PV créé.
func (s *Store) GeneratePV(ctx context.Context, assemblyID string) (string, error) {
	assemblyID = strings.TrimSpace(assemblyID)

	asm, err := s.GetByID(ctx, assemblyID)
	if err != nil {
		return "", fmt.Errorf("governance.GeneratePV assemblée: %w", err)
	}
	if asm.Status != "convoked" {
		return "", ErrAssembleeNonConvoquee
	}

	// Garde de complétude : un PV immuable ne doit jamais figer un registre partiel.
	// Toute résolution close doit avoir été consignée avant la génération.
	pending, err := s.ClosedUnconsignedResolutions(ctx, assemblyID)
	if err != nil {
		return "", fmt.Errorf("governance.GeneratePV complétude: %w", err)
	}
	if len(pending) > 0 {
		return "", ErrDeliberationsPendantes
	}

	register, err := s.ListRegister(ctx, assemblyID)
	if err != nil {
		return "", fmt.Errorf("governance.GeneratePV registre: %w", err)
	}

	body := composePVBody(asm, register)
	id := uid.New()
	if _, err := s.DB.ExecContext(ctx,
		`INSERT INTO minutes(id, assembly_id, body) VALUES(?,?,?)`,
		id, assemblyID, body,
	); err != nil {
		if isUniqueViolation(err) {
			return "", ErrPVDejaGenere
		}
		return "", fmt.Errorf("governance.GeneratePV: %w", err)
	}
	return id, nil
}

// GetMinutes retourne le procès-verbal d'une assemblée. Retourne ErrMinutesIntrouvable
// si aucun PV n'a encore été généré. Lecture seule.
func (s *Store) GetMinutes(ctx context.Context, assemblyID string) (Minutes, error) {
	var m Minutes
	var generatedAt sql.NullString
	err := s.DB.QueryRowContext(ctx, `
		SELECT id, assembly_id, body, generated_at
		FROM minutes WHERE assembly_id=?
	`, strings.TrimSpace(assemblyID)).Scan(&m.ID, &m.AssemblyID, &m.Body, &generatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return m, ErrMinutesIntrouvable
	}
	if err != nil {
		return m, fmt.Errorf("governance.GetMinutes: %w", err)
	}
	if generatedAt.Valid {
		m.GeneratedAt, _ = time.Parse(tsLayout, generatedAt.String)
	}
	return m, nil
}

// composePVBody compose l'instantané texte du procès-verbal : nom de l'assemblée,
// ordre du jour, puis chaque délibération consignée avec son dépouillement figé. Un
// PV sans délibération consignée le mentionne explicitement (état vide assumé).
//
// RGPD — par construction, le corps ne consigne que des données AGRÉGÉES et non
// nominatives : décomptes (pour/contre/abstention/total) et libellés de résolution. Il
// n'expose aucun bulletin individuel ni identité de votant, ce qui dispense la
// génération d'un LogAccess (aucun accès nominatif). RÉSERVE : l'ordre du jour
// (asm.Agenda) est un texte libre saisi par l'administrateur et peut, selon son contenu,
// mentionner une personne nommément. Ce risque nominatif dépend alors du CONTENU saisi,
// pas de la STRUCTURE du PV ; il relève de la responsabilité de saisie de l'ordre du
// jour, non d'un accès nominatif tracé par cette fonction.
func composePVBody(asm Assembly, register []Deliberation) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Procès-verbal — %s\n\n", asm.Name)

	agenda := strings.TrimSpace(asm.Agenda)
	if agenda == "" {
		agenda = "(non renseigné)"
	}
	fmt.Fprintf(&b, "Ordre du jour :\n%s\n\n", agenda)

	b.WriteString("Délibérations consignées :\n")
	if len(register) == 0 {
		b.WriteString("Aucune délibération consignée.\n")
		return b.String()
	}
	for i, d := range register {
		fmt.Fprintf(&b,
			"%d. %s — Pour : %d · Contre : %d · Abstention : %d · Total des votes : %d\n",
			i+1, d.Label, d.Pour, d.Contre, d.Abstention, d.Total)
	}
	return b.String()
}
