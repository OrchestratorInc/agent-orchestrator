package sqlite

import (
	"database/sql/driver"
	"fmt"

	sqlitedriver "modernc.org/sqlite"

	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/searchtext"
)

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
