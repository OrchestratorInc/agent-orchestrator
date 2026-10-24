import { useFocusEffect } from "expo-router";
import { useCallback, useMemo, useRef, useState } from "react";
import { AppState } from "react-native";
import { shouldPoll } from "../appStatePoll";
import { userFacingError } from "../connectionError";
import type { WorkspaceManifest } from "./api";
import type { WorkspaceSource } from "./workspaceSource";

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
 * and the app is in front. An unchanged version and repository catalog keep
 * the old object, so an idle session re-renders nothing.
 */
export function useWorkspaceManifest(source: WorkspaceSource | null, sessionId: string, enabled = true): ManifestState {
	const owner = useMemo(() => ({ source, sessionId, enabled }), [source, sessionId, enabled]);
	const current = useRef(owner); current.current = owner;
	const focused = useRef(false);
	const pending = useRef<{ controller: AbortController; promise: Promise<void> } | null>(null);
	const [state, setState] = useState<{ owner: typeof owner; manifest: WorkspaceManifest | null; loading: boolean; error?: string }>({ owner, manifest: null, loading: true });
	const [refreshState, setRefreshState] = useState<{ owner: typeof owner; token: number; active: boolean }>();
	const refreshToken = useRef(0);

	const cancel = useCallback(() => {
		pending.current?.controller.abort(); pending.current = null;
		refreshToken.current++;
		setRefreshState(undefined);
	}, []);
	const load = useCallback((): Promise<void> => {
		if (!source || !source.isCurrent() || !sessionId || !enabled || !focused.current || !shouldPoll(AppState.currentState)) return Promise.resolve();
		if (pending.current) return pending.current.promise;
		const controller = new AbortController();
		const owns = () => current.current === owner && focused.current && !controller.signal.aborted && source.isCurrent();
		const request = { controller, promise: Promise.resolve() };
		request.promise = (async () => {
			try {
				const next = await source.getManifest(sessionId, { signal: controller.signal });
				if (!owns()) return;
				setState((previous) => {
					const before = previous.owner === owner ? previous.manifest : null;
					// Workers version file changes independently of repository availability.
					const unchanged = before?.workspaceVersion && before.workspaceVersion === next.workspaceVersion
						&& JSON.stringify(before.repositories) === JSON.stringify(next.repositories);
					if (unchanged && !previous.loading && !previous.error) return previous;
					return { owner, manifest: unchanged ? before : next, loading: false };
				});
			} catch (cause) {
				if (owns()) setState((previous) => ({ owner, manifest: previous.owner === owner ? previous.manifest : null, loading: false, error: userFacingError(cause, "Couldn't load this session's files.") }));
			} finally { if (pending.current === request) pending.current = null; }
		})();
		pending.current = request;
		return request.promise;
	}, [owner, source, enabled, sessionId]);

	useFocusEffect(useCallback(() => {
		focused.current = true;
		void load();
		const timer = setInterval(() => {
			if (shouldPoll(AppState.currentState)) void load();
		}, WORKSPACE_POLL_MS);
		const subscription = AppState.addEventListener("change", (state) => { if (shouldPoll(state)) void load(); else cancel(); });
		return () => { focused.current = false; clearInterval(timer); subscription.remove(); cancel(); };
	}, [cancel, load]));

	const refresh = useCallback(async () => {
		if (!source?.isCurrent() || current.current !== owner) return;
		const token = ++refreshToken.current;
		setRefreshState({ owner, token, active: true });
		try { await load(); } finally { if (current.current === owner && refreshToken.current === token) setRefreshState(undefined); }
	}, [load, owner, source]);

	const visible = state.owner === owner && source?.isCurrent() && enabled ? state : undefined;
	return { manifest: visible?.manifest ?? null, loading: Boolean(source && enabled && !visible?.manifest && (visible?.loading ?? true)), error: visible?.error, refreshing: refreshState?.owner === owner && refreshState.active, refresh };
}
