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
} from "@/components/app-icons";
import {
	sessionSourceFileQueryOptions,
	sessionWorkspaceFilesQueryOptions,
	clearMissingWorkspace,
	isMissingWorkspaceError,
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

type SessionFileExplorerProps = {
	sessionId: string;
	hostId?: string;
	isMaximized?: boolean;
	onOpenFile?: (path: string, options?: FileOpenOptions) => void;
	onSplitChange?: (split: boolean) => void;
	onToggleMaximized?: (next: boolean) => void;
	projectKind?: ProjectKind;
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
	const [previewRequest, setPreviewRequest] = useState<(FileOpenOptions & { key: number }) | null>(null);
	const filesTopbarHost = useFilesTopbarHost();
	const filesActionsHost = useFilesActionsHost();
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
		refetchInterval: (query) => isMissingWorkspaceError(query.state.error)
			? false : workspaceFilesRefetchInterval(connectionState, Boolean(query.state.data?.degraded)),
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
		onBranchChange: () => {
			setPreviewRequest(null);
			setSelectedPath(null);
		},
	});
	const branch = review.target.branch;
 const filterKey=`${uiKey}:${branchKey(branch)}`;
 useEffect(()=>setFilter(filtersBySource.current.get(filterKey) ?? ""),[filterKey]);
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

	const hasViewTabs = hasChanges || onOtherBranch;
	const setLayout = (next: boolean) => {
		if (controlledSplit === undefined) setInternalSplit(next);
		onSplitChange?.(next);
	};
	const reviewHasFiles = review.target.state === "pending" || (review.target.files?.length ?? 0) > 0;
	const previewHasDiff = splitView && selectedPath !== null
		&& (source.kind === "snapshot" || Boolean(filesData?.files.some((file) => file.path === selectedPath && file.status !== "unmodified")));
	const showLayout = showChanges ? reviewHasFiles : previewHasDiff;
	const showCollapse = showChanges && Boolean(filesData) && reviewHasFiles;
	const showHeader = !filesTopbarHost || !filesData || hasViewTabs || review.hasPullRequest !== false;
	const showTreeToggle = showHeader && splitView && !hasViewTabs;
	const treeShown = treeOpen || !showHeader;
	const reviewFiles = review.target.files;
	const reviewTreeData = useMemo(() => buildWorkspaceFileTree(reviewFiles ?? []), [reviewFiles]);
	const [reviewRailShown, setReviewRailShown] = useState(() => (reviewFiles?.length ?? 0) > 0);
	useLayoutEffect(() => {
		if (review.target.state !== "pending") setReviewRailShown((reviewFiles?.length ?? 0) > 0);
	}, [review.target.state, reviewFiles]);
	const reviewFilter = filter.trim().toLowerCase();
	const reviewFilterMatches = !reviewFilter || (reviewFiles ?? []).some((file) => `${file.path} ${file.previousPath ?? ""}`.toLowerCase().includes(reviewFilter));
	const [reviewTreePath, setReviewTreePath] = useState<string | null>(null);
	const [reviewScrollRequest, setReviewScrollRequest] = useState<{ path: string; key: number }>();
	const selectReviewTreePath = (node: TreeNode) => {
		setReviewTreePath(node.path);
		setReviewScrollRequest((current) => ({ path: node.path, key: (current?.key ?? 0) + 1 }));
	};
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
			<TooltipTrigger asChild onFocus={(event) => { if (!event.currentTarget.matches(":focus-visible")) event.preventDefault(); }}>
				<Button
					aria-label={isMaximized ? t("common.returnToPanel") : t("common.maximize")}
					className="shrink-0"
					onClick={() => onToggleMaximized(!isMaximized)}
					size="icon-sm"
					type="button"
					variant="quiet"
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

	const headerInTitlebar = isMaximized && Boolean(filesTopbarHost) && !filesActionsHost;
	const headerInFrame = showHeader && !headerInTitlebar;

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
							<Button
								aria-label={t("files.reviewChanges")}
								aria-selected={showChanges} data-active={showChanges} role="tab"
								onClick={() => handleViewChange(true)}
								size="icon-sm"
								type="button"
								variant="quiet"
							>
								<GitCompareArrows aria-hidden="true" className="size-icon-base" />
							</Button>
						</TooltipTrigger>
						<TooltipContent side="bottom">{t("files.reviewChanges")}</TooltipContent>
					</Tooltip>
					<Tooltip>
						<TooltipTrigger asChild>
							<Button
								aria-label={allFilesTabLabel}
								aria-selected={!showChanges} data-active={!showChanges} role="tab"
								onClick={() => (!showChanges && splitView ? setTreeOpen((open) => !open) : handleViewChange(false))}
								size="icon-sm"
								type="button"
								variant="quiet"
							>
								<Folders aria-hidden="true" className="size-icon-base" />
							</Button>
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
				<header className="@container/files-header flex shrink-0 items-center gap-1 pb-3 pl-3 pr-1 pt-2">
					{pickers}
					{filesTopbarHost ? null : <span aria-hidden="true" className="flex-1" />}
					{filesTopbarHost ? null : filterField}
					<span aria-hidden="true" className="flex-1" />
					{viewControls}
					{filesActionsHost || filesTopbarHost ? null : maximizeButton}
				</header>
			) : null}
			{review.notice ? (
				<p className="shrink-0 border-b border-border px-3 py-1 text-2xs text-muted-foreground" role="status">
					{review.notice}
				</p>
			) : null}
   {!showChanges && source.kind === "snapshot" && review.target.state === "pending" ? <PanelMessage>{t("files.loading")}</PanelMessage> : showChanges ? (
				filesQuery.isPending ? (
					<PanelMessage>{t("files.loading")}</PanelMessage>
				) : filesQuery.isError ? (
					<PanelMessage action={<RetryButton onClick={() => {
						clearMissingWorkspace(sessionId, hostId);
						void filesQuery.refetch();
					}} />}>
						{isMissingWorkspaceError(filesQuery.error)
							? t("files.error.workspaceMissing") : filesQuery.error.message || t("files.error.loadWorkspace")}
					</PanelMessage>
				) : filesData ? (
					isMaximized ? (
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
