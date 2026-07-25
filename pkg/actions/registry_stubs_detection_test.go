// CLAUDE:SUMMARY M-ASSOKIT-AUDIT-FIX-3 Axe 2a : détecteur AST des stubs cachés. Pour chaque action seeds/, parser le corps de Run() et vérifier qu'il contient une opération DB OU figure dans la whitelist read-only.
package actions_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// readOnlyWhitelist : actions légitimement sans INSERT/UPDATE/DELETE.
// - Les actions *.list, *.get, *.search font seulement des SELECT (légitime).
// - branding.preview : preview cosmetique sans persistance.
// - Toute nouvelle action no-op DOIT être ajoutée ici avec justification, sinon le test échoue.
var readOnlyWhitelist = map[string]string{
	"feedback.list":      "SELECT-only (consultation triages)",
	"users.list":         "SELECT-only (admin RBAC overview)",
	"users.search":       "SELECT-only (autocomplete admin)",
	"branding.get":       "SELECT-only (singleton read)",
	"branding.preview":   "preview en mémoire sans persistance",
	"pages.get":          "SELECT-only (rendering)",
	"pages.list":         "SELECT-only (admin overview)",
	"profile.get":        "SELECT-only (settings page)",
	"signup.list":        "SELECT-only (admin overview signups)",
	"mailer.outbox.list": "SELECT-only (admin overview email outbox)",
	"messages.inbox":     "SELECT-only (liste conversations utilisateur courant)",
	"setup.status":       "lecture seule (diagnostic config : Config + branding + COUNT admin)",
	"events.list":        "SELECT-only (agenda public)",
	"events.get":         "SELECT-only (fiche événement)",
	"memberships.list":   "SELECT-only (liste des adhésions, admin)",
	"memberships.mine":   "SELECT-only (adhésions de l'utilisateur courant)",
	"newsletter.list":    "SELECT-only (admin overview diffusions)",
	// Salons : lecture seule légitime.
	"salon.list": "SELECT-only (salons dont l'utilisateur courant est membre)",
	// Transcripts : lecture seule via transcript.Store (l'écriture appartient au bot transcripteur, pas à l'utilisateur).
	"transcript.list": "SELECT-only (transcripts d'un salon via transcript.Store.ListBySalon)",
	"transcript.get":  "SELECT-only (transcript + segments via transcript.Store.GetTranscript/Segments)",
	// room.join : forge un jeton LiveKit participant (SELECT + RoomAccess JWT en mémoire).
	// Aucun INSERT/UPDATE/DELETE : l'action est une opération de lecture + calcul, pas une mutation DB.
	"room.join": "forge de jeton LiveKit participant (SELECT salon + RoomAccess, pas de mutation DB)",
	// room.mute_participant, room.remove_participant, room.end : actes serveur via RoomService Twirp
	// (appels HTTP vers LiveKit). Aucune mutation DB : l'action appelle le Connector LiveKit
	// qui émet des requêtes HTTP. La garde IsOwner fait un SELECT, pas d'INSERT/UPDATE/DELETE.
	"room.mute_participant":   "acte réseau RoomService Twirp (MutePublishedTrack via Connector LiveKit, pas de mutation DB)",
	"room.remove_participant": "acte réseau RoomService Twirp (RemoveParticipant via Connector LiveKit, pas de mutation DB)",
	"room.end":                "acte réseau RoomService Twirp (DeleteRoom via Connector LiveKit, pas de mutation DB)",
	// Actions marketplace en lecture seule (pkg/listingactions).
	"listing.list_favorites":      "SELECT-only (favoris de l'acheteur)",
	"listing.list_saved_searches": "SELECT-only (recherches sauvegardées)",
	"listing.search":              "SELECT-only (recherche d'annonces, facettes FTS5)",
	// Couche RGPD transverse (pkg/gdpr). gdpr.access_log_list lit le journal
	// (SELECT). account.data_export agrège en lecture les données du membre ; sa
	// seule écriture est la trace d'export au journal (gdpr.LogAccess, side-effect
	// de redevabilité, pas une mutation de donnée métier).
	"gdpr.access_log_list": "SELECT-only (journal des accès nominatifs, redevabilité)",
	"account.data_export":  "lecture (portabilité) + trace d'export au journal (side-effect)",
	// Module convention de pâturage (C4). convention.generate assemble une
	// convention existante (GetByID, SELECT) et ses clauses via le fournisseur
	// injecté ; sa seule écriture est la trace d'accès au journal (side-effect de
	// redevabilité). convention.create et convention.revoke sont des mutations
	// réelles (store.Create / store.Revoke), détectées sans whitelist.
	"convention.generate": "lecture (GetByID + assemblage clauses via provider) + trace d'accès au journal (side-effect)",
	// Gouvernance — présence et représentation (V2b). governance.check_quorum résout
	// les éligibles via membership.Store (SELECT) puis délègue à CheckQuorum →
	// ComputeQuorum, qui décompte présents/représentés par SELECT seuls : aucune
	// mutation de donnée métier. governance.record_mandate et governance.sign_attendance
	// sont des mutations réelles (store.RecordMandate / store.SignAttendance), détectées
	// sans whitelist via les tokens dédiés.
	"governance.check_quorum": "lecture (éligibles via membership + décompte ComputeQuorum par SELECT)",
	// Gouvernance — décision : vote simple (V2c). governance.tally_resolution dépouille
	// par SELECT ... GROUP BY (Tally), aucune mutation de donnée métier. open_resolution,
	// cast_vote et close_resolution sont des mutations réelles (store.OpenResolution /
	// CastVoteWithChecker / CloseResolution), détectées sans whitelist via les tokens dédiés.
	"governance.tally_resolution": "lecture (dépouillement Tally par SELECT GROUP BY)",
	// Gouvernance — trace : procès-verbal et registre (V2d). governance.list_register
	// liste les délibérations consignées par SELECT (ListRegister), aucune mutation.
	// record_deliberation et generate_minutes sont des mutations réelles (store.
	// RecordDeliberation / store.GeneratePV : INSERT deliberations / minutes), détectées
	// sans whitelist via les tokens dédiés.
	"governance.list_register": "lecture (registre des délibérations par SELECT ListRegister)",
}

