import { useCallback, useEffect, useMemo, useRef, useState, type ReactElement } from "react";
import { useQueries, useQueryClient } from "@tanstack/react-query";
import { parsePatchFiles, type CodeViewItem, type FileDiffMetadata } from "@pierre/diffs";
import { CodeView, type CodeViewHandle } from "@pierre/diffs/react";
import { ChevronRight, FileCode2, GitCompareArrows, MessageSquarePlus, MoreVertical, Pencil, SearchX } from "lucide-react";
import { useTranslation } from "react-i18next";
import {
	fetchWorkspaceFileRevision,
	filesReviewEndOfFileQueryKey,
	invalidateWorkspaceViews,
	sessionWorkspaceDiffsQueryOptions,
	workspaceErrorCode,
	type ReviewTarget,
	type WorkspaceDiffScope,
	type WorkspaceFileSummary,
} from "../../hooks/useSessionWorkspaceFiles";
import { cn } from "../../lib/utils";
import { WORKSPACE_REVIEW_BATCH_SIZE, WORKSPACE_REVIEW_INITIAL_BATCHES, WORKSPACE_REVIEW_BATCH_WAVE } from "../../lib/workspace-review";
import { statusLabel, statusTone } from "../../lib/workspace-file-status";
import { useUiStore } from "../../stores/ui-store";
import { type FileOpenOptions } from "../FileContentPane";
import { EmptyMessage, PanelMessage, RetryButton, FileAnnotationComposer, FileAnnotationSendBar, LineFeedbackButtonControl, cancelFileAnnotations, fileAnnotationKey, type FileAnnotationModel } from "../WorkspaceDiffView";
import { VscodeGoToFileIcon } from "../icons/VscodeGoToFileIcon";
import { WorkspaceEntryIcon } from "../WorkspaceEntryIcon";
import { Button } from "../ui/button";
import { Checkbox } from "../ui/checkbox";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "../ui/dropdown-menu";
import { Popover, PopoverAnchor, PopoverContent } from "../ui/popover";
import { Tooltip, TooltipContent, TooltipTrigger } from "../ui/tooltip";
import { useAskInChat } from "../../lib/chat-context-bus";
import { sessionUiKey } from "../../lib/hosts";
import { AO_PIERRE_FILES_REVIEW_CSS, AO_PIERRE_SURFACE_CSS } from "./pierreTheme";
import { ImageDiffView, isWorkspaceImagePath } from "../ImageDiffView";
import { REVIEW_CONTEXT_LINES, diffContentVersion, endsAtLastHunk, hydratedCopy, patchIdentity, stableFileDiff } from "./trailingContext";
import { SelectionAskButton } from "./SelectionAskButton";
import { codeReference, useCodeSelection } from "./useCodeSelection";
import { usePersistentGutterUtility } from "./usePersistentGutterUtility";

const parsedPatchCache = new Map<string, { patch: string; files: FileDiffMetadata[] }>();
const MAX_PARSED_GROUPS = 24;
// Height of the custom per-file header row below (h-9).
const FILE_HEADER_HEIGHT_PX = 36;
// Keeps an item's version (a 32-bit content hash, two flag bits, then this) a safe integer.
const ANNOTATION_REVISION_SPAN = 65_536;
const REVIEW_CODE_VIEW_LAYOUT = { gap: 4, paddingBottom: 8, paddingTop: 0 };
const REVIEW_CODE_VIEW_METRICS = { diffHeaderHeight: FILE_HEADER_HEIGHT_PX };
const REVIEW_CODE_VIEW_THEME = { dark: "github-dark", light: "github-light" } as const;
// Files whose patch proves they end at the last hunk get their full contents up
// front, so their diff has no dead "More unchanged context may be available"
// row. Bounded so a big review doesn't fetch every file; beyond these limits the
// row stays and still loads on click.
const MAX_END_OF_FILE_PREFETCHES = 40;
const END_OF_FILE_PREFETCH_MAX_BYTES = 128 * 1024;
// Keep speculative reads serial within a host/session, without allowing one
// stalled host to block other reviews. Every active pair has its own deadline.
const FULL_CONTEXT_PREFETCH_TIMEOUT_MS = 30_000;
const fullContextPrefetchTails = new Map<string, Promise<void>>();
function prefetchFullContext<T>(key: string, load: (signal: AbortSignal) => Promise<T>, signal: AbortSignal): Promise<T> {
	const previous = fullContextPrefetchTails.get(key) ?? Promise.resolve();
	let release = () => {};
	const tail = new Promise<void>((resolve) => { release = resolve; });
	fullContextPrefetchTails.set(key, tail);
	return previous.then(async () => {
		signal.throwIfAborted();
		const controller = new AbortController();
		const onAbort = () => controller.abort(signal.reason);
		signal.addEventListener("abort", onAbort, { once: true });
		const timer = setTimeout(() => controller.abort(new DOMException("Diff context loading timed out", "TimeoutError")), FULL_CONTEXT_PREFETCH_TIMEOUT_MS);
		let onCancelled = () => {};
		const cancelled = new Promise<never>((_resolve, reject) => {
			onCancelled = () => reject(controller.signal.reason);
			controller.signal.addEventListener("abort", onCancelled, { once: true });
		});
		try {
			return await Promise.race([load(controller.signal), cancelled]);
		} finally {
			clearTimeout(timer);
			// Stop a sibling request if the other side failed first.
			controller.abort();
			signal.removeEventListener("abort", onAbort);
			controller.signal.removeEventListener("abort", onCancelled);
		}
	}).finally(() => {
		release();
		if (fullContextPrefetchTails.get(key) === tail) fullContextPrefetchTails.delete(key);
	});
}

function chunked<T>(items: readonly T[], size: number): T[][] {
	const chunks: T[][] = [];
	for (let index = 0; index < items.length; index += size) chunks.push(items.slice(index, index + size));
	return chunks;
}

