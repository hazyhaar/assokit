// CLAUDE:SUMMARY Service RBAC : Can() hot-path L1→L2, Recompute() atomique TX, hooks mutation (M-ASSOKIT-RBAC-2).
// CLAUDE:WARN Recompute() invalide L1 via Cache.BumpVersion(). Toute mutation de grade/perm DOIT passer par Service (pas Store direct) pour maintenir le cache cohérent.
package rbac

import (
	"context"
	"fmt"
	"log/slog"
)

// reqIDKeyType est volontairement défini ici (pas d'import de middleware) pour
// éviter un cycle middleware→perms→rbac→middleware. La valeur est lue via
// context.Value(requestIDKey{}) en utilisant la struct exportée par le middleware,
// mais comme on ne peut pas l'importer, on accepte que ce soit "" pour rbac.
// Le user_id + perm + allowed restent suffisants pour le debug RBAC.

// Service orchestre Store + Cache : permissions effectives avec cache à 2 niveaux.
type Service struct {
	Store *Store
	Cache *Cache
	// Logger optionnel : si nil, logs Debug désactivés (slog.Default n'affiche pas Debug par défaut).
	Logger *slog.Logger
}

// logger retourne le slog.Logger configuré ou slog.Default() en fallback.
func (svc *Service) logger() *slog.Logger {
	if svc.Logger != nil {
		return svc.Logger
	}
	return slog.Default()
}

// Can vérifie si userID a la permission permName.
// Hot path : L1 cache → si hit, retour immédiat sans DB.
// Miss : charge depuis L2 (user_effective_permissions), peuple L1.
// Log Debug uniquement (hot path, désactivé par défaut en prod).
func (svc *Service) Can(ctx context.Context, userID, permName string) (bool, error) {
	if perms, ok := svc.Cache.Get(userID); ok {
		_, has := perms[permName]
		svc.logger().Debug("rbac_check",
			"user_id", userID,
			"perm", permName,
			"allowed", has,
			"source", "L1",
		)
		return has, nil
	}
	perms, err := svc.loadEffectiveFromDB(ctx, userID)
	if err != nil {
		return false, err
	}
	svc.Cache.Set(userID, perms)
	_, has := perms[permName]
	svc.logger().Debug("rbac_check",
		"user_id", userID,
		"perm", permName,
		"allowed", has,
		"source", "L2",
	)
	return has, nil
}

// loadEffectiveFromDB charge les noms de permissions effectives depuis L2.
func (svc *Service) loadEffectiveFromDB(ctx context.Context, userID string) (map[string]struct{}, error) {
	rows, err := svc.Store.DB.QueryContext(ctx, `
		SELECT p.name
		FROM permissions p
		JOIN user_effective_permissions uep ON uep.permission_id = p.id
		WHERE uep.user_id = ?`, userID)
	if err != nil {
		return nil, fmt.Errorf("rbac: load effective: %w", err)
	}
	defer rows.Close()
	perms := make(map[string]struct{})
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		perms[name] = struct{}{}
	}
	return perms, rows.Err()
}

