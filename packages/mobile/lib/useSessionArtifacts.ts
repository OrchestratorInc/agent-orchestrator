import { useCallback, useEffect, useRef, useState } from "react";
import { getSession } from "./api";
import type { SessionArtifact } from "./artifacts";
import { userFacingError } from "./connectionError";
import { useApp } from "./store";

type Fetched = { sessionId: string; artifacts: SessionArtifact[] };

/**
 * One session's artifacts, newest first. The board's poll keeps a listed
 * session's row current; the session is also read directly, for one the board
 * does not list (an orchestrator) and so a pull-to-refresh answers at once.
 * A deleted file leaves the list immediately rather than at the next poll.
 */
export function useSessionArtifacts(sessionId: string) {
	const { sessions, config, refresh } = useApp();
	const listed = sessions.find((session) => session.id === sessionId)?.artifacts;
	const [fetched, setFetched] = useState<Fetched | null>(null);
	const [loading, setLoading] = useState(true);
	const [error, setError] = useState<string>();
	// Paths deleted from here, hidden until the list they were read from is
	// replaced by a newer one (which no longer has them).
	const [removed, setRemoved] = useState<{ base: SessionArtifact[] | undefined; paths: string[] } | null>(null);
	const current = useRef({ config, sessionId });
	current.current = { config, sessionId };

	const load = useCallback(async () => {
		if (!config || !sessionId) return;
		setError(undefined);
		try {
			const session = await getSession(config, sessionId);
			if (current.current.config === config && current.current.sessionId === sessionId) setFetched({ sessionId, artifacts: session.artifacts ?? [] });
		} catch (cause) {
			if (current.current.config === config && current.current.sessionId === sessionId) setError(userFacingError(cause));
		} finally {
			if (current.current.config === config && current.current.sessionId === sessionId) setLoading(false);
		}
	}, [config, sessionId]);

	useEffect(() => {
		setLoading(true);
		void load();
	}, [load]);

	const reload = useCallback(async () => {
		await Promise.all([load(), refresh().catch(() => {})]);
	}, [load, refresh]);

	const base = listed ?? (fetched?.sessionId === sessionId ? fetched.artifacts : undefined);
	const hidden = removed && removed.base === base ? removed.paths : [];
	const artifacts = base?.filter((artifact) => !hidden.includes(artifact.path));

	const markDeleted = useCallback((path: string) => {
		setRemoved((previous) => ({ base, paths: [...(previous && previous.base === base ? previous.paths : []), path] }));
	}, [base]);

	return {
		/** Undefined until the first answer. */
		artifacts,
		loading: loading && !artifacts,
		error: artifacts ? undefined : error,
		reload,
		markDeleted,
	};
}
