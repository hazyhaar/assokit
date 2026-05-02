# Code Patterns Go — assokit

Style baseline Go pour ce projet. Chaque section : règle + snippet DO / DON'T.
Longueur cible ≤ 200 LOC — pointer ici depuis CONTRIBUTING.md.

---

## 1. Naming

**Règle** : PascalCase pour les exportés, camelCase pour les privés, packages courts au singulier, méthodes = verbe.

```go
// DO
package rbac

type Store struct { DB *sql.DB }
func (s *Store) CreateGrade(ctx context.Context, name string) (string, error) { … }
var ErrCycleDetected = errors.New("rbac: cycle detected")

// DON'T
package rbacPackage  // package verbeux, pluriel
type RBACStore struct {}
func (s *RBACStore) DoCreateGrade(…) {}  // préfixe "Do" superflu
```

---

## 2. Errors

**Règle** : wrap avec `%w`, sentinels exportés `var ErrXxx = errors.New(…)`, JAMAIS `panic` hors init, toujours `return err` explicite.

```go
// DO
var ErrNotFound = errors.New("store: not found")

func (s *Store) Get(ctx context.Context, id string) (*Grade, error) {
    if errors.Is(err, sql.ErrNoRows) {
        return nil, ErrNotFound
    }
    return nil, fmt.Errorf("store.Get: %w", err)
}

// DON'T
func (s *Store) Get(ctx context.Context, id string) (*Grade, error) {
    if err != nil { panic(err) }  // jamais en runtime prod
    return nil, errors.New("not found")  // perd le contexte d'appel
}
```

---

## 3. Returns — early return

**Règle** : guard clause en entrée de fonction, pas de `if { … } else { return }` profond.

```go
// DO
func processGrade(g *Grade) error {
    if g == nil        { return ErrNilGrade }
    if g.Name == ""    { return ErrEmptyName }
    return nil  // happy path à plat
}

// DON'T
func processGrade(g *Grade) error {
    if g != nil {
        if g.Name != "" { /* happy path enfoui */ } else { return ErrEmptyName }
    } else { return ErrNilGrade }
    return nil
}
```

---

## 4. Struct literals

**Règle** : un champ par ligne, virgule trailing, tags `json`/`db` cohérents avec le nom de colonne SQL.

```go
// DO
type Permission struct {
    ID          string `db:"id"          json:"id"`
    Name        string `db:"name"        json:"name"`
    Description string `db:"description" json:"description,omitempty"`
}

// DON'T
type Permission struct { ID string; Name string; Desc string `json:"d"` }
// ligne unique + tag incohérent avec le nom de champ
```

---

## 5. Defer

**Règle** : ressources fermées via `defer` immédiatement après ouverture, JAMAIS fermées à la main en fin de fonction.

```go
// DO
rows, err := s.DB.QueryContext(ctx, `SELECT id FROM grades`)
if err != nil { return err }
defer rows.Close()
for rows.Next() { … }
return rows.Err()

// DON'T
rows, err := s.DB.QueryContext(ctx, `SELECT id FROM grades`)
if err != nil { return err }
for rows.Next() { … }
rows.Close()  // oubli possible sur early-return intermédiaire
return nil
```

---

## 6. Tests — table-driven

**Règle** : `tests := []struct{…}{…}`, `t.Helper()` dans les helpers, `t.Cleanup` pour les ressources.

```go
// DO
func TestAtLeast(t *testing.T) {
    for _, tc := range []struct{ p, req Permission; want bool }{
        {PermWrite, PermRead, true},
        {PermRead, PermWrite, false},
    } {
        if got := tc.p.AtLeast(tc.req); got != tc.want {
            t.Errorf("AtLeast(%s,%s) = %v", tc.p, tc.req, got)
        }
    }
}

// DON'T — un cas = un test = explosion du fichier
func TestAtLeast1(t *testing.T) {}
func TestAtLeast2(t *testing.T) {}
```

---

## 7. Templ

**Règle** : composants stateless, props minimales, aucune logique métier dans `.templ` (helpers Go), `Render(ctx, w)` reçoit le ctx complet.

```go
// DO
templ GradeRow(g rbac.Grade) {
    <tr id={ "grade-row-" + g.ID }>
        <td>{ g.Name }</td>
        if g.System { <td>système</td> } else { <td>custom</td> }
    </tr>
}
// DON'T — couplage DB dans le composant, testabilité zéro
templ GradeList(db *sql.DB) { /* SELECT ici → interdit */ }
```

---

## 8. Comments

**Règle** : phrase Go complète `// Foo …`, uniquement quand le WHY est non-obvious. Pas de `// FIXME` sans `owner:date`. Pas de bloc multi-lignes.

```go
// DO
// BumpVersion invalide toutes les entrées L1 en une opération atomique.
// Préféré à Invalidate(userID) quand une mutation affecte N users inconnus.
func (c *Cache) BumpVersion() uint64 { return c.ver.Add(1) }

// DON'T
// This function increments the version counter by 1 and returns the new value.
// It is used for cache invalidation purposes.  ← décrit le QUOI, pas le POURQUOI
func (c *Cache) BumpVersion() uint64 { return c.ver.Add(1) }
```

---

## 9. Imports

**Règle** : 3 groupes séparés par une ligne vide (stdlib / external / local), pas d'alias sauf collision explicite.

```go
// DO
import (
    "context"
    "database/sql"
    "fmt"

    "github.com/go-chi/chi/v5"
    "github.com/a-h/templ"

    "github.com/hazyhaar/assokit/pkg/horui/rbac"
    "github.com/hazyhaar/assokit/pkg/horui/perms"
)

// DON'T
import (
    "github.com/go-chi/chi/v5"; "context"                          // pas de groupes
    r "github.com/hazyhaar/assokit/pkg/horui/rbac"                 // alias inutile
)
```
