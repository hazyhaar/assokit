// CLAUDE:SUMMARY Middleware chi RBAC : injecte rbac.Service + userID en ctx (M-ASSOKIT-RBAC-3).
package middleware

import (
	"net/http"

	"github.com/hazyhaar/assokit/pkg/horui/perms"
	"github.com/hazyhaar/assokit/pkg/horui/rbac"
)

// RBAC injecte svc dans le contexte et extrait l'userID depuis le middleware Auth.
// À placer après le middleware Auth dans la chaîne.
func RBAC(svc *rbac.Service) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := perms.ContextWithService(r.Context(), svc)
			if u := UserFromContext(r.Context()); u != nil {
				ctx = perms.ContextWithUserID(ctx, u.ID)
			}
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
