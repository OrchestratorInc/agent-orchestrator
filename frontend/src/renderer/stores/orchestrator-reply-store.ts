import { useEffect, useRef } from "react";
import { create } from "zustand";
import { turnStanding } from "../components/chat/background-turns";
import { indexWorkerUpdates } from "../components/chat/WorkerUpdates";
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

const checks = new Map<string, Promise<boolean>>();

function settledTurnsStandOut(id: string, sessionId: string, hostId: string | undefined, since: number): Promise<boolean> {
	let check = checks.get(id);
	if (!check) {
		check = fetchRecentConversation(sessionId, hostId).then((snapshot) => {
			const index = indexWorkerUpdates(snapshot.items);
			const settled = snapshot.turns.filter((turn) => Date.parse(turn.completedAt ?? "") >= since);
			return settled.length === 0 || snapshot.turns.some((turn) => turn.state === "running") || settled.some((turn) => {
				if (turn.hasHumanPrompt) return true;
				const standing = turnStanding(snapshot.items.filter((item) => item.turnId === turn.id), turn.state, index);
				return standing !== "background" && standing !== "replaced";
			});
		}, () => true);
		checks.set(id, check);
		void check.finally(() => checks.delete(id));
	}
	return check;
}

export type OrchestratorReplyState = "working" | "unread" | null;

/**
 * What a project's orchestrator buttons should signal: a reply the user has not
 * seen, or a turn of theirs in progress. A reply is the orchestrator's activity
 * settling from working back to idle or waiting for input, observed live from
 * the streamed workspace, when the orchestrator waits for the user or the turns
 * that settled include one the user started or one the chat keeps in view.
 * Worker traffic the orchestrator handled on its own stays quiet. Resets to idle on restore or resume never count, and
 * tool calls inside a turn keep it working. Viewing the orchestrator marks its
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
	useEffect(() => {
		const prev = previous.current;
		const parsed = Date.parse(lastActivityAt ?? "");
		const at = Number.isFinite(parsed) ? parsed : Date.now();
		previous.current = { id: orchestrator?.id, working, since: prev?.working && prev.id === orchestrator?.id ? prev.since : at };
		if (!key || !orchestrator || prev?.id !== orchestrator.id || !prev.working) return;
		if (activityState !== "idle" && activityState !== "waiting_input") return;
		const store = useOrchestratorReplyStore.getState();
		if (!quiet || viewing || activityState === "waiting_input" || lastUserAt > (store.replyAt[key] ?? 0)) {
			store.recordReply(key, at);
			return;
		}
		void settledTurnsStandOut(`${key}:${at}`, orchestrator.id, hostId, prev.since).then((standsOut) => {
			if (standsOut) useOrchestratorReplyStore.getState().recordReply(key, at);
		});
	}, [activityState, hostId, key, lastActivityAt, lastUserAt, orchestrator, quiet, viewing, working]);
	useEffect(() => {
		if (viewing && key && replyAt > seenAt) useOrchestratorReplyStore.getState().markSeen(key, replyAt);
	}, [key, replyAt, seenAt, viewing]);
	if (!orchestrator || viewing) return null;
	if (working && (!quiet || lastUserAt > replyAt)) return "working";
	return replyAt > seenAt ? "unread" : null;
}