// stubReport : action signalée comme potentiel stub (Run sans opération DB).
type stubReport struct {
	actionID, file string
	line           int
}

// dbOperationKeywords : tokens dont la présence dans le corps de Run() prouve
// que l'action touche la DB (au-delà du seul SELECT). Cas couvert :
// - INSERT, UPDATE, DELETE en SQL string.
// - Appels deps.DB.* (Exec, ExecContext).
// - Appels store.Create*, store.Update*, store.Delete*, store.Save*, store.Set*.
var dbOperationKeywords = []string{
	// Phrases SQL (et non mots nus) : évite qu'une constante comme
	// "DELETE_MY_ACCOUNT" fasse passer un stub pour une vraie écriture.
	"INSERT INTO", "INSERT OR", "UPDATE ", "DELETE FROM",
	"deps.DB.Exec",
	"store.Create", "store.Update", "store.Delete", "store.Save", "store.Set",
	"store.Add", "store.Remove", "store.Recompute", "store.Grant", "store.Revoke",
	"store.Assign", "store.Unassign",
	"store.Send", "store.MarkRead", // messagerie privée (INSERT / UPDATE read_at)
	"store.Enqueue",             // file de publication réseaux sociaux (INSERT OR IGNORE social_post_queue)
	"store.ResolvePublishing",   // réconciliation d'une diffusion orpheline (UPDATE social_post_log status)
	"store.Join", "store.Leave", // salon : INSERT / DELETE salon_member
	"store.PostMessage",                           // salon : INSERT salon_message
	"store.RecordMandate", "store.SignAttendance", // gouvernance V2b : INSERT mandates / attendance
	"store.OpenResolution", "store.CastVoteWithChecker", "store.CloseResolution", // gouvernance V2c : INSERT resolutions/votes, UPDATE resolutions
	"store.RecordDeliberation", "store.GeneratePV", // gouvernance V2d : INSERT deliberations / minutes (registre + PV append-only)
	"store.Toggle", "store.Archive", // salon : UPDATE salon
	"store.SoftDelete", // events.delete (UPDATE deleted_at)
	"store.MarkSent",   // newsletter.send (UPDATE sent_at)
	"deleteUserRGPD",   // account.delete_self (transaction DELETE FROM users + anonymisation)
	".ExecContext",     // fallback pour deps.DB.ExecContext via variable
	// Mutateurs du Service RBAC partagé (rbacService(deps).X) : Store + recompute des
	// permissions effectives. Émis en idents nus par l'AST (X est un CallExpr, pas un
	// Ident, donc le sélecteur "store.Grant" ne matche pas).
	"GrantPerm", "RevokePerm", "AddInherit", "RemoveInherit",
	// Mutateurs du store marketplace (pkg/listingactions) hors préfixes génériques.
	"store.Report", "store.Hide", "store.Unhide", "store.Open",
	// Gouvernance — socle réunion. governance.convoke délègue à seeds.Convoke, qui
	// effectue la transition (UPDATE assemblies) et la mise en file des courriels +
	// trace (INSERT convocations) : mutation réelle, surfacée par l'appelant nommé.
	"Convoke",
}

