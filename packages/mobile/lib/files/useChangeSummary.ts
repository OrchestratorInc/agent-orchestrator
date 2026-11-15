import { useFocusEffect } from "expo-router";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { ServerConfig } from "../config";
import type { WorkspaceManifest } from "./api";
import { daemonFilesBackend, type FilesBackend } from "./backend";

export type ChangeSummary = WorkspaceManifest["summary"];

/** A machine session's totals, read from its daemon. */
export function useChangeSummary(config: ServerConfig | null, sessionId: string, signal: string): ChangeSummary | null {
	const backend = useMemo(() => config ? daemonFilesBackend(config) : null, [config]);
	return useBackendChangeSummary(backend, sessionId, signal);
}

/**
 * The session's changed-file totals from git, for the composer's changes pill,
 * from whichever host holds the session (a machine's daemon or Cloud).
 * No timer: it asks when the screen gains focus (coming back from Files, say)
 * and whenever `signal` changes — the caller passes something that moves when a
 * turn reports edits — so an idle conversation costs nothing.
 */
export function useBackendChangeSummary(backend: FilesBackend | null, sessionId: string, signal: string): ChangeSummary | null {
	const [summary, setSummary] = useState<ChangeSummary | null>(null);
	const latest = useRef(0);

	const load = useCallback(() => {
		if (!backend || !sessionId) return;
		const ticket = ++latest.current;
		backend.manifest(sessionId)
			.then((manifest) => {
				if (ticket !== latest.current) return;
				setSummary((previous) => previous
					&& previous.files === manifest.summary.files
					&& previous.additions === manifest.summary.additions
					&& previous.deletions === manifest.summary.deletions ? previous : manifest.summary);
			})
			// A failed read keeps the last answer: the pill is a shortcut, not a status.
			.catch(() => {});
	}, [backend, sessionId]);

	useEffect(() => { setSummary(null); }, [backend, sessionId]);
	useEffect(() => { load(); }, [load, signal]);
	useFocusEffect(useCallback(() => { load(); }, [load]));
	return summary;
}
