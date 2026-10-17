import { useCallback, useEffect, useState } from "react";
import { createPortal } from "react-dom";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { Eye, FileCode2, GitCompareArrows, LoaderCircle, MessageSquarePlus, Pencil, Save, X } from "lucide-react";
import { Editor, type EditorFactory } from "@pierre/diffs/edit";
import { EditProvider } from "@pierre/diffs/react";
import {
	sessionArtifactFileQueryOptions,
	sessionWorkspaceFileQueryKey,
	sessionSourceFileQueryOptions,
	sessionSourceFileRevisionQueryOptions,
	updateSessionArtifactFile,
	updateSessionWorkspaceFile,
	type WorkspaceDiffScope,
	type WorkspaceFileDetail,
	type FilesSource,
} from "../hooks/useSessionWorkspaceFiles";
import { usePierreFileHighlightReady } from "../hooks/usePierreFileHighlight";
import { workspaceQueryKeyForHost } from "../hooks/useWorkspaceQuery";
import { subscribeWorkspaceFileChanges } from "../lib/workspace-file-events";
import { sessionUiKey } from "../lib/hosts";
import { cn } from "../lib/utils";
import { markFileViewerPerformance } from "../lib/file-viewer-performance";
import { rememberedFileDisplayMode, useUiStore, type FileDisplayMode } from "../stores/ui-store";
import { isAbsoluteFileReference } from "../lib/workspace-file-path";
import { statusLabel, statusTone } from "../lib/workspace-file-status";
import {
	canSplitCompare,
	cancelFileAnnotations,
	FileAnnotationComposer,
	FileAnnotationSendBar,
	PanelMessage,
	ReviewDiffBody,
	RetryButton,
	type FileAnnotationModel,
} from "./WorkspaceDiffView";
import { ReadOnlyFileView } from "./ReadOnlyFileView";
import { WorkspaceEntryIcon } from "./WorkspaceEntryIcon";
import { AoDiffFile } from "./diffs/AoDiffFile";
import { AO_PIERRE_FILES_REVIEW_CSS } from "./diffs/pierreTheme";
import { Button } from "./ui/button";
import { Tooltip, TooltipContent, TooltipTrigger } from "./ui/tooltip";
import { MarkdownFileView } from "./markdown/MarkdownFileView";
import { MarkdownEditor } from "./MarkdownEditor";
import { SvgFileView } from "./SvgFileView";
import { isSvgPath } from "../lib/image-files";

// Edit-mode Cancel/Save sit beside icon-sm toolbar buttons; keep them the same
// height with small text and icons so they do not dwarf the toolbar.
const EDIT_ACTION_CLASS = "h-6 gap-1 px-2 text-xs";

export type FileViewMode = FileDisplayMode;
export type FileOpenOptions = { commitSha?: string; editing?: boolean; line?: number; mode?: FileViewMode; scope?: WorkspaceDiffScope };

const DEFAULT_FILES_SOURCE: FilesSource = { kind: "workspace" };

const createReviewEditor: EditorFactory<"feedback", undefined> = (editorType, options, editStateKey) =>
	new Editor(editorType, options, editStateKey);

function canRenderMarkdown(path: string, detail: WorkspaceFileDetail): boolean {
	return !detail.deleted && !detail.binary && !detail.contentTruncated && /\.(md|markdown)$/i.test(path);
}

function canRenderSvg(path: string, detail: WorkspaceFileDetail): boolean {
	return !detail.deleted && !detail.binary && !detail.contentTruncated && isSvgPath(path);
}

