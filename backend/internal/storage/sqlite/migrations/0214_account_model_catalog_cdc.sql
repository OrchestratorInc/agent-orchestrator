-- +goose Up
-- Account cache keys are not project IDs; notify the logical project recorded
-- by the service, or each active project for an account-global catalog.
-- +goose StatementBegin
DROP TRIGGER IF EXISTS agent_model_catalog_cdc_update;
DROP TRIGGER IF EXISTS agent_model_catalog_cdc_insert;

CREATE TRIGGER agent_model_catalog_cdc_insert
AFTER INSERT ON agent_model_catalog
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    SELECT projects.id, NULL, 'session_updated',
        json_object('kind', 'model_catalog', 'agentId', NEW.agent_id, 'projectId', projects.id),
        datetime('now')
    FROM projects
    WHERE (NEW.project_id = '' AND projects.archived_at IS NULL)
       OR projects.id = NEW.project_id
       OR (NEW.project_id GLOB '@account:*'
           AND json_type(NEW.metadata_json, '$.accountId') = 'text'
           AND json_extract(NEW.metadata_json, '$.accountId') <> ''
           AND (projects.id = json_extract(NEW.metadata_json, '$.projectId')
                OR (json_extract(NEW.metadata_json, '$.projectId') = '' AND projects.archived_at IS NULL)));
END;

CREATE TRIGGER agent_model_catalog_cdc_update
AFTER UPDATE ON agent_model_catalog
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    SELECT projects.id, NULL, 'session_updated',
        json_object('kind', 'model_catalog', 'agentId', NEW.agent_id, 'projectId', projects.id),
        datetime('now')
    FROM projects
    WHERE (NEW.project_id = '' AND projects.archived_at IS NULL)
       OR projects.id = NEW.project_id
       OR (NEW.project_id GLOB '@account:*'
           AND json_type(NEW.metadata_json, '$.accountId') = 'text'
           AND json_extract(NEW.metadata_json, '$.accountId') <> ''
           AND (projects.id = json_extract(NEW.metadata_json, '$.projectId')
                OR (json_extract(NEW.metadata_json, '$.projectId') = '' AND projects.archived_at IS NULL)));
END;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS agent_model_catalog_cdc_update;
DROP TRIGGER IF EXISTS agent_model_catalog_cdc_insert;

CREATE TRIGGER agent_model_catalog_cdc_insert
AFTER INSERT ON agent_model_catalog
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    SELECT projects.id, NULL, 'session_updated',
        json_object('kind', 'model_catalog', 'agentId', NEW.agent_id, 'projectId', projects.id),
        datetime('now')
    FROM projects
    WHERE (NEW.project_id = '' AND projects.archived_at IS NULL)
       OR projects.id = NEW.project_id;
END;

CREATE TRIGGER agent_model_catalog_cdc_update
AFTER UPDATE ON agent_model_catalog
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    SELECT projects.id, NULL, 'session_updated',
        json_object('kind', 'model_catalog', 'agentId', NEW.agent_id, 'projectId', projects.id),
        datetime('now')
    FROM projects
    WHERE (NEW.project_id = '' AND projects.archived_at IS NULL)
       OR projects.id = NEW.project_id;
END;
-- +goose StatementEnd
