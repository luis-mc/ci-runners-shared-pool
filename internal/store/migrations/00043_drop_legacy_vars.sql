-- +goose Up
-- CRSP229: the repository setting legacy_vars is gone (github-integration
-- R5.39 removed). The saved repository state (00016) keeps each policy as a
-- JSON object; the setting is removed from every saved policy, so no policy
-- carries it after the upgrade. The other fields of a policy stay as they are.
UPDATE repos_state
SET body = json_set(body, '$.policies', json((
    SELECT json_group_object(p.key, json_remove(p.value, '$.LegacyVars', '$.legacy_vars'))
    FROM json_each(repos_state.body, '$.policies') AS p
)))
WHERE json_valid(body) AND json_type(body, '$.policies') = 'object';

-- +goose Down
-- The setting is not put back: it was off by default and nothing reads it.
SELECT 1;
