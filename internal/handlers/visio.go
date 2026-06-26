// Fichier retiré dans le lot L2a (feat: salon visio gardée par appartenance).
// MountVisioRoutes, handleVisioRoom et renderVisioRoom ont été supprimés :
// ils forgeaient un jeton LiveKit pour n'importe quelle salle sans contrôle
// d'appartenance (bypass de sécurité). La nouvelle route est
// GET /salon/{slug}/visio (internal/handlers/salon_visio.go, gardée par IsMember + visio_enabled).
package handlers
