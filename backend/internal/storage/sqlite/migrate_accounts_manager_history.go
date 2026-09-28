package sqlite

import (
	"database/sql"
	"fmt"
)

// Earlier account builds occupied 161-168. Physical schema proof is required
// before reassigning those history entries; user choices and journals stay put.
func repairRenumberedAccountsManagerHistory(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var history, accountObjects int
	if err := tx.QueryRow(`SELECT
    (SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='goose_db_version'),
    (SELECT COUNT(*) FROM sqlite_master WHERE name GLOB 'accounts_manager_*')`).Scan(&history, &accountObjects); err != nil {
		return err
	}
	if accountObjects == 0 {
		return nil
	}
	if history == 0 {
		return fmt.Errorf("account schema exists without migration history")
	}
	proofs := []struct {
		query string
		count int
	}{
		{`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name IN ('accounts_manager_routing_policies','accounts_manager_routing_policy_accounts','accounts_manager_session_routes')`, 3},
		{`SELECT COUNT(*) FROM sqlite_master WHERE
    (type='table' AND name IN ('accounts_manager_session_bindings','accounts_manager_binding_clock')) OR
    (type='trigger' AND name IN ('accounts_manager_bindings_insert','accounts_manager_bindings_update','accounts_manager_bindings_delete'))`, 5},
		{`SELECT COUNT(*) FROM sqlite_master WHERE
    (type='table' AND name='accounts_manager_switches') OR
    (type='trigger' AND name IN ('accounts_manager_switches_insert','accounts_manager_switches_update','accounts_manager_switches_delete')) OR
    (type='index' AND name IN ('accounts_manager_switches_active_session','accounts_manager_switches_session_history'))`, 6},
		{`SELECT COUNT(*) FROM sqlite_master WHERE
    (type='table' AND name='accounts_manager_removals') OR
    (type='trigger' AND name IN ('accounts_manager_removal_insert','accounts_manager_removal_update'))`, 3},
		{`SELECT COUNT(*) FROM pragma_table_info('accounts_manager_switches') WHERE name IN ('retired_target_generation','retired_target_handle_id')`, 2},
		{`SELECT COUNT(*) FROM pragma_table_info('accounts_manager_switches') WHERE name IN ('empty_source','source_native_conversation_id')`, 2},
		{`SELECT COUNT(*) FROM sqlite_master WHERE
    (type='table' AND name='accounts_manager_queue_obligations') OR
    (type='trigger' AND name IN ('accounts_manager_queue_on_switch','accounts_manager_queue_on_settlement','accounts_manager_queue_adopted'))`, 4},
		{`SELECT
    (SELECT COUNT(*) FROM pragma_table_info('accounts_manager_removals') WHERE name IN ('stop_started','bindings_revoked')) +
    (SELECT COUNT(*) FROM sqlite_master WHERE
        (type='table' AND name IN ('accounts_manager_removed_choices','accounts_manager_chat_hosts')) OR
        (type='index' AND name='accounts_manager_removal_account') OR
        (type='trigger' AND name IN ('accounts_manager_removed_choice_insert','accounts_manager_removed_choice_delete')))`, 7},
	}
	applied := func(version int) (bool, error) {
		var value int
		err := tx.QueryRow(`SELECT COALESCE((SELECT is_applied FROM goose_db_version WHERE version_id=? ORDER BY id DESC LIMIT 1),0)`, version).Scan(&value)
		return value == 1, err
	}
	completed := 0
	for i, proof := range proofs {
		var count int
		if err := tx.QueryRow(proof.query).Scan(&count); err != nil {
			return fmt.Errorf("inspect account migration %d: %w", 165+i, err)
		}
		if count == 0 {
			continue
		}
		if count != proof.count || completed != i {
			return fmt.Errorf("incomplete account schema at migration %d", 165+i)
		}
		legacy, err := applied(161 + i)
		if err != nil {
			return err
		}
		canonical, err := applied(165 + i)
		if err != nil {
			return err
		}
		if !legacy && !canonical {
			return fmt.Errorf("account migration %d has no applied history", 165+i)
		}
		completed++
	}
	if completed == 0 {
		return fmt.Errorf("account schema has no complete routing migration")
	}
	var automationParts, discussionColumns, fx, gemini int
	if err := tx.QueryRow(`SELECT
    (SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name IN ('automations','automation_runs')) +
    (SELECT COUNT(*) FROM pragma_table_info('sessions') WHERE name IN ('automation_run_id','automation_launch_completed')),
    (SELECT COUNT(*) FROM pragma_table_info('pr') WHERE name IN ('discussion_comment_count','discussion_commenters_json')),
    (SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='sessions' AND instr(sql,'''fx''')>0),
    (SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='sessions' AND instr(sql,'''gemini''')>0)`).Scan(&automationParts, &discussionColumns, &fx, &gemini); err != nil {
		return err
	}
	if (automationParts != 0 && automationParts != 4) || (discussionColumns != 0 && discussionColumns != 2) {
		return fmt.Errorf("incomplete main schema at colliding account migrations")
	}
	for i := 0; i < completed; i++ {
		present, err := applied(165 + i)
		if err != nil {
			return err
		}
		if !present {
			if _, err := tx.Exec(`INSERT INTO goose_db_version(version_id,is_applied) VALUES (?,1)`, 165+i); err != nil {
				return err
			}
		}
	}
	for i, present := range []bool{automationParts == 4, discussionColumns == 0, fx != 0, gemini != 0} {
		if !present {
			if _, err := tx.Exec(`DELETE FROM goose_db_version WHERE version_id=?`, 161+i); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}
