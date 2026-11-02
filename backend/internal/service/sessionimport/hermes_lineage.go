package sessionimport

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
)

type hermesSegment struct {
	id, parent, reason, source, cwd, title string
	started, updated                      float64
	count                                 int64
	hidden, ended, fork                    bool
}

// Only a compression-ended parent and an unmarked continuation form one
// conversation. Reset/branch/delegate children remain separate native chats.
func hermesLineages(ctx context.Context, db *sql.DB, requested string) (map[string][]hermesSegment, error) {
	columns := map[string]bool{}
	rows, err := db.QueryContext(ctx, "PRAGMA table_info(sessions)")
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var cid, notNull, pk int
		var name, kind string
		var value sql.NullString
		if err := rows.Scan(&cid, &name, &kind, &notNull, &value, &pk); err != nil {
			_ = rows.Close()
			return nil, err
		}
		columns[name] = true
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, err
	}
	optional := func(name, fallback string) string {
		if columns[name] {
			return "s." + name
		}
		return fallback
	}
	prefix, from := "", "FROM sessions s LEFT JOIN messages m ON m.session_id=s.id GROUP BY s.id"
	activity, count := "COALESCE(MAX(m.timestamp),s.started_at)", "COUNT(m.id)"
	var args []any
	if requested != "" {
		// Fresh validation reads only this connected compression component. A
		// branch/reset marker is resolved below; unrelated chats and gateway
		// messages never enter the per-candidate aggregation or graph walk.
		from = "FROM sessions s WHERE s.id=?"
		args = []any{requested}
		if columns["parent_session_id"] && columns["end_reason"] {
			prefix = `WITH RECURSIVE component(id) AS (
				SELECT id FROM sessions WHERE id=?
				UNION SELECT child.parent_session_id FROM sessions child
				JOIN component c ON c.id=child.id JOIN sessions parent ON parent.id=child.parent_session_id
				WHERE parent.end_reason='compression'
				UNION SELECT child.id FROM sessions child JOIN component c ON c.id=child.parent_session_id
				JOIN sessions parent ON parent.id=c.id WHERE parent.end_reason='compression'
			) `
			from = "FROM sessions s WHERE s.id IN (SELECT id FROM component)"
		}
		activity = "COALESCE((SELECT MAX(timestamp) FROM messages WHERE session_id=s.id),s.started_at)"
		count = "(SELECT COUNT(*) FROM messages WHERE session_id=s.id)"
	}
	query := prefix + `SELECT s.id,COALESCE(` + optional("parent_session_id", "NULL") + `,''),
		COALESCE(` + optional("end_reason", "NULL") + `,''),s.source,COALESCE(s.cwd,''),COALESCE(s.title,''),
		s.started_at,` + activity + `,` + count + `,COALESCE(s.hidden,0),
		` + optional("ended_at", "NULL") + `,COALESCE(` + optional("model_config", "NULL") + `,'{}')
		` + from
	rows, err = db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	nodes := map[string]hermesSegment{}
	children := map[string][]hermesSegment{}
	for rows.Next() {
		var node hermesSegment
		var hidden int
		var ended sql.NullFloat64
		var config string
		if err := rows.Scan(&node.id, &node.parent, &node.reason, &node.source, &node.cwd, &node.title, &node.started, &node.updated, &node.count, &hidden, &ended, &config); err != nil {
			return nil, err
		}
		node.hidden, node.ended = hidden != 0, ended.Valid
		var markers map[string]json.RawMessage
		if json.Unmarshal([]byte(config), &markers) != nil {
			node.fork = true // Never merge unreadable fork provenance into a parent.
		}
		for _, key := range []string{"_branched_from", "_delegate_from", "_reset_from"} {
			if raw := strings.TrimSpace(string(markers[key])); raw != "" && raw != "null" {
				node.fork = true
			}
		}
		nodes[node.id] = node
		children[node.parent] = append(children[node.parent], node)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	edge := func(parent, child hermesSegment) bool {
		return parent.reason == "compression" && !child.fork && child.source != "tool"
	}
	rank := func(node hermesSegment) int {
		if node.reason == "compression" {
			return 0
		}
		if !node.ended {
			return 1
		}
		return 2
	}
	groups := map[string][]hermesSegment{}
	for _, node := range nodes {
		root := node
		seen := map[string]bool{node.id: true}
		for root.parent != "" {
			parent, ok := nodes[root.parent]
			if !ok || !edge(parent, root) || seen[parent.id] {
				break
			}
			root, seen[parent.id] = parent, true
		}
		if _, exists := groups[root.id]; exists {
			continue
		}
		lineage := []hermesSegment{root}
		seen = map[string]bool{root.id: true}
		for current := root; current.reason == "compression"; {
			var next *hermesSegment
			for _, child := range children[current.id] {
				if !edge(current, child) || seen[child.id] {
					continue
				}
				if next == nil || rank(child) < rank(*next) || rank(child) == rank(*next) && (child.updated > next.updated || child.updated == next.updated && (child.started > next.started || child.started == next.started && child.id > next.id)) {
					copyOfChild := child
					next = &copyOfChild
				}
			}
			if next == nil {
				break
			}
			lineage = append(lineage, *next)
			current, seen[next.id] = *next, true
		}
		groups[root.id] = lineage
	}
	return groups, nil
}

func hermesLineageVisible(lineage []hermesSegment) bool {
	for _, node := range lineage {
		if !node.hidden && (node.source == "cli" || node.source == "tui" || node.source == "oneshot") {
			return true
		}
	}
	return false
}
