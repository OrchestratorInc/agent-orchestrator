import { useFocusEffect } from "expo-router";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { AppState } from "react-native";
import { shouldPoll } from "../appStatePoll";
import { ApiError } from "../api";
import { userFacingError } from "../connectionError";
import type { WorkspaceBranches, WorkspaceCommitSelection, WorkspaceHistory, WorkspaceManifest } from "./api";
import type { FilesBackend } from "./backend";
import { sumFiles, type ChangeGroup } from "./model";
import { branchId, normalizeSelection, selectionFromParams, selectionGroups, selectionParams, viewOf, type FilesBranch, type FilesSelection, type SelectionParams } from "./selection";

/**
 * How often another branch or a pull request is read again. Nothing reports
 * when one moves, as the worktree's own changes are reported, so the desktop
 * polls it on this beat too.
 */
export const VIEW_POLL_MS = 30_000;

/**
 * The same backend object while its key is the same. The app rebuilds the
 * backend when its connection state refreshes, and a read keyed on the object
 * would start over each time, never finishing on a big worktree.
 */
export function useKeyedBackend(backend: FilesBackend | null): FilesBackend | null {
	// eslint-disable-next-line react-hooks/exhaustive-deps
	return useMemo(() => backend, [backend?.key]);
}

/** The backend reading `branch`: the worktree's own, or the same machine's view of another branch. */
export function useBranchBackend(backend: FilesBackend | null, branch: FilesBranch): FilesBackend | null {
	const keyed = useKeyedBackend(backend);
	const id = branchId(branch);
	// Keyed on the id, so a pick that names the same branch keeps the same backend.
	// eslint-disable-next-line react-hooks/exhaustive-deps
	return useMemo(() => !keyed || branch.kind === "checked_out" ? keyed : keyed.git?.at(viewOf(branch)) ?? null, [keyed, id]);
}

export type ChangeList = {
	/** Undefined while the pick's files are still loading. */
	groups: ChangeGroup[] | undefined;
	summary: { files: number; additions: number; deletions: number } | undefined;
	error?: string;
	refresh: () => Promise<void>;
};

/**
 * The changed files the pick lists. The worktree's scopes come from the
 * manifest the screen already polls; one commit needs the branch's history,
 * and every commit of another branch or a pull request needs its range.
 * `branchBackend` is useBranchBackend's for the pick's branch.
 */
export function useChangeList(branchBackend: FilesBackend | null, sessionId: string, selection: FilesSelection, manifest: WorkspaceManifest | null, enabled = true): ChangeList {
	const pick = normalizeSelection(selection);
	const sha = pick.changes.kind === "commits" ? pick.changes.sha : undefined;
	const viewing = pick.branch.kind !== "checked_out";
	const needsHistory = Boolean(sha);
	const needsRange = viewing && !sha;
	const key = `${branchBackend?.key ?? ""}|${sessionId}|${needsHistory ? `history:${sha}` : needsRange ? "range" : "manifest"}`;
	const [loaded, setLoaded] = useState<{ key: string; history?: WorkspaceHistory; range?: WorkspaceCommitSelection; error?: string }>();
	// The committed pick's key, set in an effect so a render React discards never
	// moves it; and a sequence, so only the newest read for that key lands.
	const current = useRef(key);
	const latest = useRef(0);
	useEffect(() => {
		current.current = key;
	}, [key]);

	const load = useCallback(async () => {
		const git = branchBackend?.git;
		if (!enabled || !git || (!needsHistory && !needsRange)) return;
		const ticket = ++latest.current;
		const landed = () => current.current === key && ticket === latest.current;
		try {
			const next = needsHistory ? { history: await git.history(sessionId) } : { range: await git.rangeFiles(sessionId) };
			if (landed()) setLoaded({ key, ...next });
		} catch (cause) {
			if (!landed()) return;
			const error = userFacingError(cause, needsHistory ? "Couldn't read this commit." : "Couldn't read this branch's changes.");
			// A failed refresh keeps what was already shown, and says it failed.
			setLoaded((previous) => previous?.key === key && (previous.history || previous.range) ? { ...previous, error } : { key, error });
		}
	}, [branchBackend, enabled, key, needsHistory, needsRange, sessionId]);

	// The worktree's history moves with its version; a view's on a timer.
	const version = !viewing ? manifest?.workspaceVersion ?? "" : "";
	useFocusEffect(useCallback(() => {
		void load();
		if (!viewing) return;
		const timer = setInterval(() => {
			if (shouldPoll(AppState.currentState)) void load();
		}, VIEW_POLL_MS);
		return () => clearInterval(timer);
		// `version` reloads the worktree's history when the agent commits.
		// eslint-disable-next-line react-hooks/exhaustive-deps
	}, [load, viewing, version]));

	const shown = loaded?.key === key ? loaded : undefined;
	// The history lists only the newest commits; a pick that has left it is not an empty commit.
	const missingCommit = Boolean(sha && shown?.history && !shown.history.commits.some((commit) => commit.sha === sha));
	const groups = useMemo(() => missingCommit ? undefined : selectionGroups(pick, manifest, shown?.history, shown?.range?.files), [manifest, missingCommit, pick, shown]);
	const summary = useMemo(() => {
		if (!groups) return undefined;
		if (pick.branch.kind === "checked_out" && pick.changes.kind === "all") return manifest?.summary;
		if (shown?.range && !sha) return shown.range.summary;
		return sumFiles(groups.flatMap((group) => group.files));
	}, [groups, manifest, pick, sha, shown]);
	const error = missingCommit ? "This commit isn't among the branch's listed commits any more. Pick it again from Changes." : shown?.error;
	return { groups, summary, error, refresh: load };
}

