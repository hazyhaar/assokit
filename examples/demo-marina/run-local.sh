#!/usr/bin/env bash
# run-local.sh — provisionne et lance l'instance demo-marina EN LOCAL.
#
# Une instance = une communauté = une réplication : DB locale dédiée + branding
# dédié + identité d'instance, le tout injecté à la BORDURE (cmd/assokit) sans
# toucher au core tenant-agnostic (pkg/api ne lit aucun environnement).
#
# Le mot de passe initial de Dominique se fournit HORS-BANDE via la variable
# d'environnement demo-marina_ADMIN_PASSWORD (jamais hardcodée dans le repo).
# Le compte est créé par la voie native de bootstrap (bootstrap.BootstrapAdmin :
# bcrypt, grade sys-admin, idempotent si la table users est déjà peuplée).
#
# Usage :
#   demo-marina_ADMIN_PASSWORD='<secret hors-bande>' examples/demo-marina/run-local.sh
#
# Lancer depuis la racine du module (/devhoros/assokit).
set -euo pipefail

INSTANCE_DIR="examples/demo-marina"
PORT="${PORT:-8092}"
DB_PATH="${DB_PATH:-${INSTANCE_DIR}/demo-marina.db}"

if [ -z "${demo-marina_ADMIN_PASSWORD:-}" ]; then
  echo "demo-marina_ADMIN_PASSWORD non défini." >&2
  echo "Fournir le mot de passe initial de Dominique hors-bande :" >&2
  echo "  demo-marina_ADMIN_PASSWORD='<secret>' $0" >&2
  exit 1
fi

# Build CGO_ENABLED=0 (driver modernc pur-Go).
CGO_ENABLED=0 go build -o "${INSTANCE_DIR}/demo-marina" ./cmd/assokit

# Bordure : tout le contexte d'instance via l'environnement, consommé par
# cmd/assokit/main.go et passé en api.Options. Le core ne lit rien de tout cela.
PORT="${PORT}" \
DB_PATH="${DB_PATH}" \
BRANDING_DIR="${INSTANCE_DIR}/config" \
BASE_URL="http://127.0.0.1:${PORT}" \
ADMIN_EMAIL="dominique@demo-marina.local" \
ADMIN_PASSWORD="${demo-marina_ADMIN_PASSWORD}" \
CONTACT_EMAIL="contact@demo-marina.local" \
exec "${INSTANCE_DIR}/demo-marina"