// TestRegistry_NoHiddenStubsInSeeds parse tous les seeds/*.go et vérifie pour
// chaque action que sa Run() contient au moins une opération DB OU qu'elle
// figure dans la whitelist read-only.
func TestRegistry_NoHiddenStubsInSeeds(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	// Toutes les sources d'actions injectées dans le MÊME registry runtime (donc
	// servies en HTTP /admin/actions ET en outil MCP). Couvrir seeds/ seul laissait
	// les actions marketplace (pkg/listingactions) hors du gardien anti-stub.
	sourceDirs := []string{
		filepath.Join(wd, "seeds"),
		filepath.Join(wd, "..", "listingactions"),
	}

	var suspects []stubReport
	fset := token.NewFileSet()
	for _, dir := range sourceDirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("read actions dir %s: %v", dir, err)
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
				continue
			}
			path := filepath.Join(dir, e.Name())
			f, err := parser.ParseFile(fset, path, nil, 0)
			if err != nil {
				t.Fatalf("parse %s: %v", path, err)
			}
			extractActions(fset, f, e.Name(), &suspects)
		}
	}

	// Filtrer les actions whitelisted.
	var bad []stubReport
	for _, s := range suspects {
		if _, ok := readOnlyWhitelist[s.actionID]; ok {
			continue
		}
		bad = append(bad, s)
	}

	if len(bad) > 0 {
		for _, s := range bad {
			t.Errorf("HIDDEN STUB: action %q dans %s:%d — Run() ne contient ni opération DB ni entrée dans readOnlyWhitelist (M-ASSOKIT-AUDIT-FIX-3 Axe 2a)",
				s.actionID, s.file, s.line)
		}
	}
}

// extractActions parcourt l'AST d'un fichier seeds, identifie les littéraux
// actions.Action{ID:..., Run: func(...) {...}}, et signale ceux dont Run() ne
// contient pas de keyword DB.
func extractActions(fset *token.FileSet, f *ast.File, filename string, suspects *[]stubReport) {
	ast.Inspect(f, func(n ast.Node) bool {
		cl, ok := n.(*ast.CompositeLit)
		if !ok {
			return true
		}
		// Vérifier que c'est un actions.Action{...} (SelectorExpr "actions" "Action")
		sel, ok := cl.Type.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Action" {
			return true
		}

		var actionID string
		var runBody string
		var runPos token.Pos
		for _, elt := range cl.Elts {
			kv, ok := elt.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			key, ok := kv.Key.(*ast.Ident)
			if !ok {
				continue
			}
			switch key.Name {
			case "ID":
				if bl, ok := kv.Value.(*ast.BasicLit); ok {
					actionID = strings.Trim(bl.Value, `"`)
				}
			case "Run":
				if fl, ok := kv.Value.(*ast.FuncLit); ok && fl.Body != nil {
					runPos = fl.Pos()
					var sb strings.Builder
					ast.Inspect(fl.Body, func(nn ast.Node) bool {
						if id, ok := nn.(*ast.Ident); ok {
							sb.WriteString(id.Name)
							sb.WriteByte(' ')
						}
						if bl, ok := nn.(*ast.BasicLit); ok {
							sb.WriteString(bl.Value)
							sb.WriteByte(' ')
						}
						if se, ok := nn.(*ast.SelectorExpr); ok {
							if x, ok := se.X.(*ast.Ident); ok {
								sb.WriteString(x.Name + "." + se.Sel.Name)
								sb.WriteByte(' ')
							}
						}
						return true
					})
					runBody = sb.String()
				}
			}
		}

		if actionID == "" || runBody == "" {
			return true
		}
		hasDB := false
		for _, kw := range dbOperationKeywords {
			if strings.Contains(runBody, kw) {
				hasDB = true
				break
			}
		}
		if !hasDB {
			*suspects = append(*suspects, stubReport{actionID: actionID, file: filename, line: fset.Position(runPos).Line})
		}
		return true
	})
}
