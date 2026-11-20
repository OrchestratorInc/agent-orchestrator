// A subprocess fixture for checking the environment and stdin that Cline's
// generated wrappers actually deliver to the AO hook dispatcher.
package main

import (
	"encoding/json"
	"io"
	"os"
)

func main() {
	payload, err := io.ReadAll(os.Stdin)
	if err != nil {
		panic(err)
	}
	env := map[string]string{}
	for _, key := range []string{"AO_SESSION_ID", "AO_RUNTIME_LAUNCH_ID", "AO_DATA_DIR", "AO_RUN_FILE", "AO_REVIEW_SESSION_ID", "AO_REVIEW_WORKER_SESSION_ID", "CLINE_TEST_PROVIDER_ENV"} {
		env[key] = os.Getenv(key)
	}
	data, err := json.Marshal(struct {
		Args    []string
		Env     map[string]string
		Payload string
	}{os.Args[1:], env, string(payload)})
	if err != nil {
		panic(err)
	}
	if err := os.WriteFile(os.Getenv("CLINE_TEST_CAPTURE"), data, 0o600); err != nil {
		panic(err)
	}
	// Hooks must still print Cline's continuation response when AO fails.
	os.Exit(1)
}
