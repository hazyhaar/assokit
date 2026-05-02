# Admin Donations — Workflow Boris

UI admin pour suivre les donations HelloAsso de l'asso Nonpossumus.

## Routes

| Verbe | URL | Description |
|---|---|---|
| GET | `/admin/donations` | Liste paginée keyset (50/page), filtres URL |
| GET | `/admin/donations/stats.json` | Stats agrégées (cache 60s) |
| GET | `/admin/donations/export.csv` | Export CSV UTF-8 BOM (Excel-FR) |
| GET | `/admin/donations/{id}` | Détail donation + croisement membre |
| POST | `/admin/donations/{id}/erase-email` | RGPD soft-delete email donateur |
| POST | `/admin/donations/{id}/match-user` | Forcer match user_id manuel |

Toutes protégées par `requireAdmin` (role=admin sinon 403).

## Workflow Boris (compta mensuelle)

1. Aller sur `/admin/donations`.
2. Filtrer par mois : `?from=2026-04-01&to=2026-04-30`.
3. Optionnel : `&status=paid` pour exclure pending/refunded.
4. Cliquer "Exporter CSV" → fichier `donations-YYYYMMDD-HHMM.csv`.
5. Ouvrir dans LibreOffice / Excel (BOM UTF-8 garantit accents corrects).
6. Importer dans logiciel compta (colonnes : id, date, donateur, email, type, montant_eur, currency, status, user_id, user_email, is_member, helloasso_payment_id).

## Filtres URL supportés

- `from` / `to` : dates `YYYY-MM-DD` (inclusives sur `paid_at` ou fallback `created_at`).
- `status` : `pending|authorized|paid|refunded|failed`.
- `type` : `Donation|Membership|...` (helloasso_form_type).
- `min_eur` / `max_eur` : bornes montant en euros.
- `q` : LIKE sur donor_name OR donor_email (case-insensitive).
- `cursor` : pagination keyset (format `created_at|id`).

## RGPD — Soft-delete email

POST `/admin/donations/{id}/erase-email` :
- `donor_email = ''`
- `donor_name = '[supprimé RGPD]'`
- Row gardée (compta + statistiques).
- Audit dans logs slog : `admin_donation_email_erased actor_user_id=... donation_id=...`.

Action déclenchée sur demande nominative du donateur. Irréversible.

## Croisement membres

Détail affiche `is_member=true` si `user_roles` contient `(user_id, 'member')`.
Liste affiche le pourcentage `donateurs_membres_pct` dans les stats globales.

## Stats endpoint (cache 60s)

`/admin/donations/stats.json` retourne :

```json
{
  "total_cumul_eur": 12450.00,
  "total_mois_courant_eur": 750.00,
  "nb_donateurs_uniques": 47,
  "nb_dons_mois": 12,
  "montant_moyen_eur": 62.50,
  "donateurs_membres_pct": 65.4,
  "top_3_paliers": [{"montant": 50, "count": 18}],
  "evolution_30j": [{"date": "2026-04-15", "total_eur": 120}]
}
```

Cache in-memory protégé par `sync.RWMutex`, fenêtre 60 secondes.
Première requête après 60s déclenche un recompute (5 queries SQL).
Invalidation programmatique : `invalidateStatsCache()` (réservé tests).

## Sécurité — UX-RGPD compromise

- **Liste** : email masqué `a***@example.org` (pour ne pas leaker en captures d'écran admin).
- **Détail** : email full visible (Boris a besoin de contacter le donateur).
- **CSV export** : email full (export = action explicite, journalisée dans audit log).

## Tests gardiens (12 PASS)

`go test ./internal/handlers/ -run TestAdminDonations -v` :
- NonAdminReturns403, ListPaginationKeyset, FilterByStatus, StatsCorrect,
  StatsCacheRespects60s, ExportCSVCorrectFormat, ExportCSVRespectsFilter,
  RGPDSoftDeleteEmailKeepsRow, ManualUserMatch, ListMaskedEmail,
  HeaderColumns, ContentDispositionAttachment.
