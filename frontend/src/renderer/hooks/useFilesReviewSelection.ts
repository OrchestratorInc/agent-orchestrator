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

const TICK_SETTLE_MS = 250;
const OTHER_BRANCH_REFRESH_MS = 30_000;
const LEGACY_SCOPES: readonly ReviewChangesScope[] = ["combined", "staged", "unstaged"];
const WORKING_SCOPES: readonly ReviewChangesScope[] = ["uncommitted", "staged", "unstaged"];
const UNCOMMITTED_SCOPES: readonly ReviewChangesScope[] = ["uncommitted"];

export type FilesReviewSelection = {
	changesMenu: ReviewChangesMenuProps;
	branchMenu: ReviewBranchMenuProps | null;
	hasPullRequest: boolean | undefined;
	target: ReviewTarget;
	retryTarget: () => void;
	notice: string;
	includeCommitsBetween: () => void;
	showAllCommits: () => void;
};

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
	checkedOutBranch?: string;
	manifest: WorkspaceFilesResponse | undefined;
	projectKind?: ProjectKind;
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
	const uncommittedOnly = !legacy && (scratch || projectKind === "workspace");
	const selection: FilesSelection = (legacy || scratch) && stored.branch.kind !== "checked_out" ? DEFAULT_FILES_SELECTION : stored;
	const branch = selection.branch;

	const viewingOtherBranch = branch.kind !== "checked_out";
	const branchesQuery = useQuery({
		...sessionWorkspaceBranchesQueryOptions(sessionId, t("files.branches.error"), hostId),
		enabled: Boolean(manifest) && !legacy && !scratch,
		refetchInterval: viewingOtherBranch ? OTHER_BRANCH_REFRESH_MS : false,
		refetchOnWindowFocus: viewingOtherBranch ? "always" : false,
	});
	const branchRows = branchesQuery.data?.branches;
	const selectedRow = branchRows ? findBranchRow(branchRows, branch) : undefined;
	const prBranchRow = branch.kind === "pull_request" && selectedRow?.kind === "local" ? selectedRow : undefined;
	const historyBranch: FilesBranchTarget = branch.kind === "checked_out" || !selectedRow?.tipSha || prBranchRow ? branch : { ...branch, tipSha: selectedRow.tipSha };
	const historyQuery = useQuery({
		...sessionWorkspaceHistoryQueryOptions(sessionId, t("files.error.loadWorkspace"), hostId, historyBranch),
		enabled: Boolean(manifest) && !(uncommittedOnly && branch.kind === "checked_out"),
	});
	const history = historyQuery.data;

	const changes = uncommittedOnly && branch.kind === "checked_out" ? scopeChanges("uncommitted") : normalizeChanges(selection, manifest, legacy, history);
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

	const selectionErrorCode = workspaceErrorCode(selectionQuery.error);
	useEffect(() => {
		if (selectionErrorCode === "WORKSPACE_REF_MOVED") invalidateWorkspaceViews(queryClient, sessionId, hostId);
	}, [hostId, queryClient, selectionErrorCode, sessionId]);

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

	const branchesError = branchesQuery.error;
	const branchesRouteMissing = branchesError instanceof WorkspaceRequestError && branchesError.status === 404 && !branchesError.code?.startsWith("SESSION_");
	const checkedOutRow = branchRows?.find((row) => row.checkedOut);
	const branchTrigger: ReviewBranchTrigger = branch.kind === "branch" ? { kind: "branch", name: branch.name }
		: branch.kind === "pull_request" ? { kind: "pull_request", number: branch.number, branch: branch.branch }
		: { kind: "checked_out", name: checkedOutRow ? checkedOutRow.name || undefined : checkedOutBranch || undefined, detached: checkedOutRow?.detached };
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
		onOpenChange: (open) => {
			if (open) void branchesQuery.refetch();
		},
	};

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
