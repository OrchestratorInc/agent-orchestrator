package commanddetail

import "strings"

func UnwrapShell(command string) string {
	for _, flag := range []string{" -lc ", " -c "} {
		if idx := strings.Index(command, flag); idx > 0 && looksLikeShell(command[:idx]) {
			inner := strings.TrimSpace(command[idx+len(flag):])
			if len(inner) >= 2 && inner[0] == inner[len(inner)-1] && (inner[0] == '\'' || inner[0] == '"') {
				return inner[1 : len(inner)-1]
			}
			return inner
		}
	}
	return command
}

func looksLikeShell(prefix string) bool {
	base := prefix
	if idx := strings.LastIndex(prefix, "/"); idx >= 0 {
		base = prefix[idx+1:]
	}
	switch base {
	case "sh", "bash", "zsh", "dash", "fish":
		return true
	default:
		return false
	}
}
