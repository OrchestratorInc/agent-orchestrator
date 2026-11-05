-- +goose Up
-- +goose StatementBegin
CREATE TRIGGER scheduler_workflow_created AFTER INSERT ON scheduler_workflow_state BEGIN
 INSERT INTO change_log(event_type,payload) VALUES('scheduler_updated',json_object('kind','workflow','id',NEW.workflow_id,'revision',NEW.revision));
END;
CREATE TRIGGER scheduler_workflow_updated AFTER UPDATE ON scheduler_workflow_state BEGIN
 INSERT INTO change_log(event_type,payload) VALUES('scheduler_updated',json_object('kind','workflow','id',NEW.workflow_id,'revision',NEW.revision));
END;
CREATE TRIGGER scheduler_budget_reservation_updated AFTER UPDATE ON budget_reservations BEGIN
 INSERT INTO change_log(event_type,payload) VALUES('scheduler_updated',json_object('kind','budget_reservation','id',NEW.admission_id,'state',NEW.state,'revision',NEW.revision));
END;
-- +goose StatementEnd

-- +goose Down
DROP TRIGGER scheduler_budget_reservation_updated;
DROP TRIGGER scheduler_workflow_updated;
DROP TRIGGER scheduler_workflow_created;
