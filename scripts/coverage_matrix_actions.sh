#!/usr/bin/env bash
# CLAUDE:SUMMARY M-ASSOKIT-AUDIT-FIX-3 Axe 3a — coverage matrix actions registry vs tests.
# Dump tous les action.ID via helper Go inline, grep récursif chaque ID dans *_test.go,
# génère un Markdown table dans plans/MCP_COVERAGE_MATRIX_<date>.md.
#
# Usage : bash scripts/coverage_matrix_actions.sh
#
# Sortie : plans/MCP_COVERAGE_MATRIX_$(date +%Y-%m-%d).md
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${REPO_ROOT}"

OUT_DIR="${REPO_ROOT}/plans"
OUT_FILE="${OUT_DIR}/MCP_COVERAGE_MATRIX_$(date +%Y-%m-%d).md"
mkdir -p "${OUT_DIR}"

TMP_DIR="$(mktemp -d)"
trap 'rm -rf "${TMP_DIR}"' EXIT

# --- 1. Helper Go inline qui dump Registry.All() en JSON. ---
HELPER="${TMP_DIR}/dump_registry.go"
cat >"${HELPER}" <<'GO'
package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/hazyhaar/assokit/pkg/actions"
	"github.com/hazyhaar/assokit/pkg/actions/seeds"
)

func main() {
	reg := actions.NewRegistry()
	seeds.InitAll(reg)
	type row struct {
		ID     string `json:"id"`
		Domain string `json:"domain"`
	}
	out := make([]row, 0)
	for _, a := range reg.All() {
		domain := a.ID
		for i := 0; i < len(a.ID); i++ {
			if a.ID[i] == '.' {
				domain = a.ID[:i]
				break
			}
		}
		out = append(out, row{ID: a.ID, Domain: domain})
	}
	if err := json.NewEncoder(os.Stdout).Encode(out); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
GO

# Build dans le module courant via go run avec un dossier dédié.
HELPER_DIR="${TMP_DIR}/cmd_dump"
mkdir -p "${HELPER_DIR}"
mv "${HELPER}" "${HELPER_DIR}/main.go"

JSON_OUT="${TMP_DIR}/registry.json"
(cd "${REPO_ROOT}" && go run "${HELPER_DIR}/main.go") >"${JSON_OUT}"

# --- 2. Pour chaque action_id, count d'occurrences dans *_test.go. ---
{
	echo "# MCP Actions Coverage Matrix"
	echo ""
	echo "> Genere automatiquement par scripts/coverage_matrix_actions.sh le $(date +%Y-%m-%d_%H:%M:%S)"
	echo ""
	echo "| Action ID | Domain | Tests count | Status |"
	echo "|---|---|---|---|"
} >"${OUT_FILE}"

# Parse JSON ligne par ligne via python (fallback sed si python absent).
if command -v python3 >/dev/null 2>&1; then
	python3 -c "
import json,sys
data=json.load(open('${JSON_OUT}'))
for r in data:
    print(r['id']+'\t'+r['domain'])
" >"${TMP_DIR}/ids.tsv"
else
	# fallback: extraction grep simple
	grep -oE '"id":"[^"]+","domain":"[^"]+"' "${JSON_OUT}" | \
		sed -E 's/"id":"([^"]+)","domain":"([^"]+)"/\1\t\2/' >"${TMP_DIR}/ids.tsv"
fi

TOTAL=0
COVERED=0
while IFS=$'\t' read -r action_id domain; do
	[ -z "${action_id}" ] && continue
	TOTAL=$((TOTAL+1))
	# Count occurrences dans *_test.go (string literal de l'ID).
	COUNT=$( { grep -r --include='*_test.go' -F "\"${action_id}\"" "${REPO_ROOT}" 2>/dev/null || true; } | wc -l | tr -d ' ')
	COUNT=${COUNT:-0}
	if [ "${COUNT}" -gt 0 ]; then
		STATUS="OK"
		COVERED=$((COVERED+1))
	else
		STATUS="MISSING"
	fi
	echo "| ${action_id} | ${domain} | ${COUNT} | ${STATUS} |" >>"${OUT_FILE}"
done <"${TMP_DIR}/ids.tsv"

{
	echo ""
	echo "**Total** : ${COVERED}/${TOTAL} actions couvertes par >=1 test."
} >>"${OUT_FILE}"

echo "Matrix ecrite dans ${OUT_FILE}"
echo "Couverture : ${COVERED}/${TOTAL}"
