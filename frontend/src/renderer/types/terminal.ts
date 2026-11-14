export type TerminalTarget =
	| { kind: "worker" }
	| {
			kind: "reviewer";
			handleId: string;
			harness: string;
			sessionId: string;
			reviewStatus?: "running" | "complete" | "delivered" | "failed" | "cancelled";
	  }
	| {
			generation: string;
			kind: "shell";
			handleId: string;
			sessionId?: string;
			title: string;
	  };

export function terminalTargetBelongsToSession(target: TerminalTarget, sessionId: string | undefined): boolean {
	if (target.kind === "worker") return true;
	return target.sessionId === sessionId;
}