export function FileContentPane({
	annotation,
	initialEditing = false,
	initialLine,
	initialMode = "diff",
	initialRequestKey = 0,
	commitSha,
	onDirtyChange,
	onInitialLineConsumed,
	onContentReady,
	path,
	previousPath,
	rememberDisplayMode = false,
	sessionId,
	hostId,
	split,
	scope = "combined",
	source = DEFAULT_FILES_SOURCE,
	toolbarHost,
}: {
	annotation: FileAnnotationModel;
	initialEditing?: boolean;
	initialLine?: number;
	initialMode?: FileViewMode;
	initialRequestKey?: number;
	commitSha?: string;
	onDirtyChange?: (dirty: boolean) => void;
	onInitialLineConsumed?: (requestKey: number) => void;
	onContentReady?: () => void;
	path: string | null;
	previousPath?: string;
	/** Restore the display mode picked in the toolbar when this pane remounts (centre file tabs). */
	rememberDisplayMode?: boolean;
	sessionId: string;
	hostId?: string;
	split: boolean;
	scope?: WorkspaceDiffScope;
	source?: FilesSource;
	/**
	 * A header the host renders, with the file name, for the mode and action
	 * buttons. When set, the pane renders those buttons into it and drops its own
	 * path row; null renders none until the host mounts.
	 */
	toolbarHost?: HTMLElement | null;
	/**
	 * @deprecated Ignored: centre file tabs and the Files panel now share the
	 * compact toolbar (breadcrumb + status/counts, icon mode switches).
	 */
	toolbar?: "tabs" | "compact";
}) {
	const { t } = useTranslation();
	const queryClient = useQueryClient();
	const setFileDisplayMode = useUiStore((state) => state.setFileDisplayMode);
	const uiSessionId = sessionUiKey(sessionId, hostId);
	// A mode picked in the toolbar outlives the pane: switching sessions
	// unmounts it, and coming back should not drop Rich preview.
	const restoredMode = () =>
		(rememberDisplayMode ? rememberedFileDisplayMode(useUiStore.getState(), uiSessionId, path, initialRequestKey) : undefined) ?? initialMode;
	const [mode, setMode] = useState<FileViewMode>(restoredMode);
	// An artifact has one view: its initial mode, which editing replaces until
	// the edit is saved or cancelled. No display mode switch.
	const singleView = source.kind === "artifact";
	const [editing, setEditing] = useState(false);
	const [draft, setDraft] = useState("");
	const [saving, setSaving] = useState(false);
	const [saveError, setSaveError] = useState("");
	// Warm syntax highlighting without withholding readable file contents.
	const sourceHighlightReady = usePierreFileHighlightReady(path);
	useEffect(() => {
		if (!path || source.kind !== "workspace" || commitSha) return;
		return subscribeWorkspaceFileChanges(sessionId, queryClient, hostId);
	}, [path, source.kind, commitSha, sessionId, queryClient, hostId]);
	// A background refetch mid-selection would re-render the pane out from under
	// an active native text selection.
	const [selectionOrMenuActive, setSelectionOrMenuActive] = useState(false);
	// The daemon serves workspace files by relative path only. An absolute path
	// that reaches here was not inside the session, so say that rather than
	// sending a request that can only fail.
	const outsideWorkspace = source.kind === "workspace" && isAbsoluteFileReference(path ?? "");
	const query = useQuery({
		...sessionSourceFileQueryOptions(sessionId, source, path ?? "", t(source.kind === "artifact" ? "files.error.loadArtifact" : "files.error.loadWorkspaceFile"), scope, commitSha, previousPath, hostId),
		refetchOnWindowFocus: true,
		enabled: Boolean(path) && !outsideWorkspace && !selectionOrMenuActive,
	});
	const canReadLiveText = Boolean(path) && !outsideWorkspace && mode === "file" && !editing && !initialEditing && !selectionOrMenuActive && source.kind === "workspace" && !commitSha && (scope === "combined" || scope === "unstaged" || scope === "untracked");
	const liveText = useQuery({
		...sessionSourceFileRevisionQueryOptions({ path: path ?? "", scope, sessionId, hostId, side: "after", source }),
		enabled: canReadLiveText,
		staleTime: 5_000,
		refetchOnWindowFocus: true,
	});

	const hasUnsavedChanges = Boolean(editing && query.data && draft !== query.data.content);
	useEffect(() => {
		if (path) markFileViewerPerformance("file-selected");
	}, [path]);
	useEffect(() => {
		if (!query.data) return;
		markFileViewerPerformance("file-header-painted");
		markFileViewerPerformance("file-content-painted");
	}, [query.data]);
	useEffect(() => {
		if (query.data && sourceHighlightReady) onContentReady?.();
	}, [onContentReady, query.data, sourceHighlightReady]);
	useEffect(() => {
		setMode(restoredMode());
		setEditing(initialEditing);
		setDraft("");
		setSaveError("");
	}, [commitSha, hostId, initialEditing, initialMode, initialRequestKey, path, rememberDisplayMode, scope, sessionId, source, uiSessionId]);
	const selectMode = (next: FileViewMode) => {
		setMode(next);
		if (rememberDisplayMode && path) setFileDisplayMode(uiSessionId, path, next, initialRequestKey);
	};
	useEffect(() => {
		if (initialEditing && query.data) setDraft(query.data.content);
	}, [initialEditing, initialRequestKey, path, query.data]);
	useEffect(() => {
		onDirtyChange?.(hasUnsavedChanges);
	}, [hasUnsavedChanges, onDirtyChange]);
	useEffect(() => () => onDirtyChange?.(false), [onDirtyChange]);
	const saveEditing = useCallback(async () => {
		const detail = query.data;
		if (!path || !detail?.fileFingerprint || saving) return;
		setSaving(true);
		setSaveError("");
		try {
			if (source.kind === "artifact") {
				const saved = await updateSessionArtifactFile({
					content: draft,
					expectedFileFingerprint: detail.fileFingerprint,
					path,
					sessionId,
					hostId,
				});
				queryClient.setQueryData(sessionArtifactFileQueryOptions(sessionId, path, source.rawUrl).queryKey, {
					...detail,
					content: draft,
					fileFingerprint: saved.fileFingerprint,
					size: saved.size,
				});
				// The session's artifact list carries each file's modified time.
				await queryClient.invalidateQueries({ queryKey: workspaceQueryKeyForHost(hostId) });
				setEditing(false);
				setDraft("");
				setMode(initialMode);
				return;
			}
			const saved = await updateSessionWorkspaceFile({
				content: draft,
				expectedFileFingerprint: detail.fileFingerprint,
				path,
				sessionId,
				hostId,
			});
			queryClient.setQueryData(sessionWorkspaceFileQueryKey(sessionId, path, scope, commitSha, hostId), saved);
			await queryClient.invalidateQueries({
				predicate: ({ queryKey }) => queryKey[1] === (hostId ?? sessionId)
					&& (!hostId || queryKey[2] === sessionId) && [
					"session-workspace-files",
					"session-workspace-history",
					"session-workspace-tree",
					"session-workspace-search",
					"session-workspace-file-revision",
					"session-workspace-diffs",
					"files-review-end-of-file",
				].includes(String(queryKey[0])),
			});
			setEditing(false);
			setDraft("");
		} catch (error) {
			setSaveError(error instanceof Error ? error.message : t("files.saveError"));
		} finally {
			setSaving(false);
		}
	}, [commitSha, draft, initialMode, path, query.data, queryClient, saving, scope, sessionId, source, hostId, t]);
	useEffect(() => {
		if (!editing) return;
		const onSaveShortcut = (event: KeyboardEvent) => {
			if (
				event.key.toLowerCase() !== "s"
				|| (!event.metaKey && !event.ctrlKey)
				|| event.altKey
				|| event.shiftKey
			) return;
			event.preventDefault();
			if (!saving && hasUnsavedChanges) void saveEditing();
		};
		window.addEventListener("keydown", onSaveShortcut, true);
		return () => window.removeEventListener("keydown", onSaveShortcut, true);
	}, [editing, hasUnsavedChanges, saveEditing, saving]);
	const refetch = query.refetch;

	if (!path) {
		return <PanelMessage>{t("files.explorer.selectFile")}</PanelMessage>;
	}
	if (outsideWorkspace) {
		return <PanelMessage>{t("files.error.outsideWorkspace")}</PanelMessage>;
	}
	// File navigation can paint live text independently of repository status.
	// Editing, annotations and comparison controls wait for their real metadata.
	if (canReadLiveText && !query.data && liveText.data?.exists && !liveText.data.binary && !liveText.data.truncated && mode === "file") {
		const revision = liveText.data;
		const preview: WorkspaceFileDetail = {
			sessionId, path, content: revision.content, size: revision.size, binary: false, deleted: false,
			contentTruncated: false, diff: "", diffTruncated: false, status: "unmodified", additions: 0, deletions: 0,
			fileFingerprint: revision.revision,
		};
		return <div className="flex min-h-0 flex-1 flex-col overflow-auto">
			<div className="sticky top-0 z-20 flex min-h-9 items-center gap-2 border-b border-border bg-background px-3 py-1 text-xs" title={path}>
				<WorkspaceEntryIcon className="size-icon-base" kind="file" name={path.split("/").pop() ?? path} />
				<span className="truncate">{path}</span>
			</div>
			<ReadOnlyFileView annotation={{ ...annotation, begin: () => {} }} detail={preview} sessionId={sessionId} hostId={hostId} scope={scope} onContentReady={onContentReady} revealLine={initialLine ? { line: initialLine, requestKey: initialRequestKey } : undefined} onRevealLineConsumed={onInitialLineConsumed} />
			{query.error ? <PanelMessage action={<RetryButton onClick={() => void refetch()} />}>{query.error.message}</PanelMessage> : null}
		</div>;
	}

	if (query.isPending) {
		return <PanelMessage>{t("files.loadingDiff")}</PanelMessage>;
	}
	if (query.error) {
		return (
			<PanelMessage action={<RetryButton onClick={() => void refetch()} />}>
				{query.error.message || t("files.error.loadFile")}
			</PanelMessage>
		);
	}
	if (!query.data) {
		return (
			<PanelMessage action={<RetryButton onClick={() => void refetch()} />}>
				{t("files.error.loadFile")}
			</PanelMessage>
		);
	}

	const detail = query.data;
	const markdownAvailable = canRenderMarkdown(path, detail);
	const renderedAvailable = markdownAvailable || canRenderSvg(path, detail);
	const hasDisplayModeChoice = !singleView && (detail.status !== "unmodified" || renderedAvailable);
	const fileName = path.split("/").pop() || path;
	const newerLiveText = canReadLiveText && liveText.data?.exists && !liveText.data.binary && !liveText.data.truncated && liveText.dataUpdatedAt >= query.dataUpdatedAt && liveText.data.content !== detail.content;
	const displayDetail = newerLiveText ? { ...detail, content: liveText.data!.content, size: liveText.data!.size, fileFingerprint: liveText.data!.revision } : detail;
	const editable = !newerLiveText && detail.editable && Boolean(detail.fileFingerprint) && !(source.kind === "artifact" && source.readOnly);
	const effectiveMode =
		(detail.status === "unmodified" && mode === "diff") || (mode === "rendered" && !renderedAvailable)
			? "file"
			: mode;
	const fileView = (
		<CompleteFileView
			annotation={newerLiveText ? { ...annotation, begin: () => {} } : annotation}
			detail={displayDetail}
			editing={editing && effectiveMode === "file"}
			revealLine={initialLine ? { line: initialLine, requestKey: initialRequestKey } : undefined}
			onEditChange={setDraft}
			onContentReady={onContentReady}
			onRevealLineConsumed={onInitialLineConsumed}
			scope={scope}
			sessionId={sessionId}
			commitSha={commitSha}
			hostId={hostId}
			source={source}
			markdownEditing={editing && markdownAvailable}
			version={query.dataUpdatedAt}
		/>
	);
	const renderedView = markdownAvailable
		? <MarkdownFileView content={detail.content} filePath={path} hostId={hostId} sessionId={sessionId} artifactOrigin={artifactOriginOf(source)} truncated={detail.contentTruncated} version={query.dataUpdatedAt} />
		: <SvgFileView content={detail.content} path={path} />;
	const beginEditing = () => {
		setMode("file");
		cancelFileAnnotations(annotation, (target) => target.surface !== "review" && target.path === detail.path);
		setDraft(detail.content);
		setSaveError("");
		setEditing(true);
	};
	const cancelEditing = () => {
		setEditing(false);
		setDraft("");
		setSaveError("");
		if (singleView) setMode(initialMode);
	};
	const unsavedIndicator = hasUnsavedChanges && !onDirtyChange ? (
		<span
			aria-hidden="true"
			className="size-2 shrink-0 rounded-full bg-foreground"
			data-testid="unsaved-file-indicator"
		/>
	) : null;
	const wholeFileAnnotationTarget = annotation.targets.find((target) => target.surface !== "review"
		&& target.path === detail.path
		&& target.side === "file"
		&& target.line == null
		&& target.quote == null);
	const compactModeButton = (mode: FileViewMode, label: string, icon: React.ReactNode) => (
		<Tooltip key={mode}>
			<TooltipTrigger asChild>
				<Button
					aria-label={label}
					aria-selected={effectiveMode === mode}
					className={cn("text-muted-foreground hover:text-foreground", effectiveMode === mode && "bg-interactive-active text-foreground")}
					disabled={editing}
					onClick={() => selectMode(mode)}
					role="tab"
					size="icon-sm"
					type="button"
					variant="ghost"
				>
					{icon}
				</Button>
			</TooltipTrigger>
			<TooltipContent side="bottom">{label}</TooltipContent>
		</Tooltip>
	);
	const pathSegments = detail.path.split("/");
	// Mode switches and file actions share one row and one gap.
	const toolbarActions = (
		<div className="flex shrink-0 items-center gap-0.5">
			{hasDisplayModeChoice ? (
				<div aria-label={t("files.fileDisplayMode")} className="flex shrink-0 items-center gap-0.5" role="tablist">
					{detail.status !== "unmodified" ? compactModeButton("diff", t("files.diff"), <GitCompareArrows aria-hidden="true" className="size-icon-sm" />) : null}
					{compactModeButton("file", t("files.fileView"), <FileCode2 aria-hidden="true" className="size-icon-sm" />)}
					{renderedAvailable ? compactModeButton("rendered", t("files.rendered"), <Eye aria-hidden="true" className="size-icon-sm" />) : null}
				</div>
			) : null}
			{editing ? (
				<div className="flex shrink-0 items-center gap-1">
					<Button aria-label={t("files.cancelEditing")} className={EDIT_ACTION_CLASS} disabled={saving} onClick={cancelEditing} size="sm" type="button" variant="ghost"><X aria-hidden="true" className="size-icon-sm" />{t("files.cancelEditing")}</Button>
					<Button aria-label={t("files.saveFile")} className={EDIT_ACTION_CLASS} disabled={saving || !hasUnsavedChanges} onClick={() => void saveEditing()} size="sm" type="button" variant="primary">{saving ? <LoaderCircle aria-hidden="true" className="size-icon-sm animate-spin" /> : <Save aria-hidden="true" className="size-icon-sm" />}{t("files.saveFile")}</Button>
				</div>
			) : (
				<div className="flex shrink-0 items-center gap-0.5">
					{editable ? (
						<Tooltip>
							<TooltipTrigger asChild><Button aria-label={t("files.editFile")} className="text-muted-foreground hover:text-foreground" onClick={beginEditing} size="icon-sm" type="button" variant="ghost"><Pencil aria-hidden="true" className="size-icon-sm" /></Button></TooltipTrigger>
							<TooltipContent side="bottom">{t("files.editFile")}</TooltipContent>
						</Tooltip>
					) : null}
					<Tooltip>
						<TooltipTrigger asChild>
							<Button
								aria-label={t("files.addFeedback")}
								disabled={Boolean(newerLiveText)}
								className="text-muted-foreground hover:text-foreground"
								onClick={() => annotation.begin({ path: detail.path, previousPath: detail.previousPath, side: "file", scope, surface: "focused", workspaceVersion: detail.workspaceVersion, fileFingerprint: detail.fileFingerprint })}
								size="icon-sm"
								type="button"
								variant="ghost"
							>
								<MessageSquarePlus aria-hidden="true" className="size-icon-sm" />
							</Button>
						</TooltipTrigger>
						<TooltipContent side="bottom">{t("files.addFeedback")}</TooltipContent>
					</Tooltip>
				</div>
			)}
		</div>
	);
	const compactTabs = toolbarHost !== undefined ? (toolbarHost ? createPortal(toolbarActions, toolbarHost) : null) : (
		<div className="sticky top-0 z-20 flex min-h-9 items-center gap-2 border-b border-border bg-background px-3 py-1">
			<nav aria-label={t("files.filePath")} className="flex min-w-0 flex-1 items-center gap-1.5" title={detail.path}>
				<WorkspaceEntryIcon className="size-icon-base" kind="file" name={fileName} />
				<span className="flex min-w-0 items-center text-xs">
					{pathSegments.slice(0, -1).map((segment, index) => (
						<span className="flex min-w-0 shrink items-center text-muted-foreground" key={`${index}:${segment}`}>
							<span className="truncate">{segment}</span>
							<span aria-hidden="true" className="px-1 text-passive">/</span>
						</span>
					))}
					<span className="shrink-0 truncate text-foreground">{fileName}</span>
				</span>
				{unsavedIndicator}
				{detail.status !== "unmodified" ? (
					<span className="ml-1.5 flex shrink-0 items-center gap-1.5 text-xs tabular-nums">
						{statusLabel[detail.status] ? <span className={cn("font-semibold", statusTone[detail.status])}>{statusLabel[detail.status]}</span> : null}
						<span className="text-success">+{detail.additions}</span>
						<span className="text-error">−{detail.deletions}</span>
					</span>
				) : null}
			</nav>
			{toolbarActions}
		</div>
	);
	// Both the Files panel and a centre file tab use the compact icon toolbar.
	// Whole-file feedback opens in the page flow right under it, not over the code.
	const toolbarNode = (
		<>
			{compactTabs}
			{wholeFileAnnotationTarget ? <FileAnnotationComposer annotation={annotation} target={wholeFileAnnotationTarget} /> : null}
		</>
	);

	if (detail.status !== "unmodified") {
		const fallback = (
			<ReviewDiffBody
				annotation={annotation}
				detail={detail}
				detailLoadedAt={query.dataUpdatedAt}
				emptyFallback={
					!detail.binary && !detail.contentTruncated && !detail.deleted && detail.content ? (
						<ReadOnlyFileView annotation={annotation} detail={detail} hostId={hostId} scope={scope} sessionId={sessionId} />
					) : undefined
				}
				filePath={path}
				onActiveSelectionChange={setSelectionOrMenuActive}
				sessionId={sessionId}
				hostId={hostId}
				split={split && canSplitCompare(detail.status)}
				wrap
			/>
		);
		return (
			<div className="relative min-w-0">
				{toolbarNode}
				<EditProvider createEditor={createReviewEditor}>
				{effectiveMode === "diff" ? (
					<AoDiffFile
						annotation={annotation}
						detail={detail}
						fallback={fallback}
						onActiveSelectionChange={setSelectionOrMenuActive}
						scope={scope}
						sessionId={sessionId}
						split={split && canSplitCompare(detail.status)}
						commitSha={commitSha}
						hostId={hostId}
						source={source}
						// Files panel preview: the compact toolbar already shows the name
						// and counts, and its filler gutters sit on the canvas.
						extraCSS={AO_PIERRE_FILES_REVIEW_CSS}
						hideFileHeader
					/>
				) : effectiveMode === "rendered" && renderedAvailable ? (
					renderedView
				) : (
					fileView
				)}
				</EditProvider>
				{saveError ? <p className="border-t border-error/40 bg-error/10 px-3 py-2 text-xs text-error" role="alert">{saveError}</p> : null}
				<FileAnnotationSendBar annotation={annotation} className="sticky bottom-0 z-20" surface="focused" />
			</div>
		);
	}
	return (
		<div className="relative min-w-0">
			{toolbarNode}
			<EditProvider createEditor={createReviewEditor}>
			{effectiveMode === "rendered" && renderedAvailable ? renderedView : fileView}
			</EditProvider>
			{saveError ? <p className="border-t border-error/40 bg-error/10 px-3 py-2 text-xs text-error" role="alert">{saveError}</p> : null}
			<FileAnnotationSendBar annotation={annotation} className="sticky bottom-0 z-20" surface="focused" />
		</div>
	);
}

