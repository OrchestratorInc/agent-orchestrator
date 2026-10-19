package sqlite

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestAccountModelCatalogCDCUsesLogicalProject(t *testing.T) {
	db := openMigratedDatabaseCopy(t, 213)
	if err := migrate(db); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO projects (id,path,registered_at,archived_at) VALUES
 ('catalog-a','/tmp/catalog-a',CURRENT_TIMESTAMP,NULL),
 ('catalog-b','/tmp/catalog-b',CURRENT_TIMESTAMP,NULL),
 ('catalog-archived','/tmp/catalog-archived',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, scope, metadata string
		want                  []string
	}{
		{"account project", "@account:project-account", `{"projectId":"catalog-a","accountId":"fast"}`, []string{"catalog-a"}},
		{"account global", "@account:global-account", `{"projectId":"","accountId":"normal"}`, []string{"catalog-a", "catalog-b"}},
		{"ordinary project", "catalog-a", `{}`, []string{"catalog-a"}},
		{"ordinary global", "", `{}`, []string{"catalog-a", "catalog-b"}},
		{"archived project", "catalog-archived", `{}`, []string{"catalog-archived"}},
		{"browse", "@browse:all", `{"projectId":"catalog-a","accountId":"fast"}`, nil},
		{"credential", "@cred:anthropic_api_key", `{"projectId":"catalog-a","accountId":"fast"}`, nil},
		{"missing account metadata", "@account:incomplete", `{"projectId":"catalog-a"}`, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, action := range []string{"insert", "update"} {
				if _, err := db.Exec(`DELETE FROM change_log`); err != nil {
					t.Fatal(err)
				}
				if action == "insert" {
					_, err := db.Exec(`INSERT INTO agent_model_catalog (agent_id,project_id,catalog_json,metadata_json,source,fetched_at) VALUES ('codex',?,'{}',?,'test',CURRENT_TIMESTAMP)`, tc.scope, tc.metadata)
					if err != nil {
						t.Fatal(err)
					}
				} else {
					if _, err := db.Exec(`UPDATE agent_model_catalog SET source='updated' WHERE agent_id='codex' AND project_id=?`, tc.scope); err != nil {
						t.Fatal(err)
					}
				}
				rows, err := db.Query(`SELECT project_id,payload FROM change_log WHERE event_type='session_updated' AND json_extract(payload,'$.kind')='model_catalog' ORDER BY project_id`)
				if err != nil {
					t.Fatal(err)
				}
				defer rows.Close()
				var got []string
				for rows.Next() {
					var id, payload string
					if err := rows.Scan(&id, &payload); err != nil {
						t.Fatal(err)
					}
					var event struct {
						ProjectID string `json:"projectId"`
						AgentID   string `json:"agentId"`
					}
					if err := json.Unmarshal([]byte(payload), &event); err != nil {
						t.Fatal(err)
					}
					if event.ProjectID != id || event.AgentID != "codex" {
						t.Fatalf("payload=%s project=%s", payload, id)
					}
					got = append(got, id)
				}
				if err := rows.Err(); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(got, tc.want) {
					t.Fatalf("%s events=%v want=%v", action, got, tc.want)
				}
			}
		})
	}
}
