package conpty

// foregroundReader is implemented by native PTYs that can name the program in
// the foreground of their terminal. Hosts without it never report a
// foreground (Windows ConPTY has no process groups to ask).
type foregroundReader interface {
	ForegroundPGID() (pgid int, ok bool)
}

const (
	// maxForegroundArgs bounds the argv a watcher receives. Interpreters put
	// the script that names the program within the first few entries.
	maxForegroundArgs = 8
	// maxForegroundArgLen bounds one argv entry; long prompts passed on the
	// command line are cut rather than copied around whole.
	maxForegroundArgLen = 512
)

func capForegroundArgv(argv []string) []string {
	if len(argv) > maxForegroundArgs {
		argv = argv[:maxForegroundArgs]
	}
	out := make([]string, len(argv))
	for i, arg := range argv {
		if len(arg) > maxForegroundArgLen {
			arg = arg[:maxForegroundArgLen]
		}
		out[i] = arg
	}
	return out
}
