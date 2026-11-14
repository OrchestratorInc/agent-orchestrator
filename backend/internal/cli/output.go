package cli

import (
	"encoding/json"
	"fmt"
	"io"
)

func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func writeClaimPRCheckout(w io.Writer, branchChanged bool) error {
	// An unchanged branch does not establish that HEAD matches the PR head.
	checkout := "not performed; workspace unchanged"
	if branchChanged {
		checkout = "switched to PR branch"
	}
	if _, err := fmt.Fprintf(w, "  checkout: %s\n", checkout); err != nil {
		return err
	}
	if !branchChanged {
		_, err := fmt.Fprintln(w, "  Before editing: preserve local changes, fetch the PR, then switch using its verified full head SHA. Do not use FETCH_HEAD as a checkout target: a branch with that name can make Git fail after changing the index/worktree. Verify git status, branch and HEAD after switching, even on failure.")
		return err
	}
	return nil
}
