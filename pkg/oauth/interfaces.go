// Package oauth expose la pile OAuth2/OIDC d'assokit en tant que paquet public importable.
// Les consommateurs fournissent leurs propres implémentations des interfaces ci-dessous
// pour découpler le Storage du schéma SQL cible.
package oauth

import "context"

// UserInfo regroupe les champs utilisateur consommés par le Storage pour peupler
// les claims OIDC (userinfo endpoint, ID token).
type UserInfo struct {
	ID          string
	Email       string
	DisplayName string
}

// UserProvider fournit les informations d'un utilisateur au Storage OAuth.
// Les consommateurs implémentent cette interface sur leur table users.
type UserProvider interface {
	// GetUser retourne les informations de l'utilisateur identifié par id.
	// Retourne (nil, nil) si l'utilisateur n'existe pas.
	GetUser(ctx context.Context, id string) (*UserInfo, error)
	// ListPermissions retourne les noms des permissions effectives de l'utilisateur.
	ListPermissions(ctx context.Context, id string) ([]string, error)
}

// IDGenerator produit des identifiants uniques pour les entités OAuth
// (auth requests, tokens, clients DCR).
// Implémentation de référence dans pkg/uid : uid.New() (UUIDv7).
type IDGenerator interface {
	NewID() string
}

// IDGeneratorFunc est un adaptateur fonctionnel vers IDGenerator.
type IDGeneratorFunc func() string

func (f IDGeneratorFunc) NewID() string { return f() }
