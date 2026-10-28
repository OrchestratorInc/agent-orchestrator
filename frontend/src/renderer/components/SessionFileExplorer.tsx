import { branchKey } from "../lib/files-review-selection";
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { createPortal } from "react-dom";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import {
	FolderTree,
	Folders,
	GitCompareArrows,
	Maximize2,
	Minimize2,
 Columns2,
 Rows3,
 ListChevronsDownUp,
 ListChevronsUpDown,
	Search,
} from "lucide-react";
import {
	sessionSourceFileQueryOptions,
	sessionWorkspaceFilesQueryOptions,
	type FilesBranchTarget,
	type FilesSource,
	type WorkspaceFilesResponse,
	useWorkspaceFileConnectionState,
	workspaceFilesRefetchInterval,
} from "../hooks/useSessionWorkspaceFiles";
import { useFilesReviewSelection } from "../hooks/useFilesReviewSelection";
import { subscribeWorkspaceFileChanges } from "../lib/workspace-file-events";
import { buildChangedOnlyTree, buildWorkspaceFileTree, sessionWorkspaceTreeQueryOptions, type TreeNode } from "../hooks/useSessionWorkspaceTree";
import { useFileAnnotation } from "../hooks/useFileAnnotation";
import { useUiStore } from "../stores/ui-store";
import { cn } from "../lib/utils";
import { Button } from "./ui/button";
import { Input } from "./ui/input";
import { Tooltip, TooltipContent, TooltipTrigger } from "./ui/tooltip";
import { ResizableHandle, ResizablePanel, ResizablePanelGroup } from "./ui/resizable";

import { useFilesActionsHost, useFilesTopbarHost } from "./files-topbar-host";
import { FileTree, FileTreeSkeleton } from "./FileTree";
import { FileContentPane, type FileOpenOptions, type FileViewMode } from "./FileContentPane";
import { PanelMessage, RetryButton } from "./WorkspaceDiffView";
import { WorkspaceReviewPane } from "./diffs/WorkspaceReviewPane";
import { ReviewChangesMenu } from "./diffs/ReviewChangesMenu";
import { ReviewBranchMenu } from "./diffs/ReviewBranchMenu";
import { markFileViewerPerformance } from "../lib/file-viewer-performance";
import { sessionUiKey } from "../lib/hosts";
import { isSvgPath } from "../lib/image-files";
import type { ProjectKind } from "../types/workspace";

const WORKSPACE_SOURCE: FilesSource = { kind: "workspace" };

// The agent reads which branch a comment is about from this.
function annotationSource(branch: FilesBranchTarget): string {
	if (branch.kind === "branch") return `Branch ${branch.name}`;
	if (branch.kind === "pull_request") return `PR #${branch.number} (${branch.url})`;
	return "Workspace";
}

function hasReviewableChanges(manifest: WorkspaceFilesResponse): boolean {
	const { sections } = manifest;
	return manifest.files.some((file) => file.status !== "unmodified")
		|| [sections.uncommitted, sections.staged, sections.unstaged, sections.untracked, sections.committed].some((section) => (section?.length ?? 0) > 0);
}

// Mirrors the browser panel's tab strip (.browser-panel__tab): no container
// box, 28px rounded tabs, filled only when active.
const viewTabClass = "inline-flex h-control-md items-center rounded-md px-2.5 text-xs font-medium transition-colors focus-visible:outline-2 focus-visible:-outline-offset-2 focus-visible:outline-accent/50";

type SessionFileExplorerProps = {
	sessionId: string;
	hostId?: string;
	isMaximized?: boolean;
	onOpenFile?: (path: string, options?: FileOpenOptions) => void;
	onSplitChange?: (split: boolean) => void;
	onToggleMaximized?: (next: boolean) => void;
	/** Which Git scopes and branches the review offers; undefined while unknown. */
	projectKind?: ProjectKind;
	/** The session's branch, shown on the branch picker before its list loads. */
	checkedOutBranch?: string;
	split?: boolean;
};

