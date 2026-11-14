import { useFocusEffect } from "expo-router";
import { useCallback, useRef, useState } from "react";
import { AppState } from "react-native";
import { shouldPoll } from "../appStatePoll";
import { userFacingError } from "../connectionError";
import type { WorkspaceManifest } from "./api";
import type { FilesBackend } from "./backend";

/**
 * The last manifest any Files screen read, so a sheet over it (the changes
 * picker) can count the worktree's scopes without reading a manifest of its
 * own; on a big worktree that is megabytes. One slot, not one per session:
 * only the focused screen polls, so the newest read is the sheet's screen's,
 * and holding every session's would keep megabytes per session for the app's life.
 */
let lastManifest: { key: string; manifest: WorkspaceManifest } | undefined;

export function cachedManifest(backend: Pick<FilesBackend, "key"> | null | undefined, sessionId: string): WorkspaceManifest | null {
	return backend && lastManifest?.key === `${backend.key}|${sessionId}` ? lastManifest.manifest : null;
}

export function rememberManifest(backend: Pick<FilesBackend, "key">, sessionId: string, manifest: WorkspaceManifest): void {
	lastManifest = { key: `${backend.key}|${sessionId}`, manifest };
}

/** How often an open Files screen asks whether the agent changed anything. */
export const WORKSPACE_POLL_MS = 5_000;

export type ManifestState = {
	manifest: WorkspaceManifest | null;
	loading: boolean;
	error?: string;
	refreshing: boolean;
	refresh: () => Promise<void>;
};

/**
 * The session's changed-file manifest, kept fresh while the screen is focused
 * and the app is in front. A poll whose `workspaceVersion` matches what is
 * already shown keeps the old object, so an idle session re-renders nothing.
 */
export function useWorkspaceManifest(backend: FilesBackend | null, sessionId: string, enabled = true): ManifestState {
	const [manifest, setManifest] = useState<WorkspaceManifest | null>(null);
	const [loading, setLoading] = useState(true);
	const [refreshing, setRefreshing] = useState(false);
	const [error, setError] = useState<string>();
	const current = useRef({ backend, sessionId });
	current.current = { backend, sessionId };
	// Polls, focus reloads and pulls overlap; an answer older than the last one
	// applied must not put older files on screen or in the sheet's cache.
	const issued = useRef(0);
	const applied = useRef(0);

	const load = useCallback(async () => {
		if (!backend || !enabled) return;
		const ticket = ++issued.current;
		try {
			const next = await backend.manifest(sessionId);
			if (ticket < applied.current || current.current.backend !== backend || current.current.sessionId !== sessionId) return;
			applied.current = ticket;
			rememberManifest(backend, sessionId, next);
			setManifest((previous) => previous && previous.workspaceVersion && previous.workspaceVersion === next.workspaceVersion ? previous : next);
			setError(undefined);
		} catch (cause) {
			if (ticket >= applied.current && current.current.backend === backend && current.current.sessionId === sessionId) setError(userFacingError(cause, "Couldn't load this session's files."));
		} finally {
			if (current.current.backend === backend && current.current.sessionId === sessionId) setLoading(false);
		}
	}, [backend, enabled, sessionId]);

	useFocusEffect(useCallback(() => {
		void load();
		const timer = setInterval(() => {
			if (shouldPoll(AppState.currentState)) void load();
		}, WORKSPACE_POLL_MS);
		return () => clearInterval(timer);
	}, [load]));

	const refresh = useCallback(async () => {
		setRefreshing(true);
		try { await load(); } finally { setRefreshing(false); }
	}, [load]);

	return { manifest, loading: loading && !manifest, error, refreshing, refresh };
}
