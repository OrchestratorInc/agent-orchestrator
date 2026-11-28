package sqlite

import (
	"database/sql/driver"
	"fmt"

	sqlitedriver "modernc.org/sqlite"

	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/searchtext"
)

// ao_search_body(body) decodes a session search document's stored text. The
// index is contentless, so removing a row from it means telling FTS5 the exact
// text that was indexed; the delete trigger reads it back with this function.
// It is registered before any connection opens, so every connection has it.
func init() {
	sqlitedriver.MustRegisterDeterministicScalarFunction("ao_search_body", 1,
		func(_ *sqlitedriver.FunctionContext, args []driver.Value) (driver.Value, error) {
			switch value := args[0].(type) {
			case nil:
				return "", nil
			case []byte:
				return searchtext.Decode(value), nil
			case string:
				return searchtext.Decode([]byte(value)), nil
			default:
				return nil, fmt.Errorf("ao_search_body: unexpected %T", value)
			}
		})
}
