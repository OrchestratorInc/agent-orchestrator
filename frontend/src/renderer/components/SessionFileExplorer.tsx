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
	MoreHorizontal,
	Search,
} from "lucide-react";
import {
	sessionWorkspaceFileQueryOptions,
	sessionWorkspaceFilesQueryOptions,
	type FilesBranchTarget,
	type FilesSource,
	type WorkspaceFilesResponse,
	useWorkspaceFileConnectionState,
	workspaceFilesRefetchInterval,
} from "../hooks/useSessionWorkspaceFiles";
import { useFilesReviewSelection } from "../hooks/useFilesReviewSelection";
import { subscribeWorkspaceFileChanges } from "../lib/workspace-file-events";
import { buildChangedOnlyTree, sessionWorkspaceTreeQueryOptions, type TreeNode } from "../hooks/useSessionWorkspaceTree";
import { useFileAnnotation } from "../hooks/useFileAnnotation";
import { useUiStore } from "../stores/ui-store";
import { cn } from "../lib/utils";
import { Button } from "./ui/button";
import { Input } from "./ui/input";
import { Tooltip, TooltipContent, TooltipTrigger } from "./ui/tooltip";
import { ResizableHandle, ResizablePanel, ResizablePanelGroup } from "./ui/resizable";
import {
	DropdownMenu,
	DropdownMenuContent,
	DropdownMenuItem,
	DropdownMenuRadioGroup,
	DropdownMenuRadioItem,
	DropdownMenuSub,
	DropdownMenuSubContent,
	DropdownMenuSubTrigger,
	DropdownMenuTrigger,
} from "./ui/dropdown-menu";
import { useFilesActionsHost, useFilesTopbarHost } from "./files-topbar-host";
import { FileTree } from "./FileTree";
import { FileContentPane, type FileOpenOptions } from "./FileContentPane";
import { PanelMessage, RetryButton } from "./WorkspaceDiffView";
import { WorkspaceReviewPane } from "./diffs/WorkspaceReviewPane";
import { ReviewChangesMenu } from "./diffs/ReviewChangesMenu";
import { ReviewBranchMenu } from "./diffs/ReviewBranchMenu";
import { markFileViewerPerformance } from "../lib/file-viewer-performance";
import { sessionUiKey } from "../lib/hosts";
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
	split: controlledSplit,
}: SessionFileExplorerProps) {
	const { t } = useTranslation();
	const [filter, setFilter] = useState("");
	const [internalSplit, setInternalSplit] = useState(() => window.localStorage.getItem("ao.files.diffStyle") === "split");
	const split = controlledSplit ?? internalSplit;
	const [selectedPath, setSelectedPath] = useState<string | null>(null);
	// Maximized, the review pane's edit/preview actions open the file in this
	// view's own preview (the center pane is hidden behind the overlay).
	const [previewRequest, setPreviewRequest] = useState<(FileOpenOptions & { key: number }) | null>(null);
	const filesTopbarHost = useFilesTopbarHost();
	const filesActionsHost = useFilesActionsHost();
	// The review owns which files are collapsed; the ⋯ menu asks it to collapse
	// or expand them all and reads back which of the two to offer.
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
		// A preview of the old branch's file would be stale.
		onBranchChange: () => {
			setPreviewRequest(null);
			setSelectedPath(null);
		},
	});
	const branch = review.target.branch;
	// Another branch or a PR only has the review: the file tree, the centre
	// pane and file previews all read the worktree.
	const onOtherBranch = branch.kind !== "checked_out";
	const annotation = useFileAnnotation(sessionId, { hostId, source: annotationSource(branch) });
	const changedOnlyData = useMemo(
		() => (filesData ? buildChangedOnlyTree(filesData.files) : []),
		[filesData],
	);
	const hasChanges = filesData ? hasReviewableChanges(filesData) : false;
	const showChanges = onOtherBranch || (changedOnly && (!filesData || hasChanges));
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
				? queryClient.prefetchQuery({ ...sessionWorkspaceTreeQueryOptions(sessionId, node.path, undefined, hostId), staleTime: 5_000 })
				: queryClient.prefetchQuery({ ...sessionWorkspaceFileQueryOptions(sessionId, node.path, undefined, "combined", undefined, hostId), staleTime: 5_000 });
			void request.finally(() => { prefetchBusy.current = false; });
		}, 100);
	}, [queryClient, sessionId, hostId]);

	const handleSelectPath = (node: TreeNode) => {
		setPreviewRequest(null);
		setSelectedPath(node.path);
		if (!isMaximized) onOpenFile?.(node.path, { mode: "file" });
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
	const treeSelectedPath = selectedPath;
	const selectedPreviousPath = filesData?.files.find((file) => file.path === selectedPath)?.previousPath;

	// The file tree only shows the worktree, so another branch has no Files view to switch to.
	const hasViewTabs = hasChanges && !onOtherBranch;
	const showTreeToggle = splitView && !hasViewTabs;
	// With the filter and actions in a top bar, the header holds only the pickers
	// and view tabs, so it is dropped when there are none. The pickers wait for
	// the manifest, so until it arrives the row is kept and nothing jumps.
	const setLayout = (next: boolean) => {
		if (controlledSplit === undefined) setInternalSplit(next);
		onSplitChange?.(next);
	};
	// Unified/split applies wherever a diff shows: the Changes review and the
	// preview beside the tree.
	const showLayout = showChanges || splitView;
	const showCollapse = showChanges && Boolean(filesData);
	// With the filter and maximize in a top bar, the header holds only the
	// pickers, the ⋯ menu and the view tabs, so it is dropped when there are
	// none. The pickers wait for the manifest, so until it arrives the row is
	// kept and nothing jumps.
	const showHeader = !filesTopbarHost || !filesData || showChangesTrigger || Boolean(branchMenu) || showLayout || showCollapse || hasViewTabs || showTreeToggle;
	// In the preview + tree split the (already active) All-files tab doubles as
	// the tree toggle, so the header doesn't carry two file-tree buttons.
	const allFilesTabLabel = !showChanges && splitView ? (treeOpen ? t("files.hideFileTree") : t("files.showFileTree")) : t("files.allFiles");

	const filterField = (
		<label className={cn("relative min-w-0", filesTopbarHost ? "block w-full" : "w-64 shrink")}>
			<Search className="pointer-events-none absolute left-2.5 top-1/2 size-icon-sm -translate-y-1/2 text-passive" />
			<Input
				aria-label={t("files.explorer.filter")}
				className="inspector-field-input pl-8"
				onChange={(event) => setFilter(event.target.value)}
				placeholder={t("files.explorer.filterPlaceholder")}
				value={filter}
			/>
		</label>
	);

	const viewMenu = showLayout || showCollapse ? (
		<DropdownMenu>
			<Tooltip>
				<TooltipTrigger asChild>
					<DropdownMenuTrigger asChild>
						<Button aria-label={t("files.viewOptions")} className="shrink-0 data-[state=open]:bg-interactive-active" size="icon-sm" type="button" variant="ghost">
							<MoreHorizontal className="size-icon-base" aria-hidden="true" />
						</Button>
					</DropdownMenuTrigger>
				</TooltipTrigger>
				<TooltipContent side="bottom">{t("files.viewOptions")}</TooltipContent>
			</Tooltip>
			<DropdownMenuContent align="end" className="w-56">
				{showLayout ? (
					<DropdownMenuSub>
						<DropdownMenuSubTrigger>
							<span className="min-w-0 flex-1 truncate">{t("files.layout")}</span>
							<span className="shrink-0 text-caption text-passive">{split ? t("files.layoutSplit") : t("files.layoutUnified")}</span>
						</DropdownMenuSubTrigger>
						<DropdownMenuSubContent className="w-40">
							<DropdownMenuRadioGroup onValueChange={(value) => setLayout(value === "split")} value={split ? "split" : "unified"}>
								<DropdownMenuRadioItem value="unified">{t("files.layoutUnified")}</DropdownMenuRadioItem>
								<DropdownMenuRadioItem value="split">{t("files.layoutSplit")}</DropdownMenuRadioItem>
							</DropdownMenuRadioGroup>
						</DropdownMenuSubContent>
					</DropdownMenuSub>
				) : null}
				{showCollapse ? (
					<DropdownMenuItem onSelect={() => setCollapseRequest((current) => ({ expand: reviewAllCollapsed, key: (current?.key ?? 0) + 1 }))}>
						{reviewAllCollapsed ? t("files.expandAll") : t("files.collapseAll")}
					</DropdownMenuItem>
				) : null}
			</DropdownMenuContent>
		</DropdownMenu>
	) : null;
	const maximizeButton = onToggleMaximized ? (
		<Tooltip>
			<TooltipTrigger asChild>
				{/* Sized and toned like the pinned top-bar buttons beside it. */}
				<Button
					aria-label={isMaximized ? t("files.minimize") : t("files.maximize")}
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
			<TooltipContent side="bottom">{isMaximized ? t("files.minimize") : t("files.maximize")}</TooltipContent>
		</Tooltip>
	) : null;

	return (
		<section className="relative flex h-full min-h-0 flex-col bg-background text-foreground" aria-label={t("files.sessionFiles")}>
			{/* The inspector top bar or the maximized overlay's titlebar hosts the
			    filter, and maximize sits at the top bar's trailing edge; without
			    hosts they stay in this header. */}
			{filesTopbarHost ? createPortal(filterField, filesTopbarHost) : null}
			{filesActionsHost && maximizeButton ? createPortal(maximizeButton, filesActionsHost)
				: filesTopbarHost && maximizeButton ? createPortal(<div className="ml-auto flex shrink-0 items-center pr-2">{maximizeButton}</div>, filesTopbarHost)
				: null}
			{showHeader ? (
				/* The header is a size container so the pickers can drop their icons
				   and stats as it narrows. Its trailing controls sit 4px from the right
				   edge with 4px gaps, like the pinned top-bar buttons above them. */
				<header className="@container/files-header flex shrink-0 items-center gap-1 pb-3 pl-3 pr-1 pt-2">
					{/* What to review, then which branch: the changes picker truncates
					    last, the branch picker first. -ml-2 cancels the first trigger's
					    own 8px inline padding so its label starts on the 12px gutter. */}
					{showChangesTrigger || branchMenu ? (
						<div className="-ml-2 flex min-w-0 shrink items-center gap-1">
							{showChangesTrigger ? <ReviewChangesMenu {...review.changesMenu} /> : null}
							{branchMenu ? <ReviewBranchMenu {...branchMenu} /> : null}
						</div>
					) : null}
					{filesTopbarHost ? null : <span aria-hidden="true" className="flex-1" />}
					{filesTopbarHost ? null : filterField}
					<span aria-hidden="true" className="flex-1" />
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
			{showChanges ? (
				filesQuery.isPending ? (
					<PanelMessage>{t("files.loading")}</PanelMessage>
				) : filesQuery.isError ? (
					<PanelMessage action={<RetryButton onClick={() => void filesQuery.refetch()} />}>
						{filesQuery.error.message || t("files.error.loadWorkspace")}
					</PanelMessage>
				) : filesData ? (
					<WorkspaceReviewPane
						annotation={annotation}
						target={review.target}
						onRetry={review.retryTarget}
						hostId={hostId}
						filter={filter}
						onBrowseAll={onOtherBranch ? undefined : () => handleViewChange(false)}
						canOpenInCenter={!isMaximized && Boolean(onOpenFile)}
						onOpenFile={isMaximized ? openInMaximizedPreview : onOpenFile}
						onIncludeCommitsBetween={review.includeCommitsBetween}
						onShowAllCommits={review.showAllCommits}
						sessionId={sessionId}
						split={split}
						collapseRequest={collapseRequest}
						onAllCollapsedChange={setReviewAllCollapsed}
					/>
				) : null
			) : splitView ? (
				// Preview on the left, tree on the right (collapsible from the header),
				// like an editor's changed-files rail.
				<ResizablePanelGroup className="min-h-0 flex-1 border-t border-border">
					<ResizablePanel defaultSize="74%" minSize="40%">
						<ContentScrollArea>
							<FileContentPane annotation={annotation} commitSha={previewRequest?.commitSha} hostId={hostId} initialEditing={previewRequest?.editing ?? false} initialMode={previewRequest?.mode} initialRequestKey={previewRequest?.key ?? 0} path={selectedPath} previousPath={selectedPreviousPath} scope={previewRequest?.scope} sessionId={sessionId} source={WORKSPACE_SOURCE} split={split} toolbar="compact" />
						</ContentScrollArea>
					</ResizablePanel>
					{treeOpen ? (
						<>
							<ResizableHandle />
							<ResizablePanel defaultSize="26%" minSize="18%" maxSize="50%">
								<FileTree
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
			<div className="flex w-full flex-col px-0">{children}</div>
		</div>
	);
}
