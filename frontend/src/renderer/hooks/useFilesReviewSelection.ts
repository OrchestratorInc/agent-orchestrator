import { useEffect, useMemo, useRef, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import type { ReviewBranchMenuProps, ReviewBranchRow, ReviewBranchTrigger } from "../components/diffs/ReviewBranchMenu";
import type { ReviewChangeStats, ReviewChangesMenuProps, ReviewChangesScope, ReviewChangesScopeOption, ReviewCommitRow } from "../components/diffs/ReviewChangesMenu";
import {
	ALL_COMMITS,
	DEFAULT_FILES_SELECTION,
	branchKey,
	branchRowId,
	branchTargetOf,
	buildReviewTarget,
	changesKey,
	commitSelectionRequest,
	findBranchRow,
	isLegacyManifest,
	normalizeChanges,
	scopeChanges,
	scopeFiles,
	sumChanges,
	ticksIncludingBetween,
	toggleCommit,
} from "../lib/files-review-selection";
import { sessionUiKey } from "../lib/hosts";
import type { ProjectKind } from "../types/workspace";
import { useUiStore } from "../stores/ui-store";
import { useLatestCallback } from "./useStable";
import {
	invalidateWorkspaceViews,
	sessionWorkspaceBranchesQueryOptions,
	sessionWorkspaceCommitSelectionQueryOptions,
	sessionWorkspaceHistoryQueryOptions,
	workspaceErrorCode,
	WorkspaceRequestError,
	type FilesBranchTarget,
	type FilesChanges,
	type FilesSelection,
	type ReviewTarget,
	type WorkspaceFilesResponse,
} from "./useSessionWorkspaceFiles";

/** Ticks reach the queries this long after the last one, so a run of ticks sends one request. */
const TICK_SETTLE_MS = 250;
/** How often a viewed branch or PR is read again; nothing watches them as it does the worktree. */
const OTHER_BRANCH_REFRESH_MS = 30_000;
const LEGACY_SCOPES: readonly ReviewChangesScope[] = ["combined", "staged", "unstaged"];
const WORKING_SCOPES: readonly ReviewChangesScope[] = ["uncommitted", "staged", "unstaged"];
const UNCOMMITTED_SCOPES: readonly ReviewChangesScope[] = ["uncommitted"];

export type FilesReviewSelection = {
	changesMenu: ReviewChangesMenuProps;
	/** Null where other branches cannot be read (scratch sessions, an older daemon, a degraded manifest) and until that is known. */
	branchMenu: ReviewBranchMenuProps | null;
	/** The session has a pull request the branch menu offers; undefined while the branch list is loading. */
	hasPullRequest: boolean | undefined;
	target: ReviewTarget;
	/** Reads again what a failed target read: the picked branch's history or the combined commits. */
	retryTarget: () => void;
	/** Explains a fall back to the checked-out branch; "" otherwise. */
	notice: string;
	/** Ticks every listed commit between the oldest and newest tick, which always combine. */
	includeCommitsBetween: () => void;
	showAllCommits: () => void;
};

/**
 * The Files review's branch and changes: the menus' props, and the target the
 * review pane reads. The pick lives in the UI store, so the docked and
 * maximized explorers share it; picks only change UI state and send reads.
 */
export function useFilesReviewSelection({
	sessionId,
	hostId,
	manifest,
	projectKind,
	checkedOutBranch,
	onBranchChange,
}: {
	sessionId: string;
	hostId?: string;
	/** The session's branch, which names the checked-out pick until the branch list arrives. */
	checkedOutBranch?: string;
	/** The checked-out manifest. History and branches wait for it, so they never hold up opening Files. */
	manifest: WorkspaceFilesResponse | undefined;
	/** Undefined while unknown: the scopes then read as a single repository's, and the branch list says whether others can be read. */
	projectKind?: ProjectKind;
	/** Called after the branch changes (picked, or gone), e.g. to drop a preview of the old one. */
	onBranchChange?: () => void;
}): FilesReviewSelection {
	const { t } = useTranslation();
	const queryClient = useQueryClient();
	const uiKey = sessionUiKey(sessionId, hostId);
	const stored = useUiStore((state) => state.inspectorSessions[uiKey]?.filesSelection) ?? DEFAULT_FILES_SELECTION;
	const setFilesSelection = useUiStore((state) => state.setFilesSelection);
	const resetFilesSelection = useUiStore((state) => state.resetFilesSelection);
	const setFilesChangedOnly = useUiStore((state) => state.setFilesChangedOnly);
	const branchChanged = useLatestCallback(() => onBranchChange?.());
	const [notice, setNotice] = useState({ uiKey, text: "" });

	const legacy = isLegacyManifest(manifest);
	const scratch = projectKind === "scratch";
	// Scratch sessions have no Git, and a workspace project's manifest fills
	// only Uncommitted for its repositories, so neither has the other scopes.
	const uncommittedOnly = !legacy && (scratch || projectKind === "workspace");
	// An older daemon and a scratch session read only the worktree, so a picked branch reads as it.
	const selection: FilesSelection = (legacy || scratch) && stored.branch.kind !== "checked_out" ? DEFAULT_FILES_SELECTION : stored;
	const branch = selection.branch;

	// The worktree's own changes, commits and pushes arrive as workspace events.
	// Another branch or a PR moves without any, so while one is viewed the list
	// is read again on a timer and when the window regains focus; a moved tip
	// keys a new history read, so the review follows it.
	const viewingOtherBranch = branch.kind !== "checked_out";
	const branchesQuery = useQuery({
		...sessionWorkspaceBranchesQueryOptions(sessionId, t("files.branches.error"), hostId),
		enabled: Boolean(manifest) && !legacy && !scratch,
		refetchInterval: viewingOtherBranch ? OTHER_BRANCH_REFRESH_MS : false,
		// "always": the list counts as fresh for 15s, which would skip a focus read.
		refetchOnWindowFocus: viewingOtherBranch ? "always" : false,
	});
	const branchRows = branchesQuery.data?.branches;
	const selectedRow = branchRows ? findBranchRow(branchRows, branch) : undefined;
	// The PR's branch became local, and the local row carries the PR now.
	const prBranchRow = branch.kind === "pull_request" && selectedRow?.kind === "local" ? selectedRow : undefined;
	// Key another branch's history by the tip the list shows now, so a moved tip reads anew.
	const historyBranch: FilesBranchTarget = branch.kind === "checked_out" || !selectedRow?.tipSha || prBranchRow ? branch : { ...branch, tipSha: selectedRow.tipSha };
	const historyQuery = useQuery({
		...sessionWorkspaceHistoryQueryOptions(sessionId, t("files.error.loadWorkspace"), hostId, historyBranch),
		enabled: Boolean(manifest) && !(uncommittedOnly && branch.kind === "checked_out"),
	});
	const history = historyQuery.data;

	const changes = uncommittedOnly && branch.kind === "checked_out" ? scopeChanges("uncommitted") : normalizeChanges(selection, manifest, legacy, history);
	// Keyed by session too: the explorer stays mounted across a session switch, which is no tick.
	const settledChanges = useSettledChanges(changes, `${uiKey}|${branchKey(branch)}`);
	const request = commitSelectionRequest(branch, settledChanges, manifest, history);
	const selectionQuery = useQuery({
		...sessionWorkspaceCommitSelectionQueryOptions({
			commitShas: request?.commitShas ?? [],
			errorMessage: t("files.error.loadWorkspace"),
			hostId,
			selectionKey: request?.selectionKey ?? "",
			sessionId,
			view: request?.view,
			workspaceVersion: request?.workspaceVersion,
		}),
		enabled: request !== undefined,
	});

	const settledKey = changesKey(settledChanges);
	const target = useMemo(
		() => {
			const built = buildReviewTarget({
				branch,
				changes: settledChanges,
				manifest,
				legacy,
				history,
				historyError: historyQuery.error,
				selection: request ? { data: selectionQuery.data, error: selectionQuery.error, pending: selectionQuery.isPending } : undefined,
			});
			return settledChanges.kind === "commits" && !history
				? { ...built, state: historyQuery.error ? ("error" as const) : ("pending" as const), files: undefined, error: historyQuery.error ?? undefined }
				: built;
		},
		// eslint-disable-next-line react-hooks/exhaustive-deps -- settledChanges and branch are read through their keys: normalizing rebuilds equal values on every render.
		[branchKey(branch), settledKey, manifest, legacy, history, historyQuery.error, request?.selectionKey, selectionQuery.data, selectionQuery.error, selectionQuery.isPending],
	);

	// A moved tip or base fails the read it fenced; the new one arrives under new keys.
	const selectionErrorCode = workspaceErrorCode(selectionQuery.error);
	useEffect(() => {
		if (selectionErrorCode === "WORKSPACE_REF_MOVED") invalidateWorkspaceViews(queryClient, sessionId, hostId);
	}, [hostId, queryClient, selectionErrorCode, sessionId]);

	// The picked branch or PR left the whole list, or its history says it is
	// gone. A capped list leaves branches out, so missing from it proves nothing;
	// the history read still finds a branch that was deleted.
	const historyErrorCode = workspaceErrorCode(historyQuery.error);
	const branchGone = branch.kind !== "checked_out"
		&& ((branchesQuery.isSuccess && !branchesQuery.data.truncated && !selectedRow) || historyErrorCode === "WORKSPACE_BRANCH_NOT_FOUND" || historyErrorCode?.startsWith("PR_") === true);
	const goneName = branch.kind === "branch" ? branch.name
		: branch.kind === "pull_request" ? t("files.branches.prLabel", { number: branch.number, branch: branch.branch })
		: "";
	useEffect(() => {
		if (!branchGone) return;
		resetFilesSelection(uiKey);
		setNotice({ uiKey, text: t("files.explorer.branchUnavailable", { branch: goneName }) });
		branchChanged();
	}, [branchChanged, branchGone, goneName, resetFilesSelection, t, uiKey]);
	// The PR is still there, so the pick quietly follows it to its local branch.
	// Its ticks may not be that branch's commits, so it shows them all.
	useEffect(() => {
		if (!prBranchRow) return;
		setFilesSelection(uiKey, { branch: branchTargetOf(prBranchRow), changes: ALL_COMMITS });
		branchChanged();
	}, [branchChanged, prBranchRow, setFilesSelection, uiKey]);

	const pick = (next: FilesSelection) => {
		setNotice({ uiKey, text: "" });
		setFilesSelection(uiKey, next);
	};
	const listed = useMemo(() => history?.commits.map((commit) => commit.sha) ?? [], [history]);
	// Any pick in the changes menu is something to review, so it also leaves the All files view.
	const pickChanges = (next: FilesChanges) => {
		pick({ branch, changes: next });
		setFilesChangedOnly(uiKey, true);
	};

	const commitRows = useMemo<ReviewCommitRow[]>(
		() => history?.commits.map((commit) => ({ sha: commit.sha, subject: commit.subject, timestamp: commit.timestamp, ...sumChanges(commit.files) })) ?? [],
		[history],
	);
	const scopes = useMemo<ReviewChangesScopeOption[]>(
		() => (legacy ? LEGACY_SCOPES : uncommittedOnly ? UNCOMMITTED_SCOPES : WORKING_SCOPES).map((scope) => {
			// Judged by files, not line totals: a binary-only change counts no lines.
			const files = manifest ? scopeFiles(manifest, scope, legacy) : undefined;
			return { scope, stats: files ? sumChanges(files) : undefined, empty: files ? files.length === 0 : undefined };
		}),
		[legacy, manifest, uncommittedOnly],
	);
	const allStats: ReviewChangeStats | undefined = branch.kind === "checked_out"
		? manifest ? sumChanges(manifest.sections.committed) : undefined
		: history?.rangeSummary ? { additions: history.rangeSummary.additions, deletions: history.rangeSummary.deletions } : undefined;
	const stats = triggerStats();
	function triggerStats(): ReviewChangeStats | undefined {
		if (changes.kind !== "commits") return scopes.find((option) => option.scope === changes.kind)?.stats;
		if (changes.shas.length === 0) return allStats;
		if (changes.shas.length === 1) return sumChanges(commitRows.filter((row) => row.sha === changes.shas[0]));
		// Several commits only have totals once they combine; the rows' sum would
		// count a line changed twice, and commits that can't combine have none.
		const selection = selectionQuery.data;
		if (request && settledKey === changesKey(changes) && selection?.combinable) return { additions: selection.summary.additions, deletions: selection.summary.deletions };
		return undefined;
	}

	const changesMenu: ReviewChangesMenuProps = {
		mode: branch.kind === "checked_out" ? "checked_out" : "view",
		value: changes,
		stats,
		scopes,
		commitsAvailable: !uncommittedOnly,
		onSelectScope: (scope) => pickChanges(scopeChanges(scope)),
		commits: {
			state: history ? "ready" : historyQuery.isError ? "error" : "loading",
			rows: commitRows,
			truncated: history?.commitsTruncated ?? false,
			rangeMode: history?.view?.rangeMode,
			baseRef: history?.view?.baseRef,
			allStats,
			onToggleAll: () => pickChanges(toggleCommit(changes, undefined, listed, legacy)),
			onToggle: (sha) => pickChanges(toggleCommit(changes, sha, listed, legacy)),
			onRetry: () => void historyQuery.refetch(),
		},
	};

	// A 404 that is not about the session is a daemon without the branches route.
	const branchesError = branchesQuery.error;
	const branchesRouteMissing = branchesError instanceof WorkspaceRequestError && branchesError.status === 404 && !branchesError.code?.startsWith("SESSION_");
	const checkedOutRow = branchRows?.find((row) => row.checkedOut);
	const branchTrigger: ReviewBranchTrigger = branch.kind === "branch" ? { kind: "branch", name: branch.name }
		: branch.kind === "pull_request" ? { kind: "pull_request", number: branch.number, branch: branch.branch }
		: { kind: "checked_out", name: checkedOutRow ? checkedOutRow.name || undefined : checkedOutBranch || undefined, detached: checkedOutRow?.detached };
	// A scratch session, an older daemon and a degraded manifest read no other
	// branch. Until the project kind is known, the list says whether one can.
	const branchesUnknown = projectKind === undefined && !branchesQuery.data && !branchesQuery.isError;
	const branchMenu: ReviewBranchMenuProps | null = !manifest || legacy || scratch || branchesQuery.data?.supported === false || branchesRouteMissing || branchesUnknown ? null : {
		trigger: branchTrigger,
		state: branchRows ? "ready" : branchesQuery.isError ? "error" : "loading",
		rows: (branchRows ?? []).map((row): ReviewBranchRow => ({
			id: branchRowId(row),
			kind: row.kind,
			name: row.name,
			prNumber: row.prNumber,
			checkedOut: row.checkedOut,
			detached: row.detached,
			updatedAt: row.updatedAt ?? undefined,
			selected: row === selectedRow,
		})),
		truncated: branchesQuery.data?.truncated ?? false,
		onSelect: (id) => {
			const row = branchRows?.find((entry) => branchRowId(entry) === id);
			if (!row) return;
			pick({ branch: branchTargetOf(row), changes: null });
			branchChanged();
		},
		onRetry: () => void branchesQuery.refetch(),
		// Nothing watches other branches, so the list is read again whenever it opens.
		onOpenChange: (open) => {
			if (open) void branchesQuery.refetch();
		},
	};

	// The list carries the session's PRs, as their own rows or on the local branch they own.
	const hasPullRequest = branchRows ? branchMenu !== null && branchRows.some((row) => row.prNumber != null)
		: branchesQuery.isFetching ? undefined
		: false;

	return {
		changesMenu,
		branchMenu,
		hasPullRequest,
		target,
		retryTarget: () => {
			if (historyQuery.isError) void historyQuery.refetch();
			if (selectionQuery.isError) void selectionQuery.refetch();
		},
		notice: notice.uiKey === uiKey ? notice.text : "",
		includeCommitsBetween: () => {
			if (changes.kind === "commits") pick({ branch, changes: { kind: "commits", shas: ticksIncludingBetween(changes.shas, listed) } });
		},
		showAllCommits: () => pick({ branch, changes: ALL_COMMITS }),
	};
}

/**
 * The changes reads are keyed by. A tick shows in the menu at once but reaches
 * the queries only once ticking pauses; any other pick, and any change of
 * `scope` (the session and branch), applies at once.
 */
function useSettledChanges(changes: FilesChanges, scope: string): FilesChanges {
	const key = changesKey(changes);
	const [settled, setSettled] = useState({ scope, key, changes });
	const latest = useRef(changes);
	latest.current = changes;
	const ticking = settled.key !== key && settled.scope === scope && changes.kind === "commits" && settled.changes.kind === "commits";
	useEffect(() => {
		if (settled.key === key && settled.scope === scope) return;
		const timer = setTimeout(() => setSettled({ scope, key, changes: latest.current }), ticking ? TICK_SETTLE_MS : 0);
		return () => clearTimeout(timer);
	}, [scope, key, settled.scope, settled.key, ticking]);
	return ticking ? settled.changes : changes;
}
