// Package app fournit AppDeps — le panier de dépendances transverses passé aux handlers.
// L'App réelle (listen, shutdown, wiring HTTP) vit dans pkg/api pour respecter l'entonnoir
// cmd→internal→pkg (pkg/api orchestre, internal/* fournissent les couches métier).
package app
