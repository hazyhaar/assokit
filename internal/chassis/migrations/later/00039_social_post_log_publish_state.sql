-- +goose Up
-- Distinction BROUILLON vs PUBLICATION PUBLIQUE dans le journal social_post_log.
-- Certains réseaux (TikTok) ne PUBLIENT pas directement : ils déposent un brouillon
-- en boîte de réception, à valider/publier à la main dans l'app du provider. Le worker
-- grave alors la ligne 'sent' (la remise au provider a réussi, at-most-once verrouillé)
-- alors qu'aucun post public n'existe encore — seul PostRef.URL=DraftPending le trahissait.
-- publish_state journalise explicitement l'état : 'published' (URL publique réelle) vs
-- 'draft' (brouillon en attente de validation manuelle). Défaut 'published' : les lignes
-- antérieures et les remises classiques restent des publications publiques.
-- Idempotence : la migration est appliquée une seule fois (sentinel schema_version v39
-- posé par le runner chassis) ; ré-exécuter chassis.Run ne la rejoue pas.
ALTER TABLE social_post_log ADD COLUMN publish_state TEXT NOT NULL DEFAULT 'published'
  CHECK(publish_state IN ('published','draft'));

-- +goose Down
-- SQLite STRICT ne supporte pas DROP COLUMN portable ; no-op au rollback.