/**
 * Whether this machine's daemon can compare branches and split the worktree:
 * a newer daemon always lists `sections.uncommitted`, even in a degraded
 * manifest, so the first manifest that has it settles it for the backend. One
 * slow, degraded poll then never takes the pickers (and the user's pick) away.
 */
export function useGitCapable(backend: FilesBackend | null, manifest: WorkspaceManifest | null): boolean {
	const key = backend?.git ? backend.key : "";
	const now = Boolean(key && manifest && manifest.sections.uncommitted !== undefined);
	const [seen, setSeen] = useState<string>();
	useEffect(() => {
		if (now) setSeen(key);
	}, [key, now]);
	return now || (Boolean(key) && seen === key);
}

/** An older daemon has no branches route; that is the only failure that keeps the pickers off for good. */
function routeMissing(cause: unknown): boolean {
	return cause instanceof ApiError && (cause.status === 404 || cause.status === 405);
}

/**
 * The session's branch list, read when Files opens: whether it has Git history
 * to compare at all (a scratch session has none), and the checked-out branch's
 * name. `list` is undefined until it answers; `reload` reads it again after a
 * failure (a pull to refresh), unless the daemon has no such route.
 */
export function useBranchList(backend: FilesBackend | null, sessionId: string, enabled: boolean): { list: WorkspaceBranches | undefined; reload: () => void } {
	const [state, setState] = useState<{ key: string; value?: WorkspaceBranches; missing?: boolean }>();
	const [attempt, setAttempt] = useState(0);
	const keyed = useKeyedBackend(backend);
	const git = keyed?.git;
	const key = `${keyed?.key ?? ""}|${sessionId}|${enabled}`;
	const shown = state?.key === key ? state : undefined;
	useEffect(() => {
		if (!git || !enabled) return;
		const controller = new AbortController();
		git.branches(sessionId, controller.signal)
			.then((value) => { if (!controller.signal.aborted) setState({ key, value }); })
			.catch((cause) => { if (!controller.signal.aborted && routeMissing(cause)) setState({ key, missing: true }); });
		return () => controller.abort();
	}, [attempt, enabled, git, key, sessionId]);
	const retry = !shown?.value && !shown?.missing;
	const reload = useCallback(() => { if (retry) setAttempt((count) => count + 1); }, [retry]);
	return { list: shown?.value, reload };
}

/** The pick a Files route carries in its params, the same object while it names the same pick. */
export function useRouteSelection(params: Partial<Record<keyof SelectionParams, string | string[]>>): FilesSelection {
	const parsed = selectionFromParams(params);
	const key = JSON.stringify(selectionParams(parsed));
	// eslint-disable-next-line react-hooks/exhaustive-deps
	return useMemo(() => parsed, [key]);
}
