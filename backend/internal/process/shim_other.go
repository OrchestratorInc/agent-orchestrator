//go:build !windows

package process

// resolveShimArgv is a no-op outside Windows: no other platform hides a command
// behind a batch shim that re-parses its arguments.
func resolveShimArgv(name string, args []string) (string, []string) {
	return name, args
}
