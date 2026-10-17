-- +goose Up

-- Every organization may now run on Coder or Freestyle and picks its default in
-- settings. A project without config.sandboxProvider follows that default, so
-- changing it would move projects created while the organization had a single
-- provider (every Coder project from before projects recorded their provider).
-- Pin each such project to the provider its latest session ran on, or to Coder
-- when it carries Coder dev-kit config and never ran. Only coder and freestyle
-- are pinned: a project last run on a provider since retired (nodeops) keeps
-- following the default rather than pointing at a provider no longer offered.
--
-- Row-level security is forced on these tables, so the backfill works one
-- organization at a time under its tenant context (ao.org_id), as the control
-- plane does, with the service context to find the organizations from their
-- sandboxes. Projects with Coder config whose organization never ran a sandbox
-- are pinned at session start instead (httpapi projectProvider).
-- +goose StatementBegin
DO $$
DECLARE
    org UUID;
BEGIN
    PERFORM set_config('ao.service', 'control-plane', true);
    FOR org IN
        SELECT DISTINCT sb.org_id FROM ao_sandboxes sb WHERE sb.provider IN ('coder', 'freestyle')
    LOOP
        PERFORM set_config('ao.org_id', org::text, true);
        UPDATE ao_projects AS project
        SET config = project.config || jsonb_build_object('sandboxProvider', pinned.provider)
        FROM (
            SELECT p.id, coalesce(
                (SELECT sb.provider
                    FROM ao_sessions s
                    JOIN ao_sandboxes sb ON sb.session_id = s.id AND sb.org_id = s.org_id
                    WHERE s.org_id = p.org_id AND s.project_id = p.id
                        AND sb.provider IN ('coder', 'freestyle')
                    ORDER BY sb.created_at DESC
                    LIMIT 1),
                CASE WHEN jsonb_typeof(p.config -> 'coder') = 'object' THEN 'coder' END
            ) AS provider
            FROM ao_projects p
            WHERE p.org_id = org AND coalesce(p.config ->> 'sandboxProvider', '') = ''
        ) AS pinned
        WHERE project.org_id = org AND project.id = pinned.id AND pinned.provider IS NOT NULL;
    END LOOP;
    PERFORM set_config('ao.org_id', '', true);
    PERFORM set_config('ao.service', '', true);
END
$$;
-- +goose StatementEnd

-- +goose Down
-- The pinned provider is the one each project already ran on; nothing to undo.
SELECT 1;