function artifactOriginOf(source: FilesSource): string | undefined {
	if (source.kind !== "artifact" || !source.rawUrl) return undefined;
	try {
		return new URL(source.rawUrl).origin;
	} catch {
		return undefined;
	}
}

function CompleteFileView({ annotation, commitSha, detail, editing, markdownEditing = false, onContentReady, onEditChange, onRevealLineConsumed, revealLine, scope, sessionId, hostId, source, version }: { annotation: FileAnnotationModel; commitSha?: string; detail: WorkspaceFileDetail; editing: boolean; markdownEditing?: boolean; onContentReady?: () => void; onEditChange: (content: string) => void; onRevealLineConsumed?: (requestKey: number) => void; revealLine?: { line: number; requestKey: number }; scope: WorkspaceDiffScope; sessionId: string; hostId?: string; source: FilesSource; version: number }) {
	const { t } = useTranslation();
	const needsRevision = source.kind !== "artifact" && (detail.deleted || detail.contentTruncated) && !detail.imageMediaType;
	const revision = useQuery({
		...sessionSourceFileRevisionQueryOptions({ commitSha, path: detail.path, scope, sessionId, hostId, side: detail.deleted ? "before" : "after", source, workspaceVersion: detail.workspaceVersion }),
		enabled: needsRevision,
	});
	if (needsRevision && revision.isPending && revision.isFetching) return <PanelMessage>{t("files.loading")}</PanelMessage>;
	if (needsRevision && revision.error) return <PanelMessage>{revision.error.message}</PanelMessage>;
	if (needsRevision && revision.data) {
		if (!revision.data.exists) return <PanelMessage>{t("files.error.loadFile")}</PanelMessage>;
		return (
			<ReadOnlyFileView
				annotation={annotation}
				detail={{
					...detail,
					binary: revision.data.binary,
					content: revision.data.content,
					contentTruncated: revision.data.truncated,
					deleted: false,
					size: revision.data.size,
				}}
				editing={editing}
				onContentReady={onContentReady}
				onEditChange={onEditChange}
				onRevealLineConsumed={onRevealLineConsumed}
				revealLine={revealLine}
				sessionId={sessionId}
				hostId={hostId}
				side={detail.deleted ? "before" : "after"}
				scope={scope}
			/>
		);
	}
	if (markdownEditing) return <MarkdownEditor filePath={detail.path} onChange={onEditChange} value={detail.content} />;
	return <ReadOnlyFileView annotation={annotation} detail={detail} editing={editing} imageUrl={artifactImageUrl(source, version)} onContentReady={onContentReady} onEditChange={onEditChange} onRevealLineConsumed={onRevealLineConsumed} revealLine={revealLine} scope={scope} sessionId={sessionId} hostId={hostId} />;
}

// An artifact image loads from its raw URL. The load time joins the URL so a
// rewritten image refetches instead of showing the cached bytes.
function artifactImageUrl(source: FilesSource, version: number): string | undefined {
	if (source.kind !== "artifact" || !source.rawUrl) return undefined;
	try {
		const url = new URL(source.rawUrl);
		url.searchParams.set("v", String(version));
		return url.toString();
	} catch {
		return undefined;
	}
}
