import { useFocusEffect } from "expo-router";
import { useCallback, useEffect, useRef, useState } from "react";
import type { ServerConfig } from "../config";
import { getWorkspaceManifest, type WorkspaceManifest } from "./api";

export type ChangeSummary = WorkspaceManifest["summary"];

/**
 * The session's changed-file totals from git, for the composer's changes pill.
 * No timer: it asks when the screen gains focus (coming back from Files, say)
 * and whenever `signal` changes — the caller passes something that moves when a
 * turn reports edits — so an idle conversation costs nothing.
 */
export function useChangeSummary(config: ServerConfig | null, sessionId: string, signal: string): ChangeSummary | null {
	const [summary, setSummary] = useState<ChangeSummary | null>(null);
	const latest = useRef(0);

	const load = useCallback(() => {
		if (!config || !sessionId) return;
		const ticket = ++latest.current;
		getWorkspaceManifest(config, sessionId)
			.then((manifest) => {
				if (ticket !== latest.current) return;
				setSummary((previous) => previous
					&& previous.files === manifest.summary.files
					&& previous.additions === manifest.summary.additions
					&& previous.deletions === manifest.summary.deletions ? previous : manifest.summary);
			})
			// A failed read keeps the last answer: the pill is a shortcut, not a status.
			.catch(() => {});
	}, [config, sessionId]);

	useEffect(() => { setSummary(null); }, [config, sessionId]);
	useEffect(() => { load(); }, [load, signal]);
	useFocusEffect(useCallback(() => { load(); }, [load]));
	return summary;
}
