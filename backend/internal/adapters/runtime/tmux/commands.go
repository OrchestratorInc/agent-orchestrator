package tmux

import "fmt"

func newSessionArgs(id, cwd, shellPath, launchCmd string) []string {
	return []string{
		"new-session", "-d",
		"-s", id,
		"-x", "220",
		"-y", "50",
		"-c", cwd,
		shellPath, "-c", launchCmd,
	}
}

func respawnPaneArgs(id, cwd, shellPath, launchCmd string) []string {
	return []string{
		"respawn-pane", "-k",
		"-t", id,
		"-c", cwd,
		shellPath, "-c", launchCmd,
	}
}

func setStatusOffArgs(id string) []string {
	return []string{"set-option", "-t", id, "status", "off"}
}

func setMouseOnArgs(id string) []string {
	return []string{"set-option", "-t", id, "mouse", "on"}
}

func setWindowSizeLargestArgs(id string) []string {
	return []string{"set-option", "-t", id, "window-size", "largest"}
}

func setDetachOnDestroyOnArgs(id string) []string {
	return []string{"set-option", "-t", exactSessionTarget(id) + ":", "detach-on-destroy", "on"}
}

func showDetachOnDestroyArgs(id string) []string {
	return []string{"show-options", "-t", exactSessionTarget(id) + ":", "-v", "detach-on-destroy"}
}

func panePIDArgs(id string) []string {
	return []string{"display-message", "-p", "-t", id, "#{pane_pid}"}
}

func paneDeadArgs(id string) []string {
	return []string{"list-panes", "-s", "-t", exactSessionTarget(id), "-F", "#{pane_dead}"}
}

func paneCurrentPathArgs(id string) []string {
	return []string{"display-message", "-p", "-t", id, "#{pane_current_path}"}
}

func killSessionArgs(id string) []string {
	return []string{"kill-session", "-t", exactSessionTarget(id)}
}

func hasSessionArgs(id string) []string {
	return []string{"has-session", "-t", exactSessionTarget(id)}
}

func exactSessionTarget(id string) string {
	return "=" + id
}

func listPanePIDsArgs(id string) []string {
	return []string{"list-panes", "-s", "-t", exactSessionTarget(id), "-F", "#{pane_pid}"}
}

func sendKeysLiteralArgs(id, chunk string) []string {
	return []string{"send-keys", "-t", id, "-l", chunk}
}

func sendEnterArgs(id string) []string {
	return []string{"send-keys", "-t", id, "Enter"}
}

func sendInterruptArgs(id string) []string {
	return []string{"send-keys", "-t", id, "C-c"}
}

func capturePaneArgs(id string, lines int) []string {
	return []string{"capture-pane", "-t", id, "-p", "-S", fmt.Sprintf("-%d", lines)}
}

func capturePaneStyledArgs(id string, lines int) []string {
	return []string{"capture-pane", "-e", "-t", id, "-p", "-S", fmt.Sprintf("-%d", lines)}
}
