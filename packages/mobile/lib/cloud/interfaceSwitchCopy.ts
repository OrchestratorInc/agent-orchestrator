import type { SessionInterfaceTransition } from "../chat/api";

// What a cloud session's switch between Chat and Terminal UI says, in the
// words a machine session's Chat and Terminal screens use for theirs
// (ChatSessionScreen's interfacePhaseLabel, TerminalSessionScreen's
// terminalInterfacePhaseLabel), for either direction.

export type InterfaceMode = "chat" | "tui";

export function interfaceModeName(mode: InterfaceMode): string {
	return mode === "tui" ? "Terminal UI" : "Chat";
}

/** The banner's phase, while the screen stays readable under it. */
export function switchPhaseShortLabel(phase?: string): string {
	return phase === "draining" ? "finishing current work" : "preparing native handoff";
}

/** The card's phase, from the moment the source interface starts stopping. */
export function switchPhaseLabel(to: InterfaceMode, phase?: string): string {
	const from = to === "tui" ? "Chat" : "terminal";
	const target = to === "tui" ? "the terminal" : "Chat";
	switch (phase) {
		case "draining": return to === "tui" ? "Waiting for the current turn to finish." : "Waiting for the current terminal turn to finish. New messages are queued safely.";
		case "source_stopping": return `Stopping the ${from} controller before ${target} starts.`;
		case "source_stopped": return `${to === "tui" ? "Chat" : "Terminal"} controller stopped. The worktree and native conversation are unchanged.`;
		case "target_starting": return `Resuming the same native conversation in ${target}.`;
		case "activating": return to === "tui" ? "Opening the Terminal UI." : "Opening the Chat interface.";
		case "completed": return `${to === "tui" ? "Terminal UI" : "Chat"} is ready. Opening it now.`;
		default: return `Checking that ${target} can resume this agent's native conversation.`;
	}
}

/** A failed or blocked switch's notice, until it is dismissed. */
export function switchNotice(transition: SessionInterfaceTransition | undefined): { text: string; recovered: boolean } | undefined {
	if (!transition || transition.noticeAcknowledgedAt) return undefined;
	if (transition.phase !== "failed" && transition.phase !== "recovery_required") return undefined;
	const recovered = transition.phase === "recovery_required" && transition.errorCode === "DAEMON_RESTARTED";
	const text = transition.errorDetail
		|| (recovered ? "Orchestrator restored the session in its last committed interface."
			: transition.phase === "recovery_required" ? "The interface switch needs recovery before more work is sent."
				: "The interface switch failed; the current interface remains available.");
	return { text, recovered };
}