function patchCacheKey(workspaceVersion: string | undefined, scope: WorkspaceDiffScope, selectionKey: string, repository: string | undefined, patch: string) {
	return `${workspaceVersion ?? "legacy"}:${scope}:${selectionKey || "working"}:${repository ?? "root"}:${patch.length}:${patch.slice(0, 80)}:${patch.slice(-80)}`;
}

function parseGroupPatch(workspaceVersion: string | undefined, scope: WorkspaceDiffScope, selectionKey: string, repository: string | undefined, patch: string) {
	const key = patchCacheKey(workspaceVersion, scope, selectionKey, repository, patch);
	const cached = parsedPatchCache.get(key);
	// The bounded key is only a lookup hint: same-sized edits can preserve
	// both outer slices and the metadata-based workspace version.
	if (cached?.patch === patch) return cached.files;
	const prefix = repository ? `${repository}/` : "";
	const files = parsePatchFiles(patch, key, true).flatMap((entry) => entry.files);
	for (const file of files) {
		if (prefix && !file.name.startsWith(prefix)) file.name = prefix + file.name;
		if (prefix && file.prevName && !file.prevName.startsWith(prefix)) file.prevName = prefix + file.prevName;
	}
	parsedPatchCache.set(key, { patch, files });
	if (parsedPatchCache.size > MAX_PARSED_GROUPS) {
		const oldest = parsedPatchCache.keys().next().value;
		if (oldest) parsedPatchCache.delete(oldest);
	}
	return files;
}

function isDeferredByDefault(file: WorkspaceFileSummary) {
	if (file.binary) return false;
	const name = file.path.split("/").pop()?.toLowerCase() ?? "";
	return file.size > 512 * 1024 || /^(package-lock\.json|pnpm-lock\.yaml|yarn\.lock|bun\.lockb?|go\.sum|cargo\.lock)$/.test(name);
}



type ViewedRecord = Record<string, string>;

function viewedStorageKey(sessionId: string, selectionKey: string) {
	return `ao.files.viewed.${sessionId}.${selectionKey}`;
}

function readViewedRecords(key: string): ViewedRecord {
	try {
		return JSON.parse(window.localStorage.getItem(key) ?? "{}") as ViewedRecord;
	} catch {
		return {};
	}
}

function isViewedRecord(file: WorkspaceFileSummary, records: ViewedRecord) {
	return records[file.path] === (file.fileFingerprint ?? "legacy");
}

function useViewedFiles(sessionId: string, selectionKey: string, files: readonly WorkspaceFileSummary[]) {
	const key = viewedStorageKey(sessionId, selectionKey);
	const [records, setRecords] = useState<ViewedRecord>(() => readViewedRecords(key));
	useEffect(() => {
		setRecords(readViewedRecords(key));
	}, [key]);
	const viewed = useMemo(
		() => new Set(files.filter((file) => isViewedRecord(file, records)).map((file) => file.path)),
		[files, records],
	);
	const toggle = useCallback(
		(file: WorkspaceFileSummary) => {
			setRecords((current) => {
				const next = { ...current };
				if (next[file.path] === (file.fileFingerprint ?? "legacy")) delete next[file.path];
				else next[file.path] = file.fileFingerprint ?? "legacy";
				window.localStorage.setItem(key, JSON.stringify(next));
				return next;
			});
		},
		[key],
	);
	return { viewed, toggle };
}

