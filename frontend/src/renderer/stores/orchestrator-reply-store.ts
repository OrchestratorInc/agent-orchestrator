import { useEffect, useRef } from "react";
import { create } from "zustand";
import { turnStanding } from "../components/chat/background-turns";
import { indexWorkerUpdates, placeBatchedReports } from "../components/chat/WorkerUpdates";
import { fetchRecentConversation } from "../hooks/useConversation";
import type { WorkspaceSession } from "../types/workspace";

const STORAGE_KEY = "ao.orchestrator.replies";

/** Per project: when its orchestrator last finished a reply, and up to when the user has seen them. */
type Stored = { replyAt: Record<string, number>; seenAt: Record<string, number> };

function load(): Stored {
	try {
		const saved: unknown = JSON.parse(window.localStorage.getItem(STORAGE_KEY) ?? "null");
		if (saved && typeof saved === "object") {
			const { replyAt, seenAt } = saved as Partial<Stored>;
			if (replyAt && typeof replyAt === "object" && seenAt && typeof seenAt === "object") return { replyAt, seenAt };
		}
	} catch {}
	return { replyAt: {}, seenAt: {} };
}

function persist(state: Stored) {
	try {
		window.localStorage.setItem(STORAGE_KEY, JSON.stringify(state));
	} catch {}
}

type OrchestratorReplyStore = Stored & {
	recordReply: (key: string, at: number) => void;
	markSeen: (key: string, at: number) => void;
};

export const useOrchestratorReplyStore = create<OrchestratorReplyStore>((set, get) => ({
	...load(),
	recordReply: (key, at) => {
		const { replyAt, seenAt } = get();
		// Every mounted button observes the same turn end; record it once.
		if ((replyAt[key] ?? 0) >= at) return;
		const next = { replyAt: { ...replyAt, [key]: at }, seenAt };
		persist(next);
		set(next);
	},
	markSeen: (key, at) => {
		const { replyAt, seenAt } = get();
		if ((seenAt[key] ?? 0) >= at) return;
		const next = { replyAt, seenAt: { ...seenAt, [key]: at } };
		persist(next);
		set(next);
	},
}));

export const orchestratorReplyKey = (projectId: string, hostId?: string) => `${hostId ?? ""}:${projectId}`;

const checks = new Map<string, Promise<boolean | "unsure">>();

function settledTurnsStandOut(id: string, sessionId: string, hostId: string | undefined, since: number, until: number): Promise<boolean | "unsure"> {
	let check = checks.get(id);
	if (!check) {
		check = fetchRecentConversation(sessionId, hostId).then((snapshot) => {
			if (snapshot.hasMoreBefore && !(Date.parse(snapshot.items[0]?.createdAt ?? "") <= since)) return true;
			const index = indexWorkerUpdates(snapshot.items);
			const { under } = placeBatchedReports(snapshot.batchedDeliveries ?? [], snapshot.items);
			const settled = snapshot.turns.filter((turn) => turn.state !== "cancelled" && Date.parse(turn.completedAt ?? "") >= since);
			const ids = new Set(settled.map((turn) => turn.id));
			const drained = (snapshot.batchedDeliveries ?? []).filter((delivery) => ids.has(delivery.deliveredWithTurnId));
			const standsOut = [...settled, ...drained.map((delivery) => ({ id: delivery.turnId, state: "completed", hasHumanPrompt: false }))].some((turn) => {
				if (turn.hasHumanPrompt) return true;
				const items = snapshot.items.filter((item) => item.turnId === turn.id);
				if (items.length === 0 && !ids.has(turn.id)) return false;
				const standing = turnStanding(items, turn.state, index, under);
				return standing !== "background" && standing !== "replaced";
			});
			const started = snapshot.turns.some((turn) => {
				const began = Date.parse(turn.startedAt ?? turn.requestedAt);
				return turn.state === "running" && began >= since && began <= until;
			});
			return standsOut || ((settled.length === 0 || started) && "unsure");
		}).catch(() => true);
		checks.set(id, check);
		void check.finally(() => checks.delete(id));
	}
	return check;
}

export type OrchestratorReplyState = "working" | "unread" | null;

/**
 * What a project's orchestrator buttons should signal: a reply the user has not
 * seen, or a turn in progress. A reply is the orchestrator's activity settling
 * from working back to idle or waiting for input, observed live from the
 * streamed workspace. Resets to idle on restore or resume never count, and tool
 * calls inside a turn keep it working. Viewing the orchestrator marks its
 * replies seen; both facts persist per project across restarts.
 */
export function useOrchestratorReplyState({ projectId, hostId, orchestrator, viewing }: {
	projectId?: string;
	hostId?: string;
	orchestrator?: WorkspaceSession;
	viewing: boolean;
}): OrchestratorReplyState {
	const key = projectId ? orchestratorReplyKey(projectId, hostId) : "";
	const replyAt = useOrchestratorReplyStore((state) => state.replyAt[key] ?? 0);
	const seenAt = useOrchestratorReplyStore((state) => state.seenAt[key] ?? 0);
	const activityState = orchestrator?.activity?.state;
	const lastActivityAt = orchestrator?.activity?.lastActivityAt;
	const quiet = orchestrator?.mode === "chat";
	const lastUserAt = Date.parse(orchestrator?.lastUserMessageAt ?? "") || 0;
	const working = activityState === "active";
	const previous = useRef<{ id?: string; working: boolean; since: number } | null>(null);
	const viewedAt = useRef(0);
	const handledAt = useRef(0);
	useEffect(() => {
		if (viewing) viewedAt.current = Date.now();
	}, [viewing]);
	useEffect(() => {
		const prev = previous.current;
		if (prev?.id !== orchestrator?.id && !working) handledAt.current = lastUserAt;
		const parsed = Date.parse(lastActivityAt ?? "");
		const at = Number.isFinite(parsed) ? parsed : Date.now();
		previous.current = { id: orchestrator?.id, working, since: prev?.working && prev.id === orchestrator?.id ? prev.since : at };
		if (!key || !orchestrator || prev?.id !== orchestrator.id || !prev.working) return;
		if (activityState !== "idle" && activityState !== "waiting_input") return;
		if (!quiet || viewing || activityState === "waiting_input") {
			useOrchestratorReplyStore.getState().recordReply(key, at);
			return;
		}
		const checkedFrom = Date.now();
		void settledTurnsStandOut(`${key}:${at}:${prev.since}`, orchestrator.id, hostId, prev.since, at).then((standsOut) => {
			const now = previous.current;
			if (standsOut === "unsure" && now?.id === orchestrator.id && now.working) {
				now.since = Math.min(now.since, prev.since);
				return;
			}
			if (!standsOut) {
				if (lastUserAt <= prev.since) handledAt.current = Math.max(handledAt.current, lastUserAt);
				return;
			}
			const replies = useOrchestratorReplyStore.getState();
			replies.recordReply(key, at);
			if (viewedAt.current >= checkedFrom) replies.markSeen(key, at);
		});
	}, [activityState, hostId, key, lastActivityAt, lastUserAt, orchestrator, quiet, viewing, working]);
	useEffect(() => {
		if (viewing && key && replyAt > seenAt) useOrchestratorReplyStore.getState().markSeen(key, replyAt);
	}, [key, replyAt, seenAt, viewing]);
	if (!orchestrator || viewing) return null;
	if (working && (!quiet || lastUserAt > Math.max(replyAt, handledAt.current))) return "working";
	return replyAt > seenAt ? "unread" : null;
}