export function SessionFileExplorer({
	sessionId,
	hostId,
	isMaximized = false,
	onOpenFile,
	onSplitChange,
	onToggleMaximized,
	projectKind,
	checkedOutBranch,
	split: controlledSplit,
}: SessionFileExplorerProps) {
	const { t } = useTranslation();
	const [filter, setFilter] = useState("");
 const filtersBySource=useRef(new Map<string,string>());
	const [internalSplit, setInternalSplit] = useState(() => window.localStorage.getItem("ao.files.diffStyle") === "split");
	const split = controlledSplit ?? internalSplit;
	const [selectedPath, setSelectedPath] = useState<string | null>(null);
	// Maximized, the review pane's edit/preview actions open the file in this
	// view's own preview (the center pane is hidden behind the overlay).
	const [previewRequest, setPreviewRequest] = useState<(FileOpenOptions & { key: number }) | null>(null);
	const filesTopbarHost = useFilesTopbarHost();
	const filesActionsHost = useFilesActionsHost();
	// The review owns collapsed paths; the toolbar requests a toggle-all action.
	const [reviewAllCollapsed, setReviewAllCollapsed] = useState(false);
	const [collapseRequest, setCollapseRequest] = useState<{ expand: boolean; key: number }>();
	const [treeOpen, setTreeOpen] = useState(true);
	const uiKey = sessionUiKey(sessionId, hostId);
	const queryClient = useQueryClient();
	const connectionState = useWorkspaceFileConnectionState(sessionId, hostId);
	useLayoutEffect(() => markFileViewerPerformance("files-shell-painted"), []);

	const changedOnly = useUiStore((state) => state.inspectorSessions[uiKey]?.filesChangedOnly ?? true);
	const setFilesChangedOnly = useUiStore((state) => state.setFilesChangedOnly);

	const filesQuery = useQuery({
		...sessionWorkspaceFilesQueryOptions(sessionId, t("files.error.loadWorkspace"), hostId),
		refetchInterval: (query) => workspaceFilesRefetchInterval(connectionState, Boolean(query.state.data?.degraded)),
	});
	useLayoutEffect(() => {
		if (filesQuery.data) markFileViewerPerformance("manifest-ready");
	}, [filesQuery.data?.workspaceVersion]);
	const filesData = filesQuery.data;
	const review = useFilesReviewSelection({
		sessionId,
		hostId,
		manifest: filesData,
		projectKind,
		checkedOutBranch,
		// A preview of the old branch's file would be stale.
		onBranchChange: () => {
			setPreviewRequest(null);
			setSelectedPath(null);
		},
	});
	const branch = review.target.branch;
 const filterKey=`${uiKey}:${branchKey(branch)}`;
 useEffect(()=>setFilter(filtersBySource.current.get(filterKey) ?? ""),[filterKey]);
	// Browsing and file opening preserve this review source.
	const onOtherBranch = branch.kind !== "checked_out";
	const annotation = useFileAnnotation(sessionId, { hostId, source: annotationSource(branch) });
	const changedOnlyData = useMemo(
		() => (filesData ? buildChangedOnlyTree(filesData.files) : []),
		[filesData],
	);
	const hasChanges = filesData ? hasReviewableChanges(filesData) : false;
	const showChanges = changedOnly && (!filesData || hasChanges || onOtherBranch);
 const source = review.target.source ?? WORKSPACE_SOURCE;
	const splitView = !showChanges && isMaximized;
	const showChangesTrigger = Boolean(filesData) && (hasChanges || onOtherBranch);
	const branchMenu = review.branchMenu;

	useEffect(() => {
		setSelectedPath(null);
		setFilter("");
	}, [uiKey]);

	useEffect(() => subscribeWorkspaceFileChanges(sessionId, queryClient, hostId), [queryClient, sessionId, hostId]);
	useEffect(() => {
		window.localStorage.setItem("ao.files.diffStyle", split ? "split" : "unified");
	}, [split]);

	// Give the hovered/focused row a short head start, without issuing a
	// request for every row the pointer crosses or competing with a busy read.
	const prefetchTimer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
	const prefetchBusy = useRef(false);
	useEffect(() => () => {
		if (prefetchTimer.current) clearTimeout(prefetchTimer.current);
	}, [uiKey]);
	const prefetchPath = useCallback((node: TreeNode) => {
		if (prefetchTimer.current) clearTimeout(prefetchTimer.current);
		prefetchTimer.current = setTimeout(() => {
			prefetchTimer.current = undefined;
			if (prefetchBusy.current) return;
			prefetchBusy.current = true;
			const request = node.type === "dir"
				? queryClient.prefetchQuery({ ...sessionWorkspaceTreeQueryOptions(sessionId, node.path, undefined, hostId,source), staleTime: 5_000 })
				: queryClient.prefetchQuery({ ...sessionSourceFileQueryOptions(sessionId,source,node.path,undefined,"combined",undefined,"",hostId), staleTime: 5_000 });
			void request.finally(() => { prefetchBusy.current = false; });
		}, 100);
	}, [queryClient, sessionId, hostId,source]);

	const handleSelectPath = (node: TreeNode) => {
		setPreviewRequest(null);
		setSelectedPath(node.path);
		if (!isMaximized) onOpenFile?.(node.path, { ...(source.kind === "snapshot" ? {source}:{}), mode: isSvgPath(node.path) ? "rendered" : "file" });
	};
	const handleViewChange = (next: boolean) => {
		setPreviewRequest(null);
		setSelectedPath(null);
		setFilesChangedOnly(uiKey, next);
	};
	const openInMaximizedPreview = (path: string, options?: FileOpenOptions) => {
		setPreviewRequest((current) => ({ ...options, key: (current?.key ?? 0) + 1 }));
		setSelectedPath(path);
		setFilesChangedOnly(uiKey, false);
	};
	const openPreviewInCenter = (mode: FileViewMode) => {
		if (!selectedPath || !onOpenFile) return;
		onToggleMaximized?.(false);
		onOpenFile(selectedPath, {
			...previewRequest,
			source: previewRequest?.source ?? source,
			previousPath: previewRequest?.previousPath ?? selectedPreviousPath,
			mode,
			editing: false,
		});
	};

	const treeSelectedPath = selectedPath;
	const selectedPreviousPath = filesData?.files.find((file) => file.path === selectedPath)?.previousPath;

	// Both live and historical sources support complete repository browsing.
	const hasViewTabs = hasChanges || onOtherBranch;
	const setLayout = (next: boolean) => {
		if (controlledSplit === undefined) setInternalSplit(next);
		onSplitChange?.(next);
	};
	// Unified/split applies wherever a diff shows: the Changes review and the
	// preview beside the tree. A review with no files has nothing to lay out or
	// collapse, so both go until it has some; while it loads they stay put.
	const reviewHasFiles = review.target.state === "pending" || (review.target.files?.length ?? 0) > 0;
	// Beside the tree only the maximized preview can show a diff, and only of a
	// changed file (any file of another branch's snapshot may differ). Docked,
	// the tree opens plain files in the centre, whose diffs keep the layout last
	// picked in the Changes view.
	const previewHasDiff = splitView && selectedPath !== null
		&& (source.kind === "snapshot" || Boolean(filesData?.files.some((file) => file.path === selectedPath && file.status !== "unmodified")));
	const showLayout = showChanges ? reviewHasFiles : previewHasDiff;
	const showCollapse = showChanges && Boolean(filesData) && reviewHasFiles;
	// With the filter and maximize in a top bar, the header holds only the
	// pickers and the view controls. A session with nothing to review (no
	// changes, no pull request, no other branch picked) drops the whole row and
	// shows just the tree. Until the manifest and the branch list say so, the
	// row is kept and a review never jumps.
	const showHeader = !filesTopbarHost || !filesData || hasViewTabs || review.hasPullRequest !== false;
	const showTreeToggle = showHeader && splitView && !hasViewTabs;
	// Without the header nothing can reopen a hidden tree, so it always shows.
	const treeShown = treeOpen || !showHeader;
	// Maximized, the Changes review gets a rail of just its changed files, like
	// the All files tree beside the preview; picking one scrolls to its diff.
	const reviewFiles = review.target.files;
	const reviewTreeData = useMemo(() => buildWorkspaceFileTree(reviewFiles ?? []), [reviewFiles]);
	// With no files the rail would only say the folder is empty, so the review
	// takes the whole width instead. A read in flight keeps the last answer, so
	// moving between two empty picks doesn't flash the rail in and out.
	const [reviewRailShown, setReviewRailShown] = useState(() => (reviewFiles?.length ?? 0) > 0);
	useLayoutEffect(() => {
		if (review.target.state !== "pending") setReviewRailShown((reviewFiles?.length ?? 0) > 0);
	}, [review.target.state, reviewFiles]);
	// A filter that matches none of the files empties the rail too; the review's
	// own no-match message then takes the whole width. Matched as the review does.
	const reviewFilter = filter.trim().toLowerCase();
	const reviewFilterMatches = !reviewFilter || (reviewFiles ?? []).some((file) => `${file.path} ${file.previousPath ?? ""}`.toLowerCase().includes(reviewFilter));
	const [reviewTreePath, setReviewTreePath] = useState<string | null>(null);
	const [reviewScrollRequest, setReviewScrollRequest] = useState<{ path: string; key: number }>();
	const selectReviewTreePath = (node: TreeNode) => {
		setReviewTreePath(node.path);
		setReviewScrollRequest((current) => ({ path: node.path, key: (current?.key ?? 0) + 1 }));
	};
	// In the preview + tree split the (already active) All-files tab doubles as
	// the tree toggle, so the header doesn't carry two file-tree buttons.
	const allFilesTabLabel = !showChanges && splitView ? (treeOpen ? t("files.hideFileTree") : t("files.showFileTree")) : t("files.allFiles");

	const filterField = (
		<label className={cn("relative min-w-0", filesTopbarHost ? "block w-full" : "w-64 shrink")}>
			<Search className="pointer-events-none absolute left-2.5 top-1/2 size-icon-sm -translate-y-1/2 text-passive" />
			<Input
				aria-label={t("files.explorer.filter")}
				className="inspector-field-input pl-8"
				onChange={(event) => {setFilter(event.target.value);filtersBySource.current.set(filterKey,event.target.value);}}
				placeholder={t("files.explorer.filterPlaceholder")}
				value={filter}
			/>
		</label>
	);

	const viewMenu = (
		<>
			{showLayout ? (
				<Tooltip>
					<TooltipTrigger asChild>
						<Button
							aria-label={split ? t("files.unifiedDiff") : t("files.splitDiff")}
							aria-pressed={split}
							onClick={() => setLayout(!split)}
							size="icon-sm"
							type="button"
							variant="ghost"
						>
							{split ? <Columns2 aria-hidden="true" className="size-icon-base" /> : <Rows3 aria-hidden="true" className="size-icon-base" />}
						</Button>
					</TooltipTrigger>
					<TooltipContent side="bottom">{split ? t("files.unifiedDiff") : t("files.splitDiff")}</TooltipContent>
				</Tooltip>
			) : null}
			{showCollapse ? (
				<Tooltip>
					<TooltipTrigger asChild>
						<Button
							aria-label={t(reviewAllCollapsed ? "files.expandAll" : "files.collapseAll")}
							onClick={() => setCollapseRequest((current) => ({ expand: reviewAllCollapsed, key: (current?.key ?? 0) + 1 }))}
							size="icon-sm"
							type="button"
							variant="ghost"
						>
							{reviewAllCollapsed ? <ListChevronsUpDown aria-hidden="true" className="size-icon-base" /> : <ListChevronsDownUp aria-hidden="true" className="size-icon-base" />}
						</Button>
					</TooltipTrigger>
					<TooltipContent side="bottom">{t(reviewAllCollapsed ? "files.expandAll" : "files.collapseAll")}</TooltipContent>
				</Tooltip>
			) : null}
		</>
	);

	const maximizeButton = onToggleMaximized ? (
		<Tooltip>
			{/* Closing the overlay hands focus back to this button, and a tooltip
			    that opens on that focus stays up until the pointer comes back.
			    Only keyboard focus (focus-visible) opens it; hover still does. */}
			<TooltipTrigger asChild onFocus={(event) => { if (!event.currentTarget.matches(":focus-visible")) event.preventDefault(); }}>
				{/* Sized and toned like the pinned top-bar buttons beside it. */}
				<Button
					aria-label={isMaximized ? t("common.returnToPanel") : t("common.maximize")}
					className="shrink-0 text-muted-foreground hover:text-foreground"
					onClick={() => onToggleMaximized(!isMaximized)}
					size="icon-sm"
					type="button"
					variant="ghost"
				>
					{isMaximized ? (
						<Minimize2 className="size-icon-md" aria-hidden="true" />
					) : (
						<Maximize2 className="size-icon-md" aria-hidden="true" />
					)}
				</Button>
			</TooltipTrigger>
			<TooltipContent side="bottom">{isMaximized ? t("common.returnToPanel") : t("common.maximize")}</TooltipContent>
		</Tooltip>
	) : null;

	const reviewPane = (
		<WorkspaceReviewPane
			annotation={annotation}
			target={review.target}
			onRetry={review.retryTarget}
			hostId={hostId}
			filter={filter}
			onBrowseAll={() => handleViewChange(false)}
			// Maximized, the overlay already fills the window, so the review opens
			// files in place and has no "open diff in center" action.
			canOpenInCenter={Boolean(onOpenFile) && !isMaximized}
			onOpenFile={(path, options) => {
				if (isMaximized) openInMaximizedPreview(path, options);
				else onOpenFile?.(path, options);
			}}
			onIncludeCommitsBetween={review.includeCommitsBetween}
			onShowAllCommits={review.showAllCommits}
			sessionId={sessionId}
			split={split}
			collapseRequest={collapseRequest}
			onAllCollapsedChange={setReviewAllCollapsed}
			scrollRequest={reviewScrollRequest}
		/>
	);

	// Maximized, the overlay's titlebar band carries the header too: the
	// pickers at its start, the filter centred, the view controls and minimize
	// at its end, so the review needs no row of its own.
	const headerInTitlebar = isMaximized && Boolean(filesTopbarHost) && !filesActionsHost;
	const headerInFrame = showHeader && !headerInTitlebar;

	// What to review, then which branch: the changes picker truncates last,
	// the branch picker first. -ml-1.5 cancels the first trigger's own 6px
	// inline padding so its label starts on the 12px gutter.
	const pickers = showChangesTrigger || branchMenu ? (
		<div className="-ml-1.5 flex min-w-0 shrink items-center gap-1">
			{showChangesTrigger ? <ReviewChangesMenu {...review.changesMenu} /> : null}
			{branchMenu ? <ReviewBranchMenu {...branchMenu} /> : null}
		</div>
	) : null;

	const viewControls = (
		<>
			{viewMenu}
			{hasViewTabs ? (
				<div aria-label={t("files.viewMode")} className="flex shrink-0 items-center gap-1" role="tablist">
					<Tooltip>
						<TooltipTrigger asChild>
							<button
								aria-label={t("files.reviewChanges")}
								aria-selected={showChanges}
								className={cn(viewTabClass, "w-control-md justify-center px-0", showChanges ? "bg-interactive-active text-foreground" : "text-muted-foreground hover:bg-interactive-hover hover:text-foreground")}
								onClick={() => handleViewChange(true)}
								role="tab"
								type="button"
							>
								<GitCompareArrows aria-hidden="true" className="size-icon-base" />
							</button>
						</TooltipTrigger>
						<TooltipContent side="bottom">{t("files.reviewChanges")}</TooltipContent>
					</Tooltip>
					<Tooltip>
						<TooltipTrigger asChild>
							<button
								aria-label={allFilesTabLabel}
								aria-selected={!showChanges}
								className={cn(viewTabClass, "w-control-md justify-center px-0", !showChanges ? "bg-interactive-active text-foreground" : "text-muted-foreground hover:bg-interactive-hover hover:text-foreground")}
								onClick={() => (!showChanges && splitView ? setTreeOpen((open) => !open) : handleViewChange(false))}
								role="tab"
								type="button"
							>
								<Folders aria-hidden="true" className="size-icon-base" />
							</button>
						</TooltipTrigger>
						<TooltipContent side="bottom">{allFilesTabLabel}</TooltipContent>
					</Tooltip>
				</div>
			) : null}
			{showTreeToggle ? (
				<Tooltip>
					<TooltipTrigger asChild>
						<Button
							aria-label={treeOpen ? t("files.hideFileTree") : t("files.showFileTree")}
							aria-pressed={treeOpen}
							className="shrink-0"
							onClick={() => setTreeOpen((open) => !open)}
							size="icon-sm"
							type="button"
							variant="ghost"
						>
							<FolderTree className="size-icon-base" aria-hidden="true" />
						</Button>
					</TooltipTrigger>
					<TooltipContent side="bottom">{treeOpen ? t("files.hideFileTree") : t("files.showFileTree")}</TooltipContent>
				</Tooltip>
			) : null}
		</>
	);

	return (
		<section className="relative flex h-full min-h-0 flex-col bg-background text-foreground" aria-label={t("files.sessionFiles")}>
			{/* The inspector top bar or the maximized overlay's titlebar hosts the
			    filter, and maximize sits at the top bar's trailing edge; the
			    overlay's titlebar also takes the pickers and view controls. Without
			    hosts they all stay in this header. */}
			{filesTopbarHost ? createPortal(filterField, filesTopbarHost) : null}
			{headerInTitlebar && filesTopbarHost ? (
				<>
					{createPortal(<div className="files-popout-titlebar__start">{showHeader ? pickers : null}</div>, filesTopbarHost)}
					{createPortal(<div className="files-popout-titlebar__end flex shrink-0 items-center gap-1 pr-2">{showHeader ? viewControls : null}{maximizeButton}</div>, filesTopbarHost)}
				</>
			) : filesActionsHost && maximizeButton ? createPortal(maximizeButton, filesActionsHost)
				: filesTopbarHost && maximizeButton ? createPortal(<div className="ml-auto flex shrink-0 items-center pr-2">{maximizeButton}</div>, filesTopbarHost)
				: null}
			{headerInFrame ? (
				/* The header is a size container so the pickers can drop their icons
				   and stats as it narrows. Its trailing controls sit 4px from the right
				   edge with 4px gaps, like the pinned top-bar buttons above them. */
				<header className="@container/files-header flex shrink-0 items-center gap-1 pb-3 pl-3 pr-1 pt-2">
					{pickers}
					{filesTopbarHost ? null : <span aria-hidden="true" className="flex-1" />}
					{filesTopbarHost ? null : filterField}
					<span aria-hidden="true" className="flex-1" />
					{viewControls}
					{filesActionsHost || filesTopbarHost ? null : maximizeButton}
				</header>
			) : null}
			{/* The branch already shows in its picker; this row only appears to
			    explain an automatic fall back to the checked-out branch. */}
			{review.notice ? (
				<p className="shrink-0 border-b border-border px-3 py-1 text-2xs text-muted-foreground" role="status">
					{review.notice}
				</p>
			) : null}
   {!showChanges && source.kind === "snapshot" && review.target.state === "pending" ? <PanelMessage>{t("files.loading")}</PanelMessage> : showChanges ? (
				filesQuery.isPending ? (
					<PanelMessage>{t("files.loading")}</PanelMessage>
				) : filesQuery.isError ? (
					<PanelMessage action={<RetryButton onClick={() => void filesQuery.refetch()} />}>
						{filesQuery.error.message || t("files.error.loadWorkspace")}
					</PanelMessage>
				) : filesData ? (
					isMaximized ? (
						// The review keeps its panel whether or not the rail shows, so a
						// filter that empties the rail doesn't remount it (and lose its
						// collapsed and loaded files), as the tree beside the preview does.
						<ResizablePanelGroup className={cn("min-h-0 flex-1", headerInFrame && "border-t border-border")}>
							<ResizablePanel defaultSize="74%" id="files-review" minSize="40%">
								{reviewPane}
							</ResizablePanel>
							{reviewRailShown && reviewFilterMatches ? (
								<>
									<ResizableHandle />
									<ResizablePanel defaultSize="26%" id="files-review-rail" maxSize="50%" minSize="18%">
										{reviewFiles ? (
											<FileTree
												changedOnly
												changedOnlyData={reviewTreeData}
												expandChanged
												filterText={filter}
												onSelectPath={selectReviewTreePath}
												selectedPath={reviewTreePath}
												sessionId={sessionId}
											/>
										) : review.target.state === "pending" ? (
											<FileTreeSkeleton />
										) : null}
									</ResizablePanel>
								</>
							) : null}
						</ResizablePanelGroup>
					) : reviewPane
				) : null
			) : splitView ? (
				// Preview on the left, tree on the right (collapsible from the header),
				// like an editor's changed-files rail.
				<ResizablePanelGroup className={cn("min-h-0 flex-1", headerInFrame && "border-t border-border")}>
					<ResizablePanel defaultSize="74%" minSize="40%">
						<ContentScrollArea>
							<FileContentPane onOpenInCenter={onOpenFile && selectedPath ? openPreviewInCenter : undefined} annotation={annotation} commitSha={previewRequest?.commitSha} hostId={hostId} initialEditing={previewRequest?.editing ?? false} initialMode={previewRequest?.mode} initialRequestKey={previewRequest?.key ?? 0} path={selectedPath} previousPath={selectedPreviousPath} scope={previewRequest?.scope} sessionId={sessionId} source={previewRequest?.source ?? source} split={split} toolbar="compact" />
						</ContentScrollArea>
					</ResizablePanel>
					{treeShown ? (
						<>
							<ResizableHandle />
							<ResizablePanel defaultSize="26%" minSize="18%" maxSize="50%">
								<FileTree
 source={source}
									hostId={hostId}
									changedOnly={false}
									changedOnlyData={changedOnlyData}
									filterText={filter}
									onSelectPath={handleSelectPath}
									onPrefetchPath={prefetchPath}
									selectedPath={treeSelectedPath}
									sessionId={sessionId}
								/>
							</ResizablePanel>
						</>
					) : null}
				</ResizablePanelGroup>
			) : (
				// The right rail remains a persistent navigator. File contents open
				// in center tabs so expanding folders and scrolling the tree survive.
				<FileTree
 source={source}
					hostId={hostId}
					changedOnly={false}
					changedOnlyData={changedOnlyData}
					filterText={filter}
					onSelectPath={handleSelectPath}
					onPrefetchPath={prefetchPath}
					selectedPath={treeSelectedPath}
					sessionId={sessionId}
				/>
			)}
		</section>
	);
}

function ContentScrollArea({ children }: { children: ReactNode }) {
	return (
		<div
			className="board-scrollbar h-full min-h-0 flex-1 overflow-x-hidden overflow-y-auto overscroll-contain bg-background"
			data-files-scroll-root=""
		>
			<div className="flex h-full min-h-0 w-full flex-col px-0">{children}</div>
		</div>
	);
}
