import { useEffect, useRef } from "react";
import { create } from "zustand";
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
	const working = activityState === "active";
	const previous = useRef<{ id?: string; working: boolean } | null>(null);
	useEffect(() => {
		const prev = previous.current;
		previous.current = { id: orchestrator?.id, working };
		if (!key || !orchestrator || prev?.id !== orchestrator.id || !prev.working) return;
		if (activityState !== "idle" && activityState !== "waiting_input") return;
		const at = Date.parse(lastActivityAt ?? "");
		useOrchestratorReplyStore.getState().recordReply(key, Number.isFinite(at) ? at : Date.now());
	}, [activityState, key, lastActivityAt, orchestrator, working]);
	useEffect(() => {
		if (viewing && key && replyAt > seenAt) useOrchestratorReplyStore.getState().markSeen(key, replyAt);
	}, [key, replyAt, seenAt, viewing]);
	if (!orchestrator || viewing) return null;
	if (working) return "working";
	return replyAt > seenAt ? "unread" : null;
}