// Recompute recalcule les permissions effectives d'un user :
// closure transitive des grades hérités → union des permissions → DELETE+INSERT atomique.
// Invalide le cache L1 après.
func (svc *Service) Recompute(ctx context.Context, userID string) error {
	grades, err := svc.Store.UserGrades(ctx, userID)
	if err != nil {
		return fmt.Errorf("rbac: recompute user grades: %w", err)
	}

	// Closure transitive : grades directs + tous leurs ancêtres.
	gradeIDs := make(map[string]struct{}, len(grades))
	for _, g := range grades {
		gradeIDs[g.ID] = struct{}{}
		ancestors, err := svc.Store.GradeAncestors(ctx, g.ID)
		if err != nil {
			return fmt.Errorf("rbac: recompute ancestors: %w", err)
		}
		for _, a := range ancestors {
			gradeIDs[a] = struct{}{}
		}
	}

	// Union des permission IDs sur tous les grades de la closure.
	permIDs := make(map[string]struct{})
	for gid := range gradeIDs {
		perms, err := svc.Store.GradePermissions(ctx, gid)
		if err != nil {
			return fmt.Errorf("rbac: recompute grade perms: %w", err)
		}
		for _, p := range perms {
			permIDs[p.ID] = struct{}{}
		}
	}

	// DELETE + INSERT atomique dans une TX.
	tx, err := svc.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("rbac: recompute begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	if _, err := tx.ExecContext(ctx,
		`DELETE FROM user_effective_permissions WHERE user_id = ?`, userID); err != nil {
		return fmt.Errorf("rbac: recompute delete: %w", err)
	}
	for pid := range permIDs {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO user_effective_permissions(user_id, permission_id) VALUES(?, ?)`,
			userID, pid); err != nil {
			return fmt.Errorf("rbac: recompute insert: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("rbac: recompute commit: %w", err)
	}

	// Invalider L1 pour ce user + bumper la version globale.
	svc.Cache.BumpVersion()
	svc.Cache.Invalidate(userID)
	return nil
}

// RecomputeAll recompute tous les users qui ont au moins un grade.
func (svc *Service) RecomputeAll(ctx context.Context) error {
	rows, err := svc.Store.DB.QueryContext(ctx,
		`SELECT DISTINCT user_id FROM user_grades`)
	if err != nil {
		return fmt.Errorf("rbac: recompute all users: %w", err)
	}
	defer rows.Close()
	var uids []string
	for rows.Next() {
		var uid string
		if err := rows.Scan(&uid); err != nil {
			return err
		}
		uids = append(uids, uid)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, uid := range uids {
		if err := svc.Recompute(ctx, uid); err != nil {
			return err
		}
	}
	return nil
}

// AssignGrade assigne un grade à un user et recompute ses perms effectives.
func (svc *Service) AssignGrade(ctx context.Context, userID, gradeID string) error {
	if err := svc.Store.AssignGrade(ctx, userID, gradeID); err != nil {
		return err
	}
	return svc.Recompute(ctx, userID)
}

// RemoveGrade retire un grade d'un user et recompute.
func (svc *Service) RemoveGrade(ctx context.Context, userID, gradeID string) error {
	if err := svc.Store.RemoveGrade(ctx, userID, gradeID); err != nil {
		return err
	}
	return svc.Recompute(ctx, userID)
}

// GrantPerm accorde une permission à un grade et recompute tous les users du grade.
func (svc *Service) GrantPerm(ctx context.Context, gradeID, permID string) error {
	if err := svc.Store.GrantPerm(ctx, gradeID, permID); err != nil {
		return err
	}
	return svc.recomputeGradeUsers(ctx, gradeID)
}

// RevokePerm retire une permission d'un grade et recompute.
func (svc *Service) RevokePerm(ctx context.Context, gradeID, permID string) error {
	if err := svc.Store.RevokePerm(ctx, gradeID, permID); err != nil {
		return err
	}
	return svc.recomputeGradeUsers(ctx, gradeID)
}

// AddInherit ajoute l'héritage child→parent et recompute les users du grade enfant.
func (svc *Service) AddInherit(ctx context.Context, childID, parentID string) error {
	if err := svc.Store.AddInherit(ctx, childID, parentID); err != nil {
		return err
	}
	return svc.recomputeGradeUsers(ctx, childID)
}

// RemoveInherit retire l'héritage et recompute.
func (svc *Service) RemoveInherit(ctx context.Context, childID, parentID string) error {
	if err := svc.Store.RemoveInherit(ctx, childID, parentID); err != nil {
		return err
	}
	return svc.recomputeGradeUsers(ctx, childID)
}

// recomputeGradeUsers recompute tous les users assignés au grade et à ses descendants.
// Closure descendante : si gradeID = B et A inherits B, les users de A sont aussi recomputed.
func (svc *Service) recomputeGradeUsers(ctx context.Context, gradeID string) error {
	// Closure descendante : gradeID + tous les grades qui en héritent transitivement.
	descendants, err := svc.Store.GradeDescendants(ctx, gradeID)
	if err != nil {
		return fmt.Errorf("rbac: recompute grade descendants: %w", err)
	}
	allGrades := make([]string, 0, 1+len(descendants))
	allGrades = append(allGrades, gradeID)
	allGrades = append(allGrades, descendants...)

	// Collecte des users uniques sur toute la closure.
	seen := make(map[string]struct{})
	var uids []string
	for _, gid := range allGrades {
		rows, err := svc.Store.DB.QueryContext(ctx,
			`SELECT user_id FROM user_grades WHERE grade_id = ?`, gid)
		if err != nil {
			return fmt.Errorf("rbac: grade users for recompute: %w", err)
		}
		for rows.Next() {
			var uid string
			if err := rows.Scan(&uid); err != nil {
				rows.Close()
				return err
			}
			if _, ok := seen[uid]; !ok {
				seen[uid] = struct{}{}
				uids = append(uids, uid)
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
	}

	for _, uid := range uids {
		if err := svc.Recompute(ctx, uid); err != nil {
			return err
		}
	}
	return nil
}
