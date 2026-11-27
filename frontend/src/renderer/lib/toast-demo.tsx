import type { QueryClient } from "@tanstack/react-query";
import { workspaceQueryKey } from "../hooks/useWorkspaceQuery";
import type { WorkspaceSession, WorkspaceSummary } from "../types/workspace";
import { showNotificationToast } from "./notification-toast";

/**
 * Dev only: `__toastDemo()` announces one notification of every kind the
 * daemon sends, for real sessions on this machine, worded as the daemon's
 * enricher words them (backend/internal/notify/enrich.go) and shown through the
 * same toast as live ones. They stay until dismissed; a live one leaves after
 * 8 seconds.
 */
export function toastDemo(queryClient: QueryClient) {
	const sessions = (queryClient.getQueryData<WorkspaceSummary[]>(workspaceQueryKey) ?? [])
		.flatMap((workspace) => workspace.sessions)
		.filter((session) => !session.isTerminated);
	// Different agents first, so the tiles show the range.
	const byAgent = [...new Map(sessions.map((session) => [session.provider, session])).values()];
	const picks = [...byAgent, ...sessions.filter((session) => !byAgent.includes(session))];
	const at = (index: number): WorkspaceSession | undefined => picks[index % Math.max(picks.length, 1)];

	const pr = (session: WorkspaceSession | undefined, fallback: number) => {
		const facts = session?.prs[0];
		return { label: `PR #${facts?.number ?? fallback}`, title: session?.title ?? "Fix checkout totals", url: facts?.url };
	};

	const kinds: Array<{ type: string; title: (s?: WorkspaceSession) => string; body: (s?: WorkspaceSession) => string; fallback: number }> = [
		{ type: "needs_input", fallback: 0, title: (s) => `${s?.title ?? "session"} needs your input`, body: () => "Your agent is waiting on you to continue." },
		{ type: "review_changes_requested", fallback: 9157, title: (s) => `Review found changes for ${pr(s, 9157).label}`, body: () => "Orchestrator finished reviewing this pull request and found changes to address." },
		{ type: "ready_to_merge", fallback: 9152, title: (s) => `${pr(s, 9152).title} · ${pr(s, 9152).label}`, body: (s) => `PR from session ${s?.title ?? "session"} is ready to merge. CI passed with no blocking review feedback.` },
		{ type: "review_completed", fallback: 9129, title: (s) => `Review complete for ${pr(s, 9129).label}`, body: () => "Orchestrator finished reviewing this pull request and approved it." },
		{ type: "pr_closed_unmerged", fallback: 8829, title: (s) => `${pr(s, 8829).label} closed`, body: (s) => `${pr(s, 8829).title} was closed without merging. Reopen it if this wasn't intended.` },
		{ type: "pr_merged", fallback: 9162, title: (s) => `${pr(s, 9162).label} merged`, body: (s) => `${pr(s, 9162).title} is now on main.` },
	];

	kinds.forEach((kind, index) => {
		const session = at(index);
		window.setTimeout(() => {
			const url = pr(session, kind.fallback).url;
			showNotificationToast(
				{ id: `demo-${kind.type}-${Date.now()}`, type: kind.type, title: kind.title(session), body: kind.body(session), provider: session?.provider },
				() => (url && kind.type !== "needs_input" ? window.open(url, "_blank", "noopener,noreferrer") : undefined),
				0,
			);
		}, index * 300);
	});
}
