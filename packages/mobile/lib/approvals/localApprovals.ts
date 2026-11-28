import { ApiError, apiRequest } from "../api";
import type { ServerConfig } from "../config";
import { localApprovalCard, type ApprovalCard, type ApprovalDecision, type DecisionResult, type LocalApproval } from "./types";

/** Often enough that a card decided elsewhere leaves within 2 s. */
export const APPROVAL_POLL_MS = 1_500;

/** One session's approvals waiting on this machine, oldest first. */
export async function listApprovals(cfg: ServerConfig, sessionId: string, init?: Pick<RequestInit, "signal">): Promise<ApprovalCard[]> {
	const res = await apiRequest(cfg, "/api/v1/approvals", init);
	const body = await res.json() as { pending?: LocalApproval[] };
	return (body.pending ?? []).filter((a) => a.sessionId === sessionId).map(localApprovalCard);
}

export async function decideApproval(cfg: ServerConfig, id: string, decision: ApprovalDecision): Promise<DecisionResult> {
	try {
		await apiRequest(cfg, `/api/v1/approvals/${encodeURIComponent(id)}/decision`, {
			method: "POST",
			headers: { "Content-Type": "application/json" },
			body: JSON.stringify({ decision }),
		});
		return "decided";
	} catch (cause) {
		// Answered elsewhere, timed out, or the agent stopped waiting.
		if (cause instanceof ApiError && (cause.status === 409 || cause.status === 404)) return "already-decided";
		throw cause;
	}
}

/**
 * Calls onChange with the session's approvals now and whenever they change,
 * until the returned stop. A failed read keeps the last list.
 */
export function subscribeApprovals(cfg: ServerConfig, sessionId: string, onChange: (cards: ApprovalCard[]) => void): () => void {
	let stopped = false;
	let last: string | undefined;
	const controller = new AbortController();
	const read = async () => {
		try {
			const cards = await listApprovals(cfg, sessionId, { signal: controller.signal });
			const key = JSON.stringify(cards);
			if (!stopped && key !== last) {
				last = key;
				onChange(cards);
			}
		} catch {
			// Keep what is shown; the next read retries.
		}
	};
	void read();
	const timer = setInterval(() => void read(), APPROVAL_POLL_MS);
	return () => {
		stopped = true;
		clearInterval(timer);
		controller.abort();
	};
}
