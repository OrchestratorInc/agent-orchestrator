import { useFocusEffect } from "expo-router";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { AppState } from "react-native";
import { shouldPoll } from "../appStatePoll";
import type { WorkspaceManifest } from "./api";
import type { WorkspaceSource } from "./workspaceSource";

export type ChangeSummary = WorkspaceManifest["summary"];

/**
 * The session's changed-file totals from git, for the composer's changes pill.
 * No timer: it asks when the screen gains focus (coming back from Files, say)
 * and whenever `signal` changes — the caller passes something that moves when a
 * turn reports edits — so an idle conversation costs nothing.
 */
export function useChangeSummary(source: WorkspaceSource | null, sessionId: string, signal: string): ChangeSummary | null {
	const owner = useMemo(() => ({ source, sessionId }), [source, sessionId]);
	const current = useRef(owner); current.current = owner;
	const focused = useRef(false);
	const pending = useRef<AbortController | null>(null);
	const [state, setState] = useState<{ owner: typeof owner; summary: ChangeSummary }>();
	const cancel = useCallback(() => { pending.current?.abort(); pending.current = null; }, []);

	const load = useCallback(() => {
		if (!source?.isCurrent() || !sessionId || !focused.current || !shouldPoll(AppState.currentState)) return;
		cancel();
		const controller = new AbortController(); pending.current = controller;
		source.getManifest(sessionId, { signal: controller.signal })
			.then((manifest) => {
				if (controller.signal.aborted || current.current !== owner || !source.isCurrent() || !focused.current) return;
				setState((before) => {
					const previous = before?.owner === owner ? before.summary : undefined;
					if (previous
					&& previous.files === manifest.summary.files
					&& previous.additions === manifest.summary.additions
					&& previous.deletions === manifest.summary.deletions) return before;
					return { owner, summary: manifest.summary };
				});
			})
			// A failed read keeps the last answer: the pill is a shortcut, not a status.
			.catch(() => {});
	}, [cancel, owner, source, sessionId]);

	useEffect(() => { load(); }, [load, signal]);
	useFocusEffect(useCallback(() => {
		focused.current = true; load();
		const subscription = AppState.addEventListener("change", (state) => { if (shouldPoll(state)) load(); else cancel(); });
		return () => { focused.current = false; subscription.remove(); cancel(); };
	}, [cancel, load]));
	return state?.owner === owner && source?.isCurrent() ? state.summary : null;
}
