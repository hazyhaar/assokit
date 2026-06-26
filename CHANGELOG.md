# Changelog

Format inspiré de [Keep a Changelog](https://keepachangelog.com). Versionnage
sémantique (SemVer). Avant la `1.0.0`, l'API publique (`pkg/api.Options`) peut
encore évoluer.

## [0.1.0] — non publié

Première version généralisée, issue de la reprise et de la refonte en 6 strates
(S0→S5). Kit web communautaire généraliste (au-delà des seules associations).

### Ajouté
- Messagerie privée membre↔membre, events/agenda, cotisations/adhésions,
  newsletter/diffusion (tous dans le core, actions registry HTTP+MCP).
- Menu admin **Setup** (diagnostic de configuration par fonction + édition DB-safe).
- Connecteur **LiveKit** (jetons d'accès visio + UI de salle) ; émission
  d'événements via `EventSink` (webhook HTTP générique signé + adaptateur bus
  injectable à la bordure).
- Module **`nodetree`** extrait (moteur pur de hiérarchie récursive, MIT autonome) ;
  forum/pages/catégories rebranchés en couches par-dessus.

### Modifié / durci
- Core **tenant-agnostic** : plus aucune lecture d'environnement hors de la
  bordure (`cmd/assokit`) ; configuration injectée via `api.Options`.
- Migrations unifiées sur un runner unique (élimination de goose et de son état
  global) ; ids **UUIDv7** (`google/uuid`).
- Sécurité périphérie : HMAC de session constant-time, `COOKIE_SECRET` fatal en
  prod, pragmas SQLite (FK, WAL, busy_timeout) posés au runtime, PKCE S256-only,
  anti-spoofing IP, purge/TTL DCR, rate-limiters bornés.

### Corrigé
- ~20 actions « faux OK » qui n'avaient jamais fonctionné via le registry/MCP
  (account.delete_self, users.deactivate/reactivate, forum.post.*, pages.*,
  signup.*, branding.*, mailer.outbox.*, feedback.triage, profile.edit_self) —
  toutes réparées et gardées par des tests comportementaux (58/58 actions).

### Dette connue
Voir `DETTE.md` (MCP resumability, verrouillage anti-bruteforce du login web).
