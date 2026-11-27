-- +goose Up
-- CI run insights, owner decision D17: a finding can be dismissed. It is hidden from the Do next list and the
-- counts until hidden_until (7 days later) and comes back if its condition still holds (the engine keeps
-- refreshing it, or resolves it when the condition ends). An empty string means not dismissed.
ALTER TABLE insight_findings ADD COLUMN hidden_until TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE insight_findings DROP COLUMN hidden_until;
