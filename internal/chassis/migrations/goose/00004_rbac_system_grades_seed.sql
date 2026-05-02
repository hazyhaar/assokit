-- +goose Up
-- Seed des grades système (system=1, non supprimables via UI).
INSERT INTO grades(id, name, system) VALUES
    ('sys-admin',     'admin',     1),
    ('sys-moderator', 'moderator', 1),
    ('sys-member',    'member',    1),
    ('sys-guest',     'guest',     1)
ON CONFLICT(id) DO NOTHING;

-- +goose Down
DELETE FROM grades WHERE id IN ('sys-admin','sys-moderator','sys-member','sys-guest');
