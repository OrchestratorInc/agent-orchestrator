package sqlite

import (
	"database/sql"
	"strings"
	"testing"
)

// Break caught: deleting a session held the single writer for minutes. The
// delete cascades through every foreign key below, and SQLite looks up each
// deleted row's children by the child column; without an index that reads the
// whole child table, once per deleted turn for the turn_id columns. A foreign
// key added later into this cascade fails here until its column is indexed.
func TestSessionDeleteCascadeLookupsUseIndexes(t *testing.T) {
	db := openMigratedTestDB(t)
	keys := foreignKeys(t, db)

	deleted := map[string]bool{"sessions": true}
	for grew := true; grew; {
		grew = false
		for _, k := range keys {
			if k.onDelete == "CASCADE" && deleted[k.parent] && !deleted[k.child] {
				deleted[k.child], grew = true, true
			}
		}
	}

	checked := 0
	for _, k := range keys {
		if !deleted[k.parent] {
			continue
		}
		checked++
		args := make([]any, len(k.columns))
		for i := range args {
			args[i] = "parent-key"
		}
		plan := queryPlan(t, db, `SELECT 1 FROM "`+k.child+`" WHERE "`+
			strings.Join(k.columns, `" = ? AND "`)+`" = ?`, args...)
		if len(plan) == 0 || !strings.HasPrefix(plan[0], "SEARCH ") {
			t.Errorf("deleting %s looks up %s(%s) (ON DELETE %s) without an index: %q",
				k.parent, k.child, strings.Join(k.columns, ", "), k.onDelete, plan)
		}
	}
	// sessions alone has more than 25 child tables; a broken walk must not pass.
	if checked < 25 {
		t.Fatalf("checked only %d foreign keys in the session delete cascade", checked)
	}
}

type foreignKey struct {
	child, parent, onDelete string
	columns                 []string
}

func foreignKeys(t *testing.T, db *sql.DB) []*foreignKey {
	t.Helper()
	rows, err := db.Query(`
SELECT m.name, f.id, f."table", f."from", f.on_delete
FROM sqlite_master m, pragma_foreign_key_list(m.name) f
WHERE m.type = 'table'
ORDER BY m.name, f.id, f.seq`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	type keyID struct {
		table string
		id    int
	}
	var keys []*foreignKey
	byID := map[keyID]*foreignKey{}
	for rows.Next() {
		var child, parent, column, onDelete string
		var id int
		if err := rows.Scan(&child, &id, &parent, &column, &onDelete); err != nil {
			t.Fatal(err)
		}
		k := keyID{child, id}
		if byID[k] == nil {
			byID[k] = &foreignKey{child: child, parent: parent, onDelete: onDelete}
			keys = append(keys, byID[k])
		}
		byID[k].columns = append(byID[k].columns, column)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return keys
}

func queryPlan(t *testing.T, db *sql.DB, query string, args ...any) []string {
	t.Helper()
	rows, err := db.Query("EXPLAIN QUERY PLAN "+query, args...)
	if err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	defer func() { _ = rows.Close() }()
	var plan []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return plan
}
