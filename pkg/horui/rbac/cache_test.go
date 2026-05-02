// CLAUDE:SUMMARY Tests cache L1 RBAC : version bump invalidation, Set/Get, Invalidate.
package rbac

import (
	"testing"
)

// TestCache_GetMiss : entrée absente → (nil, false).
func TestCache_GetMiss(t *testing.T) {
	c := &Cache{}
	perms, ok := c.Get("user-1")
	if ok || perms != nil {
		t.Errorf("miss attendu, got (%v, %v)", perms, ok)
	}
}

// TestCache_SetGet : Set puis Get retourne les perms.
func TestCache_SetGet(t *testing.T) {
	c := &Cache{}
	perms := map[string]struct{}{"feedback.triage": {}, "forum.post": {}}
	c.Set("user-1", perms)

	got, ok := c.Get("user-1")
	if !ok {
		t.Fatal("want hit, got miss")
	}
	if _, has := got["feedback.triage"]; !has {
		t.Error("feedback.triage should be in cache")
	}
}

// TestCache_VersionBumpInvalidatesL1 : BumpVersion() → anciennes entrées invalides.
func TestCache_VersionBumpInvalidatesL1(t *testing.T) {
	c := &Cache{}
	c.Set("user-1", map[string]struct{}{"perm.x": {}})

	// Vérifie hit avant bump
	if _, ok := c.Get("user-1"); !ok {
		t.Fatal("should be hit before bump")
	}

	c.BumpVersion()

	// Après bump → stale
	if _, ok := c.Get("user-1"); ok {
		t.Error("should be miss after BumpVersion")
	}
}

// TestCache_Invalidate : Invalidate() supprime l'entrée.
func TestCache_Invalidate(t *testing.T) {
	c := &Cache{}
	c.Set("user-1", map[string]struct{}{"x": {}})
	c.Invalidate("user-1")
	if _, ok := c.Get("user-1"); ok {
		t.Error("should be miss after Invalidate")
	}
}

// TestCache_MultipleUsers : entrées indépendantes.
func TestCache_MultipleUsers(t *testing.T) {
	c := &Cache{}
	c.Set("u1", map[string]struct{}{"a": {}})
	c.Set("u2", map[string]struct{}{"b": {}})
	c.Invalidate("u1")

	if _, ok := c.Get("u1"); ok {
		t.Error("u1 should be invalidated")
	}
	if _, ok := c.Get("u2"); !ok {
		t.Error("u2 should still be valid")
	}
}
