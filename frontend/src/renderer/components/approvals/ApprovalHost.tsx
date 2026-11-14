import { QueryClientContext, useQueryClient } from "@tanstack/react-query";
import { useRouterState } from "@tanstack/react-router";
import { useContext, useState } from "react";
import { useWorkspaceQuery } from "../../hooks/useWorkspaceQuery";
import { decideApproval, usePendingApprovals, type ApprovalDecision, type PendingApproval } from "../../lib/approvals";
import { useNavigateToSession } from "../../lib/navigate-to-session";
import { ApprovalPrompt } from "./ApprovalPrompt";

function useDecide(request: PendingApproval | undefined) {
	const queryClient = useQueryClient();
	const [busyId, setBusyId] = useState<string>();
	const [error, setError] = useState<{ id: string; message: string }>();
	const decide = async (decision: ApprovalDecision) => {
		if (!request || busyId) return;
		setBusyId(request.id);
		setError(undefined);
		try {
			await decideApproval(request.id, decision, queryClient);
		} catch (reason) {
			setError({ id: request.id, message: reason instanceof Error ? reason.message : String(reason) });
		} finally {
			setBusyId(undefined);
		}
	};
	return {
		decide,
		busy: Boolean(request && busyId === request.id),
		error: request && error?.id === request.id ? error.message : undefined,
	};
}

/**
 * The session's own approval prompt, under its topbar so it shows over chat
 * and terminal sessions alike. Requests are answered oldest first.
 */
export function SessionApprovalDock({ sessionId }: { sessionId: string }) {
	const pending = usePendingApprovals();
	const mine = (pending.data ?? []).filter((request) => request.sessionId === sessionId);
	const request = mine[0];
	const { decide, busy, error } = useDecide(request);
	if (!request) return null;
	return (
		<div className="relative z-chrome shrink-0 px-3 pt-2" data-testid="session-approval-dock">
			<div className="animate-in fade-in-0 slide-in-from-top-1 duration-200 rounded-[var(--radius-chat-composer)] border border-border-strong bg-surface">
				<ApprovalPrompt key={request.id} request={request} onDecide={decide} busy={busy} error={error} more={mine.length - 1} />
			</div>
		</div>
	);
}

/** The local session the window is showing, if any. */
function useRoutedLocalSessionId(): string | undefined {
	return useRouterState({
		select: (state) => {
			for (const match of [...state.matches].reverse()) {
				const params = match.params as { sessionId?: string; hostId?: string };
				if (params.sessionId) return params.hostId ? undefined : params.sessionId;
			}
			return undefined;
		},
	});
}

/**
 * Approval requests from sessions the user is not looking at, so one is never
 * left to time out unseen. It names the session and can open it.
 */
export function GlobalApprovalCard() {
	// Shell hosts without a query client (some test harnesses) have nothing to show.
	return useContext(QueryClientContext) ? <GlobalApprovalCardContent /> : null;
}

function GlobalApprovalCardContent() {
	// The shell's card holds the one live stream every approval reader shares.
	const pending = usePendingApprovals({ live: true });
	const routed = useRoutedLocalSessionId();
	const workspaces = useWorkspaceQuery();
	const navigateToSession = useNavigateToSession();
	const elsewhere = (pending.data ?? []).filter((request) => request.sessionId !== routed);
	const request = elsewhere[0];
	const { decide, busy, error } = useDecide(request);
	if (!request) return null;
	let projectId: string | undefined;
	let title: string | undefined;
	for (const workspace of workspaces.data ?? []) {
		const session = workspace.sessions.find((candidate) => candidate.id === request.sessionId);
		if (session) {
			projectId = workspace.id;
			title = session.title;
			break;
		}
	}
	return (
		<div
			className="fixed bottom-4 right-4 z-overlay w-[min(28rem,calc(100vw-2rem))] rounded-lg border border-(--color-border-overlay) bg-popover text-popover-foreground shadow-(--shadow-popover)"
			data-testid="global-approval-card"
		>
			<ApprovalPrompt
				key={request.id}
				request={request}
				onDecide={decide}
				busy={busy}
				error={error}
				sessionLabel={title || request.sessionId}
				onOpenSession={() => navigateToSession(projectId, request.sessionId)}
				more={elsewhere.length - 1}
			/>
		</div>
	);
}
