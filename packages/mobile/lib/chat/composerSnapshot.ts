import { useRef } from "react";
import { queuedConversationMessages } from "./timelineModel";
import type { ConversationSnapshot } from "./types";

/**
 * Everything the composer reads from a snapshot (ChatComposer, its settings
 * control and effort hook): whether a turn runs, the queued prompts, usage,
 * rate limits, capabilities, the controller, the turn settings and a model
 * reroute (which changes the model the composer names). Not the
 * transcript itself, which changes with every streamed word.
 */
export function composerSnapshotKey(snapshot: ConversationSnapshot, openingTurnId?: string): string {
	return JSON.stringify([
		snapshot.controller,
		snapshot.usage,
		snapshot.rateLimits,
		snapshot.capabilities,
		snapshot.settings,
		snapshot.modelReroute,
		snapshot.harness,
		snapshot.mode,
		snapshot.turns.map((turn) => [turn.id, turn.state]),
		queuedConversationMessages(snapshot, openingTurnId).map(({ turnId, message }) => [turnId, message.id, message.revision]),
	]);
}

/**
 * The snapshot to hand the composer: the one it already has while nothing it
 * reads has changed. A streaming reply changes the snapshot several times a
 * second, and the composer (the heaviest view on the chat screen after the
 * transcript) re-rendered on each one although nothing in it moved.
 */
export function useComposerSnapshot(snapshot: ConversationSnapshot | undefined, openingTurnId?: string): ConversationSnapshot | undefined {
	const kept = useRef<{ key: string; snapshot: ConversationSnapshot } | undefined>(undefined);
	if (!snapshot) return undefined;
	const key = composerSnapshotKey(snapshot, openingTurnId);
	if (kept.current?.key !== key) kept.current = { key, snapshot };
	return kept.current.snapshot;
}
