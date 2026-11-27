import { useQueries, useQueryClient } from "@tanstack/react-query";
import { ChevronDown, ChevronRight, ChevronsDownUp, ChevronsUpDown, FileCode2, GitCommitHorizontal, MessageSquarePlus } from "@/components/app-icons";
import { useCallback, useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { cloudWorkspaceReviewDiffsQueryOptions, cloudWorkspaceView, invalidateCloudWorkspaceViews } from "../../hooks/useCloudWorkspaceReview";
import type { FilesReviewSelection } from "../../hooks/useFilesReviewSelection";
import { CloudCpError, type CloudCpClient, type CloudCpWorkspaceReviewCommit, type CloudCpWorkspaceReviewFileSummary, type CloudCpWorkspaceReviewResponse, type CloudCpWorkspaceReviewScope } from "../../lib/cloud-cp";
import { cn } from "../../lib/utils";
import { useUiStore, type CloudReviewUiState } from "../../stores/ui-store";
import type { FileAnnotationModel } from "../WorkspaceDiffView";
import { cancelFileAnnotations, FileAnnotationComposer, FileAnnotationSendBar, PanelMessage, RetryButton } from "../WorkspaceDiffView";
import { Button } from "../ui/button";
import { Popover, PopoverAnchor, PopoverContent } from "../ui/popover";
import { Pressable } from "../ui/pressable";
import { Checkbox } from "../ui/checkbox";
import { ReviewBranchMenu } from "../diffs/ReviewBranchMenu";
import { ReviewChangesMenu } from "../diffs/ReviewChangesMenu";
import { CloudDiffFile } from "./CloudDiffFile";
import type { CloudFileOpenOptions } from "./CloudWorkspaceExplorer";

const PATCH_BATCH_SIZE = 100;
type WorkingScope = "unstaged" | "staged" | "untracked";
const scopeOrder: WorkingScope[] = ["unstaged", "staged", "untracked"];
const statusLabel: Record<CloudCpWorkspaceReviewFileSummary["status"], string> = {
	unmodified: "", modified: "M", added: "A", deleted: "D", renamed: "R", copied: "C", untracked: "U",
};
const statusTone: Record<CloudCpWorkspaceReviewFileSummary["status"], string> = {
	unmodified: "text-passive", modified: "text-warning", added: "text-success", deleted: "text-error",
	renamed: "text-logo-accent-text", copied: "text-logo-accent-text", untracked: "text-success",
};

function chunk<T>(items: readonly T[]): T[][] {
	const result: T[][] = [];
	for (let index = 0; index < items.length; index += PATCH_BATCH_SIZE) result.push(items.slice(index, index + PATCH_BATCH_SIZE));
	return result;
}

function uniqueFiles(files: readonly CloudCpWorkspaceReviewFileSummary[]) {
	const seen = new Set<string>();
	return files.filter((file) => !seen.has(file.path) && Boolean(seen.add(file.path)));
}

function filesForScope(data: CloudCpWorkspaceReviewResponse, scope: CloudCpWorkspaceReviewScope) {
	if (scope === "combined") return uniqueFiles([...data.sections.unstaged, ...data.sections.staged, ...data.sections.untracked]);
	if (scope === "committed") return data.sections.committed;
	return data.sections[scope];
}

function initialSelection(data: CloudCpWorkspaceReviewResponse): { scope: CloudCpWorkspaceReviewScope; commitSha?: string } {
	for (const scope of scopeOrder) if (data.sections[scope].length > 0) return { scope };
	if (data.commits[0]) return { scope: "committed", commitSha: data.commits[0].sha };
	return { scope: "combined" };
}

function deferredByDefault(file: CloudCpWorkspaceReviewFileSummary) {
	const name = file.path.split("/").pop()?.toLowerCase() ?? "";
	return file.binary || file.size > 512 * 1024 || /^(package-lock\.json|pnpm-lock\.yaml|yarn\.lock|bun\.lockb?|go\.sum|cargo\.lock)$/.test(name);
}

type ViewedRecords = Record<string, string>;

function readViewedRecords(storageKey: string): ViewedRecords {
	try { return JSON.parse(window.localStorage.getItem(storageKey) ?? "{}") as ViewedRecords; } catch { return {}; }
}

function isViewedRecord(file: CloudCpWorkspaceReviewFileSummary, records: ViewedRecords) {
	return records[file.path] === file.fileFingerprint;
}

export function CloudWorkspaceReviewPane({
	annotation, baseUrl, client, data, filter, onBrowseAll, onOpenFile, orgId, ready = true, repository, review, sessionId, split,
}: {
	annotation: FileAnnotationModel;
	baseUrl: string;
	client: CloudCpClient;
	data: CloudCpWorkspaceReviewResponse;
	filter: string;
	onBrowseAll: () => void;
	onOpenFile?: (path: string, options?: CloudFileOpenOptions) => void;
	orgId: string;
	/** False while the session's workspace cannot answer yet (useCloudWorkspaceReady): diffs wait, and those read before stay. */
	ready?: boolean;
	/** The sibling repository under review in a multi-repository session; undefined for the main one. */
	repository?: string;
	/**
	 * The branch and changes picked as in a local review. Another branch or a
	 * PR is reviewed from its commits; the checked-out branch keeps this pane's
	 * own scopes and commits. Unset offers the checked-out branch alone.
	 */
	review?: FilesReviewSelection;
	sessionId: string;
	split: boolean;
}) {
	const { t } = useTranslation();
	const queryClient = useQueryClient();
	const target = review?.target;
	const viewing = target !== undefined && target.branch.kind !== "checked_out" && review?.branchMenu != null;
	const view = viewing ? cloudWorkspaceView(target.view, target.commitShas) : undefined;
	const viewFiles = useMemo(() => (viewing ? (target.files ?? []) as unknown as CloudCpWorkspaceReviewFileSummary[] : undefined), [target?.files, viewing]);
	const first = useMemo(() => initialSelection(data), [data]);
	const storedSelection = useUiStore((state) => state.inspectorSessions[sessionId]?.cloudReviewSelection?.[repository ?? ""]);
	const setStoredSelection = useUiStore((state) => state.setCloudReviewSelection);
	const scope = storedSelection?.scope ?? first.scope;
	const commitSha = storedSelection ? storedSelection.commitSha : first.commitSha;
	const reviewUi = useUiStore((state) => state.inspectorSessions[sessionId]?.cloudReviewUi?.[repository ?? ""]);
	const setReviewUi = useUiStore((state) => state.setCloudReviewUi);
	const commitBrowserOpen = reviewUi?.commitBrowserOpen ?? false;
	const setCommitBrowserOpen = (open: boolean) => setReviewUi(sessionId, repository, { commitBrowserOpen: open });
	useEffect(() => {
		if (data.commits.length === 0 && commitBrowserOpen) setReviewUi(sessionId, repository, { commitBrowserOpen: false });
	}, [data.commits.length, commitBrowserOpen, sessionId, repository, setReviewUi]);
	const selectedCommit = useMemo(() => (viewing ? undefined : data.commits.find((commit) => commit.sha === commitSha)), [commitSha, data.commits, viewing]);
	const allFiles = useMemo(
		() => viewFiles ?? (scope === "committed" && selectedCommit ? selectedCommit.files : filesForScope(data, scope)),
		[data, scope, selectedCommit, viewFiles],
	);
	// Another branch or a PR only has commits.
	const reviewScope: CloudCpWorkspaceReviewScope = viewing ? "committed" : scope;
	const normalizedFilter = filter.trim().toLowerCase();
	const files = useMemo(
		() => normalizedFilter ? allFiles.filter((file) => `${file.path} ${file.previousPath ?? ""}`.toLowerCase().includes(normalizedFilter)) : allFiles,
		[allFiles, normalizedFilter],
	);
	// Viewed marks and collapsed files belong to what is reviewed: a view's are keyed by its branch and picked commits.
	const selectionKey = viewing ? `view:${target.key}` : selectedCommit ? `commit:${selectedCommit.sha}` : scope;
	const storageKey = `ao.cloud.files.viewed.${sessionId}.${repository ? `${repository}.` : ""}${selectionKey}`;
	const [viewedRecords, setViewedRecords] = useState<ViewedRecords>(() => readViewedRecords(storageKey));
	useEffect(() => {
		setViewedRecords(readViewedRecords(storageKey));
	}, [storageKey]);
	const fileStates = useMemo<NonNullable<CloudReviewUiState["pane"]>["files"]>(() => {
		const savedViewed = readViewedRecords(storageKey);
		return Object.fromEntries(allFiles.map((file) => {
			const saved = reviewUi?.pane?.selectionKey === selectionKey ? reviewUi.pane.files[file.path] : undefined;
			return [file.path, saved?.fingerprint === file.fileFingerprint ? saved : {
				fingerprint: file.fileFingerprint,
				collapsed: deferredByDefault(file) || isViewedRecord(file, savedViewed),
				loadedDeferred: false,
			}];
		}));
	}, [allFiles, reviewUi?.pane, selectionKey, storageKey]);
	const setFileStates = useCallback((next: NonNullable<CloudReviewUiState["pane"]>["files"]) => {
		setReviewUi(sessionId, repository, { pane: { selectionKey, files: next } });
	}, [repository, selectionKey, sessionId, setReviewUi]);
	useEffect(() => {
		// A view lists no worktree commits; the checked-out branch's pick waits for its return.
		if (viewing) return;
		if (!storedSelection) {
			setStoredSelection(sessionId, repository, first);
			return;
		}
		if (scope === "committed" && selectedCommit) return;
		if (scope === "combined") return;
		if (scope !== "committed" && data.sections[scope].length > 0) return;
		setStoredSelection(sessionId, repository, first);
	}, [data.sections, first, scope, selectedCommit, sessionId, repository, setStoredSelection, storedSelection, viewing]);

	const requested = files.filter((file) => !deferredByDefault(file) || fileStates[file.path]?.loadedDeferred);
	const batches = chunk(requested.map((file) => file.path));
	const diffQueries = useQueries({ queries: batches.map((paths) => ({
		...cloudWorkspaceReviewDiffsQueryOptions({ client, baseUrl, orgId, sessionId, paths, scope: reviewScope, commitSha: selectedCommit?.sha, workspaceVersion: viewing ? undefined : data.workspaceVersion, ignoreWhitespace: false, view, enabled: ready && (!viewing || target.state === "ready") }),
		staleTime: Infinity,
	})) });
	// A view's diffs come from its commits, fenced to the tip and base its
	// history listed; the version they carry names that content.
	const workspaceVersion = viewing ? diffQueries.find((query) => query.data)?.data?.workspaceVersion ?? "" : data.workspaceVersion;
	// A moved tip or base fails the reads it fenced; the views' new tips key new reads.
	const refMoved = diffQueries.some((query) => query.error instanceof CloudCpError && query.error.code === "WORKSPACE_REF_MOVED");
	useEffect(() => {
		if (refMoved) invalidateCloudWorkspaceViews(queryClient, client, baseUrl, orgId, sessionId);
	}, [baseUrl, client, orgId, queryClient, refMoved, sessionId]);
	const openContext: CloudFileOpenOptions = viewing ? { source: target.source } : { commitSha: selectedCommit?.sha, scope };
	const patchByPath = useMemo(() => {
		const result = new Map<string, string>();
		for (const query of diffQueries) for (const group of query.data?.groups ?? []) for (const path of group.includedPaths) result.set(path, group.patch);
		return result;
	}, [diffQueries]);
	const deferredReasonByPath = useMemo(() => {
		const result = new Map<string, string>();
		for (const query of diffQueries) for (const group of query.data?.groups ?? []) for (const entry of group.deferred) result.set(entry.path, entry.reason);
		return result;
	}, [diffQueries]);
	const retryAll = () => diffQueries.forEach((query) => void query.refetch());
	const firstError = diffQueries.find((query) => query.error)?.error;
	const groupError = diffQueries.flatMap((query) => query.data?.groups ?? []).flatMap((group) => group.errors)[0];
	const isViewed = (file: CloudCpWorkspaceReviewFileSummary) => isViewedRecord(file, viewedRecords);
	const toggleViewed = (file: CloudCpWorkspaceReviewFileSummary) => setViewedRecords((current) => {
		const next = { ...current };
		if (next[file.path] === file.fileFingerprint) delete next[file.path]; else next[file.path] = file.fileFingerprint;
		window.localStorage.setItem(storageKey, JSON.stringify(next));
		return next;
	});
	const selectScope = (next: CloudCpWorkspaceReviewScope) => { cancelFileAnnotations(annotation, (target) => target.surface === "review"); setStoredSelection(sessionId, repository, { scope: next }); setCommitBrowserOpen(false); };
	const selectCommit = (commit: CloudCpWorkspaceReviewCommit) => { cancelFileAnnotations(annotation, (target) => target.surface === "review"); setStoredSelection(sessionId, repository, { scope: "committed", commitSha: commit.sha }); setCommitBrowserOpen(false); };
	const collapseFile = useCallback((path: string) => {
		cancelFileAnnotations(annotation, (target) => target.surface === "review" && target.path === path);
		if (fileStates[path]?.collapsed) return;
		setFileStates({ ...fileStates, [path]: { ...fileStates[path], collapsed: true } });
	}, [annotation, fileStates, setFileStates]);
	const toggleCollapsed = (path: string) => setFileStates({ ...fileStates, [path]: { ...fileStates[path], collapsed: !fileStates[path]?.collapsed } });
	const markViewed = useCallback((file: CloudCpWorkspaceReviewFileSummary, checked: boolean) => {
		toggleViewed(file);
		if (checked) collapseFile(file.path);
	}, [collapseFile, toggleViewed]);
	const allCollapsed = files.length > 0 && files.every((file) => fileStates[file.path]?.collapsed);
	const viewedCount = allFiles.filter(isViewed).length;
	const visibleScopes = scopeOrder.filter((entry) => data.sections[entry].length > 0);
	const combinedFiles = filesForScope(data, "combined");

	const showCommitBrowser = commitBrowserOpen && !viewing;
	const conflict = target?.conflict;

	return <div className="flex h-full min-h-0 flex-col">
		<div className="flex shrink-0 flex-wrap items-center gap-1 border-b border-border bg-surface px-2 py-1.5">
			{review?.branchMenu ? <ReviewBranchMenu {...review.branchMenu} /> : null}
			{viewing ? <ReviewChangesMenu {...review.changesMenu} /> : <>
			{combinedFiles.length > 0 ? <Button aria-pressed={scope === "combined"} onClick={() => selectScope("combined")} size="sm" variant="ghost">{t("files.reviewChanges")}<span className="text-caption text-passive">{combinedFiles.length}</span></Button> : null}
			{visibleScopes.map((entry) => <Button aria-pressed={scope === entry} key={entry} onClick={() => selectScope(entry)} size="sm" variant="ghost">{t(`files.section.${entry}`)}<span className="text-caption text-passive">{data.sections[entry].length}</span></Button>)}
			<Button aria-expanded={commitBrowserOpen} aria-pressed={scope === "committed"} disabled={data.commits.length === 0} onClick={() => setCommitBrowserOpen(!commitBrowserOpen)} size="sm" variant="ghost"><GitCommitHorizontal className="size-icon-sm" />{t("files.commits")}{selectedCommit ? <span className="text-caption text-passive">{selectedCommit.sha.slice(0, 7)}</span> : null}</Button>
			</>}
			{!showCommitBrowser ? <div className="ml-auto flex items-center gap-1 text-caption text-muted-foreground">
				<span>{t("files.reviewProgress", { total: allFiles.length, viewed: viewedCount })}</span>
				<Button aria-label={t(allCollapsed ? "files.expandAll" : "files.collapseAll")} onClick={() => setFileStates({ ...fileStates, ...Object.fromEntries(files.map((file) => [file.path, { ...fileStates[file.path], collapsed: !allCollapsed }])) })} size="icon-sm" variant="ghost">{allCollapsed ? <ChevronsUpDown /> : <ChevronsDownUp />}</Button>
			</div> : null}
		</div>
		{/* The branch shows in its picker; this row only explains a fall back to the checked-out branch. */}
		{review?.notice ? <p className="shrink-0 border-b border-border px-3 py-1 text-2xs text-muted-foreground" role="status">{review.notice}</p> : null}
		{showCommitBrowser ? <CommitBrowser commits={data.commits} filter={filter} onSelect={selectCommit} selectedSha={selectedCommit?.sha} /> : viewing && target.state === "pending" ? (
			<PanelMessage compact>{(target.commitShas?.length ?? 0) > 1 ? t("files.changes.combining") : t("files.loading")}</PanelMessage>
		) : viewing && target.state === "error" ? (
			<PanelMessage action={<RetryButton onClick={review.retryTarget} />}>{target.error?.message || t("files.error.loadWorkspace")}</PanelMessage>
		) : viewing && target.state === "not_combinable" ? (
			<PanelMessage
				action={<div className="flex flex-wrap items-center justify-center gap-2">
					<Button onClick={review.includeCommitsBetween} size="sm" type="button" variant="secondary">{t("files.changes.includeBetween")}</Button>
					<Button onClick={review.showAllCommits} size="sm" type="button" variant="secondary">{t("files.changes.showAllCommits")}</Button>
				</div>}
			>
				{conflict?.reason === "unsupported_history" ? t("files.changes.unsupportedHistory") : t("files.changes.notCombinable", { subject: conflict?.subject || conflict?.commitSha.slice(0, 7) || "" })}
			</PanelMessage>
		) : <div className="board-scrollbar min-h-0 flex-1 overflow-y-auto" data-files-scroll-root>
			{firstError ? <PanelMessage action={<RetryButton onClick={retryAll} />}>{firstError.message}</PanelMessage> : null}
			{groupError ? <PanelMessage action={<RetryButton onClick={retryAll} />}>{groupError.message}</PanelMessage> : null}
			{diffQueries.some((query) => query.isPending) && patchByPath.size === 0 ? <PanelMessage compact>{t("files.loadingDiff")}</PanelMessage> : null}
			{files.length === 0 ? <PanelMessage action={allFiles.length === 0 && !viewing ? <Button onClick={onBrowseAll}>{t("files.browseAll")}</Button> : undefined} compact>{allFiles.length === 0 ? t("files.noneChanged") : t("files.noFilterMatches")}</PanelMessage> : null}
			{files.map((file) => {
				const patch = patchByPath.get(file.path);
				const deferred = deferredByDefault(file) && !fileStates[file.path]?.loadedDeferred;
				const reason = deferredReasonByPath.get(file.path);
				const closed = fileStates[file.path]?.collapsed;
				const fileAnnotationTarget = annotation.targets.find((target) => target.surface !== "focused" && target.path === file.path && target.side === "file");
				return <section className="border-b border-border" key={`${selectionKey}:${file.path}`}>
					<Popover onOpenChange={(open) => { if (!open && fileAnnotationTarget) annotation.cancel(fileAnnotationTarget); }} open={fileAnnotationTarget != null}>
					<PopoverAnchor asChild>
					<header className="flex h-10 min-w-0 items-center gap-2 bg-surface px-2">
						<Button aria-label={closed ? t("files.expandFile", { file: file.path }) : t("files.collapseFile", { file: file.path })} onClick={() => toggleCollapsed(file.path)} size="icon-sm" variant="ghost">{closed ? <ChevronRight /> : <ChevronDown />}</Button>
						<span className={cn("font-mono text-xs font-semibold", statusTone[file.status])}>{statusLabel[file.status]}</span>
						<Pressable className="min-w-0 flex-1 truncate font-mono text-xs hover:underline" onClick={() => toggleCollapsed(file.path)}>{file.path}</Pressable>
						<span className="text-caption text-success">+{file.additions}</span><span className="text-caption text-error">−{file.deletions}</span>
						<Button aria-label={t("files.addFeedback")} aria-pressed={fileAnnotationTarget != null} onClick={() => annotation.begin({ path: file.path, previousPath: file.previousPath, side: "file", scope: reviewScope, surface: "review", workspaceVersion, fileFingerprint: file.fileFingerprint })} size="icon-sm" variant="ghost"><MessageSquarePlus /></Button>
						<Button aria-label={t("files.openFullFileGeneric")} onClick={() => onOpenFile?.(file.path, { ...openContext, mode: "file" })} size="icon-sm" variant="ghost"><FileCode2 /></Button>
						<Checkbox aria-label={isViewed(file) ? t("files.markUnviewed", { file: file.path }) : t("files.markViewed", { file: file.path })} checked={isViewed(file)} onCheckedChange={(checked) => markViewed(file, checked === true)} />
					</header>
					</PopoverAnchor>
					{/* Whole-file feedback floats under the header's right edge, as in a
					    local review: it stays open on outside clicks so a draft isn't lost;
					    its close button, Esc, or this file's feedback button close it. */}
					<PopoverContent
						align="end"
						aria-label={t("files.addFeedback")}
						className="w-[min(32rem,var(--radix-popover-trigger-width))] rounded-2xl border-0 bg-transparent p-0"
						hideWhenDetached
						onInteractOutside={(event) => event.preventDefault()}
						onOpenAutoFocus={(event) => event.preventDefault()}
						side="bottom"
						sideOffset={0}
					>
						{fileAnnotationTarget ? <FileAnnotationComposer annotation={annotation} target={fileAnnotationTarget} /> : null}
					</PopoverContent>
					</Popover>
					{/* A sibling repository's paths are relative to it, so they would not name the file in the session's chat. */}
					{!closed && patch ? <CloudDiffFile annotation={annotation} baseUrl={baseUrl} chatReferences={repository === undefined} client={client} commitSha={selectedCommit?.sha} file={file} onActiveSelectionChange={() => undefined} orgId={orgId} patch={patch} scope={reviewScope} sessionId={sessionId} source={viewing ? target.source : undefined} split={split} surface="review" view={view} workspaceVersion={workspaceVersion} /> : null}
					{!closed && !patch ? <div className="flex items-center gap-2 p-3 text-xs text-muted-foreground"><span className="min-w-0 flex-1">{file.binary ? t("files.binaryUnavailable") : deferred ? t("files.deferredDiff") : reason ? t("files.diffUnavailableReason", { reason }) : t("files.loadingDiff")}</span>{deferred ? <Button onClick={() => setFileStates({ ...fileStates, [file.path]: { ...fileStates[file.path], loadedDeferred: true } })} size="sm" variant="secondary">{t("files.loadDiff")}</Button> : null}<Button onClick={() => onOpenFile?.(file.path, { ...openContext, mode: "file" })} size="sm" variant="secondary">{t("files.fileView")}</Button></div> : null}
				</section>;
			})}
		</div>}
		<FileAnnotationSendBar annotation={annotation} surface="review" />
	</div>;
}

function CommitBrowser({ commits, filter, onSelect, selectedSha }: { commits: readonly CloudCpWorkspaceReviewCommit[]; filter: string; onSelect: (commit: CloudCpWorkspaceReviewCommit) => void; selectedSha?: string }) {
	const { t } = useTranslation();
	const normalized = filter.trim().toLowerCase();
	const visible = normalized ? commits.filter((commit) => `${commit.subject} ${commit.author} ${commit.sha} ${commit.files.map((file) => file.path).join(" ")}`.toLowerCase().includes(normalized)) : commits;
	return <ul aria-label={t("files.commitHistory")} className="board-scrollbar min-h-0 flex-1 overflow-y-auto">{visible.map((commit) => <li key={commit.sha}><Pressable aria-current={selectedSha === commit.sha ? "true" : undefined} className="block w-full border-b border-border px-3 py-3 hover:bg-interactive-hover" onClick={() => onSelect(commit)}><div className="flex items-center gap-2"><GitCommitHorizontal className="size-icon-sm text-passive" /><span className="min-w-0 flex-1 truncate text-xs font-medium">{commit.subject}</span><span className="font-mono text-caption text-passive">{commit.sha.slice(0, 7)}</span></div><div className="mt-1 pl-6 text-caption text-muted-foreground">{commit.author} · {t("files.count", { count: commit.files.length })}</div></Pressable></li>)}</ul>;
}
