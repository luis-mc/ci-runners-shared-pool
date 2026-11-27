-- +goose Up
-- HND6, HND12: the runner registry keeps the route apart from the name and a
-- size class (runners R5.70, R5.71). Routing never reads the name again.
ALTER TABLE runners ADD COLUMN route_pool TEXT NOT NULL DEFAULT '';
ALTER TABLE runners ADD COLUMN route_queue TEXT NOT NULL DEFAULT 'ci' CHECK (route_queue IN ('ci', 'light'));
ALTER TABLE runners ADD COLUMN size_class TEXT NOT NULL DEFAULT 'medium' CHECK (size_class IN ('small', 'medium', 'large', 'xl'));

-- The one time the route is derived from old data: the light label, or the
-- legacy light name prefix of records made before the field existed. After
-- this the route is stored and the name is never parsed.
UPDATE runners SET route_queue = 'light'
WHERE lower(name) LIKE 'crsp-light-%' OR labels LIKE '%"crsp-light"%';

-- +goose Down
ALTER TABLE runners DROP COLUMN size_class;
ALTER TABLE runners DROP COLUMN route_queue;
ALTER TABLE runners DROP COLUMN route_pool;
