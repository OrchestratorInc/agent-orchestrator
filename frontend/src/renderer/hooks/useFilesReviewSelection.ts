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
	type WorkspaceHistoryResponse,
} from "./useSessionWorkspaceFiles";

/** Ticks reach the queries this long after the last one, so a run of ticks sends one request. */
const TICK_SETTLE_MS = 250;
const LEGACY_SCOPES: readonly ReviewChangesScope[] = ["combined", "staged", "unstaged"];
const WORKING_SCOPES: readonly ReviewChangesScope[] = ["uncommitted", "staged", "unstaged"];

export type FilesReviewSelection = {
	changesMenu: ReviewChangesMenuProps;
	/** Null where other branches cannot be read: scratch sessions, an older daemon, a degraded manifest. */
	branchMenu: ReviewBranchMenuProps | null;
	target: ReviewTarget;
	/** The picked branch's history; the checked-out branch's carries ahead/behind. */
	history: WorkspaceHistoryResponse | undefined;
	/** Explains a fall back to the checked-out branch; "" otherwise. */
	notice: string;
	legacy: boolean;
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
	onBranchChange,
}: {
	sessionId: string;
	hostId?: string;
	/** The checked-out manifest. History and branches wait for it, so they never hold up opening Files. */
	manifest: WorkspaceFilesResponse | undefined;
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
	// An older daemon reads only the worktree, so a picked branch reads as it.
	const selection: FilesSelection = legacy && stored.branch.kind !== "checked_out" ? DEFAULT_FILES_SELECTION : stored;
	const branch = selection.branch;

	const branchesQuery = useQuery({
		...sessionWorkspaceBranchesQueryOptions(sessionId, t("files.branches.error"), hostId),
		enabled: Boolean(manifest) && !legacy,
	});
	const branchRows = branchesQuery.data?.branches;
	const selectedRow = branchRows ? findBranchRow(branchRows, branch) : undefined;
	// Key another branch's history by the tip the list shows now, so a moved tip reads anew.
	const historyBranch: FilesBranchTarget = branch.kind === "checked_out" || !selectedRow?.tipSha ? branch : { ...branch, tipSha: selectedRow.tipSha };
	const historyQuery = useQuery({
		...sessionWorkspaceHistoryQueryOptions(sessionId, t("files.error.loadWorkspace"), hostId, historyBranch),
		enabled: Boolean(manifest),
	});
	const history = historyQuery.data;

	const changes = normalizeChanges(selection, manifest, legacy, history);
	const settledChanges = useSettledChanges(changes, branchKey(branch));
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
		() => buildReviewTarget({
			branch,
			changes: settledChanges,
			manifest,
			legacy,
			history,
			historyError: historyQuery.error,
			selection: request ? { data: selectionQuery.data, error: selectionQuery.error, pending: selectionQuery.isPending } : undefined,
		}),
		// eslint-disable-next-line react-hooks/exhaustive-deps -- settledChanges and branch are read through their keys: normalizing rebuilds equal values on every render.
		[branchKey(branch), settledKey, manifest, legacy, history, historyQuery.error, request?.selectionKey, selectionQuery.data, selectionQuery.error, selectionQuery.isPending],
	);

	// A moved tip or base fails the read it fenced; the new one arrives under new keys.
	const selectionErrorCode = workspaceErrorCode(selectionQuery.error);
	useEffect(() => {
		if (selectionErrorCode === "WORKSPACE_REF_MOVED") invalidateWorkspaceViews(queryClient, sessionId, hostId);
	}, [hostId, queryClient, selectionErrorCode, sessionId]);

	// The picked branch or PR left the list, or its history says it is gone.
	const historyErrorCode = workspaceErrorCode(historyQuery.error);
	const branchGone = branch.kind !== "checked_out"
		&& ((branchesQuery.isSuccess && !selectedRow) || historyErrorCode === "WORKSPACE_BRANCH_NOT_FOUND" || historyErrorCode?.startsWith("PR_") === true);
	const goneName = branch.kind === "branch" ? branch.name
		: branch.kind === "pull_request" ? t("files.branches.prLabel", { number: branch.number, branch: branch.branch })
		: "";
	useEffect(() => {
		if (!branchGone) return;
		resetFilesSelection(uiKey);
		setNotice({ uiKey, text: t("files.explorer.branchUnavailable", { branch: goneName }) });
		branchChanged();
	}, [branchChanged, branchGone, goneName, resetFilesSelection, t, uiKey]);

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
		() => (legacy ? LEGACY_SCOPES : WORKING_SCOPES).map((scope) => ({ scope, stats: manifest ? sumChanges(scopeFiles(manifest, scope, legacy)) : undefined })),
		[legacy, manifest],
	);
	const allStats: ReviewChangeStats | undefined = branch.kind === "checked_out"
		? manifest ? sumChanges(manifest.sections.committed) : undefined
		: history?.rangeSummary ? { additions: history.rangeSummary.additions, deletions: history.rangeSummary.deletions } : undefined;
	const stats = triggerStats();
	function triggerStats(): ReviewChangeStats | undefined {
		if (changes.kind !== "commits") return scopes.find((option) => option.scope === changes.kind)?.stats;
		if (changes.shas.length === 0) return allStats;
		const summary = selectionQuery.data?.summary;
		if (changes.shas.length > 1 && request && settledKey === changesKey(changes) && summary) return { additions: summary.additions, deletions: summary.deletions };
		const ticked = new Set(changes.shas);
		return sumChanges(commitRows.filter((row) => ticked.has(row.sha)));
	}

	const changesMenu: ReviewChangesMenuProps = {
		mode: branch.kind === "checked_out" ? "checked_out" : "view",
		value: changes,
		stats,
		scopes,
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
		: { kind: "checked_out", name: checkedOutRow?.name || undefined, detached: checkedOutRow?.detached };
	const branchMenu: ReviewBranchMenuProps | null = legacy || branchesQuery.data?.supported === false || branchesRouteMissing ? null : {
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

	return {
		changesMenu,
		branchMenu,
		target,
		history,
		notice: notice.uiKey === uiKey ? notice.text : "",
		legacy,
		includeCommitsBetween: () => {
			if (changes.kind === "commits") pick({ branch, changes: { kind: "commits", shas: ticksIncludingBetween(changes.shas, listed) } });
		},
		showAllCommits: () => pick({ branch, changes: ALL_COMMITS }),
	};
}

/**
 * The changes reads are keyed by. A tick shows in the menu at once but reaches
 * the queries only once ticking pauses; any other pick applies at once.
 */
function useSettledChanges(changes: FilesChanges, branch: string): FilesChanges {
	const key = changesKey(changes);
	const [settled, setSettled] = useState({ branch, key, changes });
	const latest = useRef(changes);
	latest.current = changes;
	const ticking = settled.key !== key && settled.branch === branch && changes.kind === "commits" && settled.changes.kind === "commits";
	useEffect(() => {
		if (settled.key === key && settled.branch === branch) return;
		const timer = setTimeout(() => setSettled({ branch, key, changes: latest.current }), ticking ? TICK_SETTLE_MS : 0);
		return () => clearTimeout(timer);
	}, [branch, key, settled.branch, settled.key, ticking]);
	return ticking ? settled.changes : changes;
}