export function WorkspaceReviewPane({
	annotation,
	target,
	onRetry,
	filter,
	onBrowseAll,
	onOpenFile: onOpenFileProp,
	canOpenInCenter = true,
	onIncludeCommitsBetween,
	onShowAllCommits,
	sessionId,
	hostId,
	split,
	collapseRequest,
	onAllCollapsedChange,
	scrollRequest,
}: {
	annotation: FileAnnotationModel;
	/** What to review: the Files header's branch and changes. */
	target: ReviewTarget;
	/** Reads a failed target again. */
	onRetry?: () => void;
	filter: string;
	/** Offered when there is nothing to review; omitted where the file tree can't show the change. */
	onBrowseAll?: () => void;
	/** Opens the file at the selected review source, including historical targets. */
	onOpenFile?: (path: string, options?: FileOpenOptions) => void;
	/**
	 * False only when the host has no center navigation callback. A maximized
	 * host closes its overlay before handing the file to the center.
	 */
	canOpenInCenter?: boolean;
	/** Ways out of commits that can't be combined. */
	onIncludeCommitsBetween?: () => void;
	onShowAllCommits?: () => void;
	sessionId: string;
	hostId?: string;
	split: boolean;
	/** From the Files toolbar: collapse (or expand) every file; a new key asks again. */
	collapseRequest?: { expand: boolean; key: number };
	/** Tells the toolbar whether every file is collapsed, so it offers the other action. */
	onAllCollapsedChange?: (allCollapsed: boolean) => void;
	/** From the changed-files rail: bring this file's diff into view; a new key asks again. */
	scrollRequest?: { path: string; key: number };
}) {
	const { t } = useTranslation();
	const queryClient = useQueryClient();
	const resolvedTheme = useUiStore((state) => state.resolvedTheme);
	const [collapsedPaths, setCollapsedPaths] = useState<Set<string>>(() => new Set());
	const [loadedDeferredPaths, setLoadedDeferredPaths] = useState<Set<string>>(() => new Set());
	const [activeBatchCount, setActiveBatchCount] = useState(WORKSPACE_REVIEW_INITIAL_BATCHES);
	const reviewRef = useRef<HTMLDivElement>(null);
	const fallbackRows = useRef(new Map<string, HTMLDivElement>());
	const codeViewRef = useRef<CodeViewHandle<"feedback", undefined> | null>(null);
	const [pendingScrollPath, setPendingScrollPath] = useState<string | null>(null);
	const gutterHover = usePersistentGutterUtility(reviewRef);
	// Set when a file row's overflow menu hands off to another surface (an editor,
	// a preview, the feedback composer) so closing the menu doesn't pull focus
	// back to its trigger; dismissing it without a choice still restores focus.
	const fileMenuHandoffRef = useRef(false);

	const { scope, selectionKey, workspaceVersion } = target;
	// Reads and navigation share the selected source and revision.
	const onOpenFile = onOpenFileProp;
	const allFiles = useMemo(() => target.files ?? [], [target.files]);
	const filesKnown = target.files !== undefined;
	const reviewSelectionKey = target.key;
	const normalizedFilter = filter.trim().toLowerCase();
	const files = useMemo(
		() => (normalizedFilter ? allFiles.filter((file) => `${file.path} ${file.previousPath ?? ""}`.toLowerCase().includes(normalizedFilter)) : allFiles),
		[allFiles, normalizedFilter],
	);
	const viewedSessionKey = sessionUiKey(sessionId, hostId);
	const { viewed, toggle: toggleViewed } = useViewedFiles(viewedSessionKey, reviewSelectionKey, allFiles);

	// Feedback drafted against one review target doesn't apply to the next.
	const previousTargetKey = useRef(reviewSelectionKey);
	useEffect(() => {
		if (previousTargetKey.current === reviewSelectionKey) return;
		previousTargetKey.current = reviewSelectionKey;
		cancelFileAnnotations(annotation, (entry) => entry.surface === "review");
	}, [annotation, reviewSelectionKey]);

	// Reset the collapse / deferred / batch state only when the review target
	// itself changes (workspace version, target, its files arriving, session) — computed
	// from the unfiltered allFiles. Keying on the filtered `files` instead would
	// reset all of this on every file-filter keystroke, discarding the user's manual
	// expand/collapse, loaded deferred diffs, and progressive batch progress.
	useEffect(() => {
		const savedViewed = readViewedRecords(viewedStorageKey(viewedSessionKey, reviewSelectionKey));
		setCollapsedPaths(new Set(allFiles.filter((file) => isDeferredByDefault(file) || isViewedRecord(file, savedViewed)).map((file) => file.path)));
		setLoadedDeferredPaths(new Set());
		setActiveBatchCount(WORKSPACE_REVIEW_INITIAL_BATCHES);
		setPendingScrollPath(null);
		// eslint-disable-next-line react-hooks/exhaustive-deps -- reset on review-target identity, not on allFiles' reference (which changes on every poll) or the filtered files (which changes per keystroke).
	}, [workspaceVersion, reviewSelectionKey, filesKnown, viewedSessionKey]);

	// Opening a deferred file must not change the regular batches' query keys.
	const regularBatches = useMemo(() => chunked(files.filter((file) => !isDeferredByDefault(file)).map((file) => file.path), WORKSPACE_REVIEW_BATCH_SIZE), [files]);
	const batches = useMemo(() => [
		...regularBatches,
		...Array.from(loadedDeferredPaths).filter((path) => files.some((file) => file.path === path && isDeferredByDefault(file))).map((path) => [path]),
	], [files, loadedDeferredPaths, regularBatches]);
	const patchQueries = useQueries({
		queries: batches.map((paths, index) => ({
			...sessionWorkspaceDiffsQueryOptions({
				// endsAtLastHunk reads the trailing context, so request exactly what it assumes.
				contextLines: REVIEW_CONTEXT_LINES,
				errorMessage: t("files.error.loadWorkspace"),
				paths,
				scope,
				sessionId,
				hostId,
				workspaceVersion,
				commitShas: target.commitShas,
				view: target.view,
			}),
			enabled: target.state === "ready" && paths.length > 0 && (index < activeBatchCount || index >= regularBatches.length),
			staleTime: Infinity,
		})),
	});
	useEffect(() => {
		const active = patchQueries.slice(0, Math.min(activeBatchCount, regularBatches.length));
		if (active.some((query) => query.isPending || query.isFetching)) return;
		if (activeBatchCount < regularBatches.length) {
			setActiveBatchCount((current) => Math.min(current + WORKSPACE_REVIEW_BATCH_WAVE, regularBatches.length));
		}
	}, [activeBatchCount, regularBatches.length, patchQueries]);

	const { metadataByPath, endOfFilePaths } = useMemo(() => {
		const result = new Map<string, FileDiffMetadata>();
		const endOfFile = new Set<string>();
		for (const query of patchQueries) {
			for (const group of query.data?.groups ?? []) {
				try {
					for (const metadata of parseGroupPatch(query.data?.workspaceVersion, scope, selectionKey, group.repository, group.patch)) {
						result.set(metadata.name, metadata);
						// A truncated group can cut its last patch short, which would look
						// like that file ending early.
						if (!group.truncated && endsAtLastHunk(metadata)) endOfFile.add(metadata.name);
					}
				} catch {
					// The group retains its retry/error surface below; one malformed patch
					// must not prevent other repositories from rendering.
				}
			}
		}
		return { metadataByPath: result, endOfFilePaths: endOfFile };
	}, [patchQueries, scope, selectionKey]);
	const serverDeferredByPath = useMemo(() => {
		const result = new Map<string, string>();
		for (const query of patchQueries) {
			for (const group of query.data?.groups ?? []) {
				for (const deferred of group.deferred) result.set(deferred.path, deferred.reason);
			}
		}
		return result;
	}, [patchQueries]);
	// A batch still in flight (or still queued behind activeBatchCount) is the
	// only reason a requested file can legitimately have no patch yet. Once its
	// batch settles, a file with no diff is a failure the user can retry or step
	// around, not a load that will finish on its own.
	const pendingDiffPaths = useMemo(() => {
		const result = new Set<string>();
		batches.forEach((paths, index) => {
			const query = patchQueries[index];
			if (!query || query.isPending || query.isFetching) for (const path of paths) result.add(path);
		});
		return result;
	}, [batches, patchQueries]);

	const summaryById = useMemo(() => new Map(files.map((file) => [`${reviewSelectionKey}:${file.path}`, file])), [files, reviewSelectionKey]);
	// Highlighted code gets an "Ask in chat" button (or Cmd/Ctrl+L) while the
	// session has a Chat composer; each file's header carries its item id.
	const askInChat = useAskInChat(sessionId, hostId);
	const codeSelection = useCodeSelection(reviewRef, (selection) => {
		// Pierre portals the custom header into this host's light DOM; the selected
		// code is in its shadow root. Search this host to keep the file lookup local.
		const file = summaryById.get(selection.host.querySelector("[data-review-item-id]")?.getAttribute("data-review-item-id") ?? "");
		if (file) askInChat?.(codeReference(file.path, selection, true));
	}, askInChat !== undefined);

	const loadDiffFiles = useCallback(
		async (metadata: FileDiffMetadata, signal?: AbortSignal) => {
			// Pierre may hand this callback a normalized metadata object rather than
			// the exact object stored in our parse cache, so resolve by stable path.
			const file = files.find((candidate) => candidate.path === metadata.name);
			if (!file) throw new Error(t("files.error.loadFile"));
			const read = { commitShas: target.commitShas, view: target.view, sessionId, path: file.path, scope, workspaceVersion, hostId, signal };
			const [before, after] = await Promise.all([
				fetchWorkspaceFileRevision({ ...read, side: "before" }),
				fetchWorkspaceFileRevision({ ...read, side: "after" }),
			]);
			if (before.binary || after.binary || before.truncated || after.truncated) throw new Error(t("files.error.loadFile"));
			const newFile = { name: file.path, contents: after.content, cacheKey: after.revision };
			if (metadata.type === "rename-pure") return { oldFile: null, newFile };
			return { oldFile: { name: file.previousPath || file.path, contents: before.content, cacheKey: before.revision }, newFile };
		},
		[files, scope, sessionId, hostId, t, target.commitShas, target.view, workspaceVersion],
	);

	// Keyed by patch content (not workspace version), so a refresh that leaves a
	// file's diff unchanged reuses the contents instead of flashing the row back.
	const endOfFileFiles = useMemo(
		() => files.filter((file) => endOfFilePaths.has(file.path) && file.size <= END_OF_FILE_PREFETCH_MAX_BYTES).slice(0, MAX_END_OF_FILE_PREFETCHES),
		[endOfFilePaths, files],
	);
	const endOfFileContents = useQueries({
		queries: endOfFileFiles.map((file) => {
			const metadata = metadataByPath.get(file.path);
			return {
				queryKey: filesReviewEndOfFileQueryKey(sessionId, scope, selectionKey, file.path, file.fileFingerprint ?? "", metadata ? patchIdentity(metadata) : "", hostId),
				queryFn: ({ signal }) => {
					if (!metadata) throw new Error(t("files.error.loadFile"));
					return prefetchFullContext(viewedSessionKey, (activeSignal) => loadDiffFiles(metadata, activeSignal), signal);
				},
				enabled: metadata != null,
				retry: false,
				staleTime: Infinity,
			};
		}),
	});

	// CodeView re-renders an item whenever the item object changes, and Pierre
	// 1.4.1 throws if that render sees a different object with the same cache key.
	const publishedItemsRef = useRef(new Map<string, CodeViewItem<"feedback">>());
	const annotationRevisionsRef = useRef(new Map<string, { key: string; revision: number }>());

	const items = useMemo(
		() => {
			const itemId = (path: string) => `${reviewSelectionKey}:${path}`;
			const endOfFile = new Map<string, FileDiffMetadata>();
			endOfFileFiles.forEach((file, index) => {
				const metadata = metadataByPath.get(file.path);
				const query = endOfFileContents[index];
				const hydrated = metadata && query?.data ? hydratedCopy(metadata, query.data) : null;
				if (hydrated) endOfFile.set(file.path, hydrated);
			});
			return files.flatMap((file): CodeViewItem<"feedback">[] => {
				if (file.binary) return [];
				const metadata = metadataByPath.get(file.path);
				if (!metadata) return [];
				const fileDiff = stableFileDiff(endOfFile.get(file.path) ?? metadata);
				const collapsed = collapsedPaths.has(file.path);
				const fileTargets = annotation.targets.filter((target) => target.surface !== "focused" && target.path === file.path);
				const fileAnnotationActive = fileTargets.some((target) => target.side === "file");
				const activeTargets = fileTargets.filter((target) => target.side !== "file" && target.line != null);
				const id = itemId(file.path);
				// Each set of open boxes gets its own revision, so opening or closing
				// one among several still changes the item's version.
				const annotationKey = activeTargets.map(fileAnnotationKey).join("\n");
				const lastRevision = annotationRevisionsRef.current.get(id);
				const annotationRevision = lastRevision?.key === annotationKey ? lastRevision.revision : (lastRevision?.revision ?? 0) + 1;
				annotationRevisionsRef.current.set(id, { key: annotationKey, revision: annotationRevision });
				const version = (diffContentVersion(fileDiff) * 4 + (collapsed ? 1 : 0) + (fileAnnotationActive ? 2 : 0)) * ANNOTATION_REVISION_SPAN + (annotationRevision % ANNOTATION_REVISION_SPAN);
				const previous = publishedItemsRef.current.get(id);
				if (previous?.type === "diff" && activeTargets.length === 0 && !fileAnnotationActive && previous.annotations == null && previous.fileDiff === fileDiff && previous.version === version && previous.collapsed === collapsed) return [previous];
				const item: CodeViewItem<"feedback"> = {
					id,
					type: "diff",
					fileDiff,
					collapsed,
					annotations: activeTargets.length > 0 ? activeTargets.map((target) => ({
						lineNumber: target.line as number,
						side: target.side === "old" ? "deletions" : "additions",
						metadata: "feedback",
					})) : undefined,
					// CodeView only re-reads an item when its version changes: the content
					// part lets changed or newly hydrated diffs through, the low bits carry
					// the collapsed/annotation state.
					version,
				};
				publishedItemsRef.current.set(id, item);
				return [item];
			});
		},
		[annotation.targets, collapsedPaths, endOfFileContents, endOfFileFiles, files, metadataByPath, reviewSelectionKey],
	);

	const beginLineAnnotation = useCallback((itemId: string, lineNumber: number, side: "deletions" | "additions") => {
		const file = summaryById.get(itemId);
		if (!file) return;
		annotation.begin({
			path: file.path,
			previousPath: file.previousPath,
			side: side === "deletions" ? "old" : "new",
			line: lineNumber,
			oldLine: side === "deletions" ? lineNumber : undefined,
			newLine: side === "additions" ? lineNumber : undefined,
			scope,
			workspaceVersion,
			fileFingerprint: file.fileFingerprint,
			surface: "review",
		});
	}, [annotation, workspaceVersion, scope, summaryById]);
	const toggleCollapsed = useCallback((file: WorkspaceFileSummary) => {
		const path = file.path;
		// An explicit expansion opts deferred files into loading, just like
		// Expand all. Keep that choice when the file is collapsed again.
		if (collapsedPaths.has(path) && isDeferredByDefault(file) && !loadedDeferredPaths.has(path)) {
			setLoadedDeferredPaths((current) => current.has(path) ? current : new Set(current).add(path));
			setPendingScrollPath(path);
		}
		setCollapsedPaths((current) => {
			const next = new Set(current);
			if (next.has(path)) next.delete(path);
			else next.add(path);
			return next;
		});
	}, [collapsedPaths, loadedDeferredPaths]);
	const collapsePath = useCallback((path: string) => {
		setCollapsedPaths((current) => {
			if (current.has(path)) return current;
			const next = new Set(current);
			next.add(path);
			return next;
		});
	}, []);
	const markViewed = useCallback((file: WorkspaceFileSummary, checked: boolean) => {
		toggleViewed(file);
		if (checked) collapsePath(file.path);
	}, [collapsePath, toggleViewed]);
	const collapseAll = useCallback(() => {
		setCollapsedPaths(new Set(files.map((file) => file.path)));
	}, [files]);
	const expandAll = useCallback(() => {
		setLoadedDeferredPaths(new Set(files.filter(isDeferredByDefault).map((file) => file.path)));
		setCollapsedPaths(new Set());
	}, [files]);
	const allFilesCollapsed = files.length > 0 && files.every((file) => collapsedPaths.has(file.path));
	useEffect(() => {
		onAllCollapsedChange?.(allFilesCollapsed);
	}, [allFilesCollapsed, onAllCollapsedChange]);
	// Only a request made while this pane is showing acts; one already handled
	// before a remount must not collapse the next review.
	const handledCollapseKey = useRef(collapseRequest?.key);
	const collapseActions = useRef({ collapseAll, expandAll });
	collapseActions.current = { collapseAll, expandAll };
	useEffect(() => {
		if (!collapseRequest || collapseRequest.key === handledCollapseKey.current) return;
		handledCollapseKey.current = collapseRequest.key;
		if (collapseRequest.expand) collapseActions.current.expandAll();
		else collapseActions.current.collapseAll();
	}, [collapseRequest]);
	// The rail can pick a file whose diff isn't in the list yet: a deferred file
	// loads, a queued batch starts, and the scroll waits until the diff is there.
	// The picked file opens, since it was asked for.
	const handledScrollKey = useRef(scrollRequest?.key);
	useEffect(() => {
		if (!scrollRequest || scrollRequest.key === handledScrollKey.current) return;
		handledScrollKey.current = scrollRequest.key;
		const { path } = scrollRequest;
		const file = files.find((entry) => entry.path === path);
		if (!file) return;
		if (isDeferredByDefault(file)) setLoadedDeferredPaths((current) => (current.has(path) ? current : new Set(current).add(path)));
		setCollapsedPaths((current) => {
			if (!current.has(path)) return current;
			const next = new Set(current);
			next.delete(path);
			return next;
		});
		setPendingScrollPath(path);
	}, [files, scrollRequest]);
	useEffect(() => {
		if (!pendingScrollPath) return;
		const id = `${reviewSelectionKey}:${pendingScrollPath}`;
		if (items.some((item) => item.id === id)) {
			codeViewRef.current?.scrollTo({ type: "item", id, align: "start" });
			setPendingScrollPath(null);
			return;
		}
		const batchIndex = batches.findIndex((paths) => paths.includes(pendingScrollPath));
		if (batchIndex >= activeBatchCount && batchIndex < regularBatches.length) {
			setActiveBatchCount(batchIndex + 1);
			return;
		}
		if (batchIndex >= 0 && pendingDiffPaths.has(pendingScrollPath)) return;
		// Footer rows are real DOM elements rather than virtualized items. Wait
		// for CodeView's footer portal to commit before bringing one into view.
		const frame = requestAnimationFrame(() => {
			fallbackRows.current.get(pendingScrollPath)?.scrollIntoView({ block: "start" });
			setPendingScrollPath(null);
		});
		return () => cancelAnimationFrame(frame);
	}, [activeBatchCount, batches, items, pendingDiffPaths, pendingScrollPath, regularBatches.length, reviewSelectionKey]);
	const retryAll = () => patchQueries.forEach((query) => void query.refetch());
	const firstError = patchQueries.find((query) => query.error)?.error;
	const groupError = patchQueries.flatMap((query) => query.data?.groups ?? []).flatMap((group) => group.errors ?? [])[0];
	const loading = patchQueries.some((query) => query.isPending);
	const fileOpenContext: FileOpenOptions = {...(target.source ? {source:target.source} : target.sha ? {commitSha:target.sha}:{}),scope};
	const changes = target.changes;
	const emptyMessage = changes.kind === "uncommitted" || changes.kind === "staged" || changes.kind === "unstaged"
		? t(`files.changes.empty.${changes.kind}`)
		: changes.kind === "combined" ? t("files.noneChanged") : t("files.noneInSource");
	const conflict = target.conflict;

	// A moved tip or base fails the read it fenced; the views' new tips key new reads.
	const refMoved = [...patchQueries, ...endOfFileContents].some((query) => workspaceErrorCode(query.error) === "WORKSPACE_REF_MOVED");
	useEffect(() => {
		if (refMoved) invalidateWorkspaceViews(queryClient, sessionId, hostId);
	}, [hostId, queryClient, refMoved, sessionId]);

	const fallbackFiles = files.filter((file) => file.binary || !metadataByPath.has(file.path));
	// Keep non-text rows in Pierre's measured scroll content, including reviews
	// with no text items. Its ResizeObserver tracks late image loads and collapse.
	const renderFallbackFiles = () => (
		<>
			{fallbackFiles.map((file) => {
				const collapsed = collapsedPaths.has(file.path);
				const deferred = isDeferredByDefault(file) && !loadedDeferredPaths.has(file.path);
				const serverDeferredReason = serverDeferredByPath.get(file.path);
				const pending = pendingDiffPaths.has(file.path);
				const unavailable = !file.binary && !deferred && !serverDeferredReason && !pending;
				const image = file.binary && !target.source && target.openable && isWorkspaceImagePath(file.path);
				const isViewed = viewed.has(file.path);
				return (
					<div className="bg-background" key={file.path} ref={(node) => { if (node) fallbackRows.current.set(file.path, node); else fallbackRows.current.delete(file.path); }}>
						<div className="group/file-header flex h-9 min-w-0 items-center gap-2 pl-4 pr-1.5 hover:bg-interactive-hover/40">
							<button
								aria-expanded={!collapsed}
								aria-label={t(collapsed ? "files.expandFile" : "files.collapseFile", { file: file.path })}
								className="flex h-full min-w-0 flex-1 items-center gap-2 text-left text-[length:var(--font-size-base)] focus-visible:underline"
								onClick={() => toggleCollapsed(file)}
								type="button"
							>
								<ChevronRight aria-hidden="true" className={cn("size-3.5 shrink-0 text-muted-foreground", !collapsed && "rotate-90")} />
								<WorkspaceEntryIcon className="size-icon-xl" kind="file" name={file.path.split("/").pop() ?? file.path} />
								<span className="truncate" title={file.path}>{file.path}</span>
								<span className={cn("shrink-0 text-xs font-semibold", statusTone[file.status])}>{statusLabel[file.status]}</span>
							</button>
							{onOpenFile ? (
								<HeaderActionTooltip label={t("files.openFullFileGeneric")}>
									<Button aria-label={t("files.openFullFileGeneric")} className="size-6 shrink-0 text-muted-foreground hover:text-foreground" onClick={() => onOpenFile(file.path, { ...fileOpenContext, mode: "file" })} size="icon-sm" type="button" variant="ghost"><FileCode2 aria-hidden="true" className="size-icon-sm" /></Button>
								</HeaderActionTooltip>
							) : null}
							<HeaderActionTooltip label={isViewed ? t("files.markUnviewed", { file: file.path }) : t("files.markViewed", { file: file.path })}>
								<span className="grid size-6 shrink-0 place-items-center">
									<Checkbox
										aria-label={isViewed ? t("files.markUnviewed", { file: file.path }) : t("files.markViewed", { file: file.path })}
										checked={isViewed}
										className="size-4 border border-muted-foreground/70 bg-transparent data-[state=checked]:border-foreground data-[state=checked]:bg-foreground data-[state=checked]:text-on-foreground"
										onCheckedChange={(checked) => markViewed(file, checked === true)}
									/>
								</span>
							</HeaderActionTooltip>
						</div>
						{!collapsed ? (
							<div className={cn("min-w-0", !image && "p-3")}>
								{image ? (
									<ImageDiffView path={file.path} sessionId={sessionId} hostId={hostId} split={split} status={file.status} version={workspaceVersion ?? ""} />
								) : (
									<p className="text-caption text-muted-foreground">{file.binary ? t("files.binaryUnavailable") : deferred ? t("files.deferredDiff") : serverDeferredReason ? t("files.diffUnavailableReason", { reason: serverDeferredReason }) : pending ? t("files.loadingDiff") : t("files.diffUnavailable")}</p>
								)}
								{deferred ? <Button onClick={() => setLoadedDeferredPaths((current) => new Set(current).add(file.path))} size="sm" type="button" variant="outline">{t("files.loadDiff")}</Button> : null}
								{unavailable ? <RetryButton onClick={retryAll} /> : null}
							</div>
						) : null}
					</div>
				);
			})}
		</>
	);

	return (
		<div
			className="@container/review flex h-full min-h-0 flex-col"
			onPointerLeave={gutterHover.onPointerLeave}
			onPointerMove={gutterHover.onPointerMove}
			ref={reviewRef}
		>
			{target.state === "pending" ? (
				<PanelMessage compact>{(target.commitShas?.length ?? 0) > 1 ? t("files.changes.combining") : t("files.loading")}</PanelMessage>
			) : target.state === "error" ? (
				<PanelMessage action={onRetry ? <RetryButton onClick={onRetry} /> : undefined}>{target.error?.message || t("files.error.loadWorkspace")}</PanelMessage>
			) : target.state === "not_combinable" ? (
				<PanelMessage
					action={(
						<div className="flex flex-wrap items-center justify-center gap-2">
							{onIncludeCommitsBetween ? <Button onClick={onIncludeCommitsBetween} size="sm" type="button" variant="outline">{t("files.changes.includeBetween")}</Button> : null}
							{onShowAllCommits ? <Button onClick={onShowAllCommits} size="sm" type="button" variant="outline">{t("files.changes.showAllCommits")}</Button> : null}
						</div>
					)}
				>
					{conflict?.reason === "unsupported_history"
						? t("files.changes.unsupportedHistory")
						: t("files.changes.notCombinable", { subject: conflict?.subject || conflict?.commitSha.slice(0, 7) || "" })}
				</PanelMessage>
			) : (
				<>
			{firstError ? <PanelMessage action={<RetryButton onClick={retryAll} />}>{firstError.message}</PanelMessage> : null}
			{groupError ? <PanelMessage action={<RetryButton onClick={retryAll} />}>{groupError.message}</PanelMessage> : null}
			{loading && items.length === 0 ? <PanelMessage compact>{t("files.loadingDiff")}</PanelMessage> : null}
			{files.length === 0 ? (
				<EmptyMessage
					action={allFiles.length === 0 && onBrowseAll ? <Button onClick={onBrowseAll} type="button">{t("files.browseAll")}</Button> : undefined}
					icon={allFiles.length === 0 ? GitCompareArrows : SearchX}
					title={allFiles.length === 0 ? emptyMessage : t("files.noFilterMatches")}
				/>
			) : null}
			<div className={cn("min-h-0 overflow-hidden", files.length > 0 && "flex-1")}>
				{items.length > 0 || fallbackFiles.length > 0 ? (
					<CodeView<"feedback">
						ref={codeViewRef}
						className="ao-pierre-surface board-scrollbar h-full min-h-0 select-text overflow-y-auto overscroll-contain"
						disableWorkerPool={typeof Worker === "undefined"}
						items={items}
						renderCodeViewFooter={renderFallbackFiles}
						options={{
							collapsedContextThreshold: 8,
							diffIndicators: "classic",
							diffStyle: split ? "split" : "unified",
							enableGutterUtility: true,
							expansionLineCount: 20,
							hunkSeparators: "line-info",
							lineDiffType: "word-alt",
							layout: REVIEW_CODE_VIEW_LAYOUT,
							// Must match the custom file header height (h-9 = 36px). Pierre's default
							// (44px) reserves the difference and pushes the header down by 8px.
							itemMetrics: REVIEW_CODE_VIEW_METRICS,
							lineHoverHighlight: "line",
							loadDiffFiles,
							maxLineDiffLength: 400,
							onPostRender: gutterHover.restoreAfterRender,
							overflow: "wrap",
							stickyHeaders: true,
							theme: REVIEW_CODE_VIEW_THEME,
							themeType: resolvedTheme,
							tokenizeMaxLength: 200_000,
							tokenizeMaxLineLength: 2_000,
							unsafeCSS: AO_PIERRE_SURFACE_CSS + AO_PIERRE_FILES_REVIEW_CSS,
						}}
						renderAnnotation={(line, item) => {
							const path = summaryById.get(item.id)?.path;
							const side = "side" in line ? line.side : undefined;
							const target = annotation.targets.find((open) => open.surface !== "focused" && open.path === path && open.side !== "file" && open.line === line.lineNumber && (open.side === "old" ? "deletions" : "additions") === side);
							return target ? <FileAnnotationComposer annotation={annotation} target={target} /> : null;
						}}
						renderGutterUtility={(getHoveredLine, item) => (
							<LineFeedbackButtonControl
								gutter
								label={t("files.addFeedback")}
								onClick={() => {
									const line = getHoveredLine();
									if (!line) return;
									const side = "side" in line ? line.side : undefined;
									if (side === "additions" || side === "deletions") beginLineAnnotation(item.id, line.lineNumber, side);
								}}
							/>
						)}
						renderCustomHeader={(item) => {
							const file = summaryById.get(item.id);
							if (!file) return null;
							const isViewed = viewed.has(file.path);
							const isCollapsed = collapsedPaths.has(file.path);

							const fileAnnotationTarget = annotation.targets.find((target) => target.surface !== "focused" && target.path === file.path && target.side === "file");
							const fileAnnotationActive = fileAnnotationTarget != null;
							return (
								<Popover onOpenChange={(open) => { if (!open && fileAnnotationTarget) annotation.cancel(fileAnnotationTarget); }} open={fileAnnotationActive}>
									{/* Lets a code selection in this file's surface find its item. */}
									<div className="relative bg-background" data-review-item-id={item.id}>
										{/* The whole row toggles the file (the chevron just rotates);
										    name + stats on the left, every action grouped on the right
										    with "viewed" pinned to the far edge. */}
										<PopoverAnchor asChild>
											<div className="group/file-header flex h-9 min-w-0 cursor-pointer items-center gap-2 pl-4 pr-1.5 hover:bg-interactive-hover/40" onClick={() => toggleCollapsed(file)}>
												<ChevronRight aria-hidden="true" className={cn("size-3.5 shrink-0 text-muted-foreground transition-transform duration-150 group-hover/file-header:text-foreground", !isCollapsed && "rotate-90")} />
												<WorkspaceEntryIcon className="size-icon-xl" kind="file" name={file.path.split("/").pop() ?? file.path} />
												<div className="flex min-w-0 shrink items-baseline gap-3">
													<button
														aria-expanded={!isCollapsed}
														aria-label={isCollapsed ? t("files.expandFile", { file: file.path }) : t("files.collapseFile", { file: file.path })}
														className="flex min-w-0 shrink items-baseline text-left text-[length:var(--font-size-base)] outline-none focus-visible:underline"
														title={file.path}
														type="button"
													>
														{file.path.includes("/") ? <span className="min-w-0 truncate text-muted-foreground">{file.path.slice(0, file.path.lastIndexOf("/") + 1)}</span> : null}
														<span className="max-w-full shrink-0 truncate text-foreground">{file.path.slice(file.path.lastIndexOf("/") + 1)}</span>
													</button>
													<span className="flex shrink-0 items-baseline gap-2.5 text-xs tabular-nums">
														<span className={cn("font-semibold", statusTone[file.status])}>{statusLabel[file.status]}</span>
														<span className="flex items-baseline gap-1.5 @max-[25rem]/review:hidden">
															<span className="text-success">+{file.additions}</span>
															<span className="text-error">−{file.deletions}</span>
														</span>
													</span>
												</div>
												{/* Same 4px gap as the inspector tab buttons. In a narrow pane the
												    secondary actions fold into one ⋮ menu (like the browser's
												    controls) and the extra lead-in goes, so the file name keeps its
												    room; the row's own gap still separates it from the stats. */}
												<div className="ml-auto flex shrink-0 items-center gap-1 pl-2 @max-[25rem]/review:pl-0" onClick={(event) => event.stopPropagation()}>
													<div className="flex items-center gap-1">
              {onOpenFile && target.source?.kind === "snapshot" ? <HeaderActionTooltip label={t("files.readOnlySnapshot")}><span className="@max-[25rem]/review:hidden" tabIndex={0}><Button aria-label={t("files.editFile")} disabled className="size-6" size="icon-sm" type="button" variant="ghost"><Pencil aria-hidden="true" className="size-icon-sm"/></Button></span></HeaderActionTooltip> : null}
														{onOpenFile && !target.source && file.editable && file.fileFingerprint ? (
															<HeaderActionTooltip label={t("files.editFile")}>
																<Button aria-label={t("files.editFile")} className="@max-[25rem]/review:hidden size-6 text-muted-foreground hover:text-foreground" onClick={() => onOpenFile(file.path, { editing: true, mode: "file", scope })} size="icon-sm" type="button" variant="ghost"><Pencil aria-hidden="true" className="size-icon-sm" /></Button>
															</HeaderActionTooltip>
														) : null}
														{onOpenFile ? (
															<HeaderActionTooltip label={t("files.openFullFileGeneric")}>
																<Button aria-label={t("files.openFullFileGeneric")} className="size-6 text-muted-foreground hover:text-foreground" onClick={() => onOpenFile(file.path, { ...fileOpenContext, mode: "file" })} size="icon-sm" type="button" variant="ghost"><FileCode2 aria-hidden="true" className="size-icon-sm" /></Button>
															</HeaderActionTooltip>
														) : null}
														{onOpenFile && canOpenInCenter ? (
															<HeaderActionTooltip label={t("files.openDiffInCenter")}>
																<Button aria-label={t("files.openDiffInCenter")} className="size-6 text-muted-foreground hover:text-foreground" onClick={() => onOpenFile(file.path, { ...fileOpenContext, mode: "diff", center:true })} size="icon-sm" type="button" variant="ghost"><VscodeGoToFileIcon aria-hidden="true" className="size-icon-sm" /></Button>
															</HeaderActionTooltip>
														) : null}
														<HeaderActionTooltip label={t("files.addFeedback")}>
															<Button aria-label={t("files.addFeedback")} aria-pressed={fileAnnotationActive} className={cn("@max-[25rem]/review:hidden size-6 text-muted-foreground hover:text-foreground", fileAnnotationActive && "bg-interactive-active text-foreground")} onClick={() => annotation.begin({ path: file.path, previousPath: file.previousPath, side: "file", scope, surface: "review", workspaceVersion, fileFingerprint: file.fileFingerprint })} size="icon-sm" type="button" variant="ghost"><MessageSquarePlus aria-hidden="true" className="size-icon-sm" /></Button>
														</HeaderActionTooltip>
													</div>
													<DropdownMenu>
														<HeaderActionTooltip label={t("files.moreFileActions")}>
															<DropdownMenuTrigger asChild>
																<Button aria-label={t("files.moreFileActions")} className="hidden size-6 text-muted-foreground hover:text-foreground data-[state=open]:bg-interactive-active data-[state=open]:text-foreground @max-[25rem]/review:inline-flex" size="icon-sm" type="button" variant="ghost"><MoreVertical aria-hidden="true" className="size-icon-sm" /></Button>
															</DropdownMenuTrigger>
														</HeaderActionTooltip>
														<DropdownMenuContent
															align="end"
															onCloseAutoFocus={(event) => {
																if (fileMenuHandoffRef.current) event.preventDefault();
																fileMenuHandoffRef.current = false;
															}}
														>
															{onOpenFile && !target.source && file.editable && file.fileFingerprint ? (
																<DropdownMenuItem className="gap-2" onSelect={() => { fileMenuHandoffRef.current = true; onOpenFile?.(file.path, { editing: true, mode: "file", scope }); }}>
																	<Pencil aria-hidden="true" className="size-icon-sm text-passive" />
																	{t("files.editFile")}
																</DropdownMenuItem>
															) : null}


															<DropdownMenuItem className="gap-2" onSelect={() => { fileMenuHandoffRef.current = true; annotation.begin({ path: file.path, previousPath: file.previousPath, side: "file", scope, surface: "review", workspaceVersion, fileFingerprint: file.fileFingerprint }); }}>
																<MessageSquarePlus aria-hidden="true" className="size-icon-sm text-passive" />
																{t("files.addFeedback")}
															</DropdownMenuItem>
														</DropdownMenuContent>
													</DropdownMenu>
													<HeaderActionTooltip label={isViewed ? t("files.markUnviewed", { file: file.path }) : t("files.markViewed", { file: file.path })}>
														{/* A 24px slot like the buttons beside it keeps the checkbox
														    centred on the header's trailing action column. */}
														<span className="grid size-6 place-items-center">
															<Checkbox
																aria-label={isViewed ? t("files.markUnviewed", { file: file.path }) : t("files.markViewed", { file: file.path })}
																checked={isViewed}
																className="size-4 border border-muted-foreground/70 bg-transparent data-[state=checked]:border-foreground data-[state=checked]:bg-foreground data-[state=checked]:text-on-foreground"
																onCheckedChange={(checked) => markViewed(file, checked === true)}
															/>
														</span>
													</HeaderActionTooltip>
												</div>
											</div>
										</PopoverAnchor>
									</div>
									{/* Whole-file feedback floats under the header's right edge, like the
									    browser's comment box: portaled above the list so no later file
									    header can cover it, and the same in split and unified views. It
									    stays open on outside clicks so a draft isn't lost; the close
									    button, Esc, or this file's feedback button close it. */}
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
							);
						}}
						style={{ height: "100%" }}
					/>
				) : null}
				<SelectionAskButton onAsk={codeSelection.ask} source={codeSelection.source} />
			</div>
				</>
			)}
			<FileAnnotationSendBar annotation={annotation} surface="review" />
		</div>
	);
}

function HeaderActionTooltip({ children, label }: { children: ReactElement; label: string }) {
	return (
		<Tooltip>
			<TooltipTrigger asChild>{children}</TooltipTrigger>
			<TooltipContent side="bottom">{label}</TooltipContent>
		</Tooltip>
	);
}
