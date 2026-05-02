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
	"feedback.list":     "SELECT-only (consultation triages)",
	"users.list":        "SELECT-only (admin RBAC overview)",
	"users.search":      "SELECT-only (autocomplete admin)",
	"branding.get":      "SELECT-only (singleton read)",
	"branding.preview":  "preview en mémoire sans persistance",
	"pages.get":         "SELECT-only (rendering)",
	"pages.list":        "SELECT-only (admin overview)",
	"forum.thread.list": "SELECT-only (listing public)",
	"forum.thread.get":  "SELECT-only (rendering)",
	"forum.posts.list":  "SELECT-only (rendering thread)",
	"profile.get":       "SELECT-only (settings page)",
	"signups.list":      "SELECT-only (admin overview, alias signup.list)",
	"signup.list":       "SELECT-only (admin overview signups)",
	"mailer.outbox.list": "SELECT-only (admin overview email outbox)",
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
	"INSERT", "UPDATE", "DELETE",
	"deps.DB.Exec",
	"store.Create", "store.Update", "store.Delete", "store.Save", "store.Set",
	"store.Add", "store.Remove", "store.Recompute", "store.Grant", "store.Revoke",
	"store.Assign", "store.Unassign",
	".ExecContext", // fallback pour deps.DB.ExecContext via variable
}

// TestRegistry_NoHiddenStubsInSeeds parse tous les seeds/*.go et vérifie pour
// chaque action que sa Run() contient au moins une opération DB OU qu'elle
// figure dans la whitelist read-only.
func TestRegistry_NoHiddenStubsInSeeds(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	seedsDir := filepath.Join(wd, "seeds")

	entries, err := os.ReadDir(seedsDir)
	if err != nil {
		t.Fatalf("read seeds dir: %v", err)
	}

	var suspects []stubReport

	fset := token.NewFileSet()
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		path := filepath.Join(seedsDir, e.Name())
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		extractActions(fset, f, e.Name(), &suspects)
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
