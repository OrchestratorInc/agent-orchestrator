import type { FileAnnotationRange } from "../../../shared/chat-file-reference";
import { useFileAnnotationReveal } from "./useFileAnnotationReveal";
import { useCallback, useEffect, useMemo, useRef, type ReactNode } from "react";
import { parsePatchFiles, type DiffLineAnnotation, type FileDiffMetadata } from "@pierre/diffs";
import { FileDiff } from "@pierre/diffs/react";
import { useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { fetchPRFileRevision, fetchWorkspaceFileRevision, type FilesSource, type WorkspaceDiffScope, type WorkspaceFileDetail } from "../../hooks/useSessionWorkspaceFiles";
import { parseUnifiedDiff, type DiffRow } from "../../lib/diff-parser";
import { useAskInChat } from "../../lib/chat-context-bus";
import { fileReferenceSource } from "../../lib/file-reference-source";
import { useUiStore } from "../../stores/ui-store";
import { FileAnnotationComposer, LineFeedbackButtonControl, RetryButton, type FileAnnotationModel } from "../WorkspaceDiffView";
import { AO_PIERRE_SURFACE_CSS, PIERRE_HIGHLIGHTER } from "./pierreTheme";
import { SelectionAskButton } from "./SelectionAskButton";
import { endsAtLastHunk, hydratedCopy, patchIdentity } from "./trailingContext";
import { codeReference, useCodeSelection } from "./useCodeSelection";
import { usePersistentGutterUtility } from "./usePersistentGutterUtility";

const metadataCache = new Map<string, FileDiffMetadata>();
const MAX_METADATA_CACHE_ENTRIES = 100;
// Same bound as the review list: bigger files keep the "more context" row and
// load on click instead of being fetched up front.
const END_OF_FILE_PREFETCH_MAX_BYTES = 128 * 1024;

function cachedMetadata(detail: WorkspaceFileDetail): FileDiffMetadata | null {
	if (!detail.diff) return null;
	const key = `${detail.fileFingerprint}:${detail.diff}`;
	const existing = metadataCache.get(key);
	if (existing) return existing;
	try {
		const parsed = parsePatchFiles(detail.diff, detail.fileFingerprint || detail.path, true);
		const metadata = parsed.flatMap((patch) => patch.files)[0] ?? null;
		if (!metadata) return null;
		metadataCache.set(key, metadata);
		if (metadataCache.size > MAX_METADATA_CACHE_ENTRIES) {
			const oldest = metadataCache.keys().next().value;
			if (oldest) metadataCache.delete(oldest);
		}
		return metadata;
	} catch {
		return null;
	}
}

function rowForLine(rows: DiffRow[], side: "deletions" | "additions", lineNumber: number) {
	const rowIndex = rows.findIndex((row) => (side === "deletions" ? row.oldNo : row.newNo) === lineNumber);
	return { row: rowIndex >= 0 ? rows[rowIndex] : undefined, rowIndex };
}

export function AoDiffFile({
	annotation,
	annotationRange,
 annotationReady = true,
	detail,
	fallback,
	onActiveSelectionChange,
	scope = "combined",
	sessionId,
	hostId,
	split,
	commitSha,
	source = { kind: "workspace" },
	hideFileHeader = false,
	extraCSS = "",
}: {
	annotation: FileAnnotationModel;
	annotationRange?: FileAnnotationRange;
 annotationReady?: boolean;
	detail: WorkspaceFileDetail;
	fallback: ReactNode;
	onActiveSelectionChange: (active: boolean) => void;
	scope?: WorkspaceDiffScope;
	sessionId: string;
	hostId?: string;
	split: boolean;
	commitSha?: string;
	source?: FilesSource;
	/** Opt-in (Files panel preview): the caller's toolbar already names the file. */
	hideFileHeader?: boolean;
	/** Opt-in extra surface CSS appended after the shared surface CSS. */
	extraCSS?: string;
}) {
	const { t } = useTranslation();
	const resolvedTheme = useUiStore((state) => state.resolvedTheme);
	const containerRef = useRef<HTMLDivElement>(null);
	const gutterHover = usePersistentGutterUtility(containerRef);
	const metadata = useMemo(() => cachedMetadata(detail), [detail]);
	const rows = useMemo(() => parseUnifiedDiff(detail.diff), [detail.diff]);
	const activeTargets = annotation.targets.filter((target) => target.surface !== "review" && target.path === detail.path && target.side !== "file" && target.line != null);
	const lineAnnotations: DiffLineAnnotation<"feedback">[] | undefined = activeTargets.length > 0
		? activeTargets.map((target) => ({ lineNumber: target.line as number, side: target.side === "old" ? "deletions" : "additions", metadata: "feedback" }))
		: undefined;

	useEffect(() => {
		const onSelectionChange = () => {
			const selection = window.getSelection();
			onActiveSelectionChange(Boolean(selection && !selection.isCollapsed && selection.anchorNode && containerRef.current?.contains(selection.anchorNode)));
		};
		document.addEventListener("selectionchange", onSelectionChange);
		return () => {
			document.removeEventListener("selectionchange", onSelectionChange);
			onActiveSelectionChange(false);
		};
	}, [onActiveSelectionChange]);

	const loadDiffFiles = useCallback(
		async (fileDiff: FileDiffMetadata) => {
			const loadRevision = (side: "before" | "after") => {
    if(source.kind === "pull_request") return fetchPRFileRevision(sessionId,source.number,source.url,detail.path,side,commitSha,hostId);
    return fetchWorkspaceFileRevision({
     commitSha,sessionId,path:detail.path,side,hostId,
     scope:source.kind === "snapshot" ? "committed" : scope,
     // Detail fingerprints and manifest versions are different fences. For
     // annotation navigation verify fresh rendered rows instead of sending a
     // detail hint to the manifest-fenced revision endpoint.
     workspaceVersion:source.kind === "snapshot" || annotationRange ? undefined : detail.workspaceVersion,
     view:source.kind === "snapshot" ? source.view : undefined,
     commitShas:source.kind === "snapshot" ? source.commitShas : undefined,
    });
   };
   const [before,after]=await Promise.all([loadRevision("before"),loadRevision("after")]);
			if (before.binary || after.binary || before.truncated || after.truncated) {
				throw new Error(t("files.explorer.tooLarge", { size: Math.max(before.size, after.size) }));
			}
			const newFile = { name: detail.path, contents: after.content, cacheKey: after.revision };
			if (fileDiff.type === "rename-pure") return { oldFile: null, newFile };
			return {
				oldFile: { name: detail.previousPath || detail.path, contents: before.content, cacheKey: before.revision },
				newFile,
			};
		},
		[commitSha, detail.path, detail.previousPath, detail.workspaceVersion, scope, sessionId, hostId, source, t, annotationRange],
	);
	// The detail patch is git's --unified=3, which endsAtLastHunk assumes. When it
	// proves the file ends at the last hunk, load the full contents up front so
	// Pierre has no dead "More unchanged context may be available" row to draw.
	const endsAtEndOfFile = metadata != null && detail.size <= END_OF_FILE_PREFETCH_MAX_BYTES && endsAtLastHunk(metadata);
	const endOfFileContents = useQuery({
		queryKey: hostId ? ["files-preview-end-of-file", hostId, sessionId, source.kind === "snapshot" ? JSON.stringify(source) : source.kind === "pull_request" ? source.url : "workspace", scope, commitSha ?? "", detail.path, detail.fileFingerprint ?? "", detail.workspaceVersion ?? "", source.kind === "pull_request" ? `${source.number}:${source.snapshot ?? ""}` : "", metadata && (endsAtEndOfFile || annotationRange) ? patchIdentity(metadata) : "", annotationRange?.requestKey ?? ""] as const
			: ["files-preview-end-of-file", sessionId, source.kind === "snapshot" ? JSON.stringify(source) : source.kind === "pull_request" ? source.url : "workspace", scope, commitSha ?? "", detail.path, detail.fileFingerprint ?? "", detail.workspaceVersion ?? "", source.kind === "pull_request" ? `${source.number}:${source.snapshot ?? ""}` : "", metadata && (endsAtEndOfFile || annotationRange) ? patchIdentity(metadata) : "", annotationRange?.requestKey ?? ""] as const,
		queryFn: () => loadDiffFiles(metadata as FileDiffMetadata),
		enabled: Boolean(metadata) && (endsAtEndOfFile || Boolean(annotationRange)),
		retry: false,
		refetchOnWindowFocus: false,
		staleTime: Infinity,
	});
	const fileDiff = useMemo(
		() => ((endsAtEndOfFile || annotationRange) && metadata && endOfFileContents.data ? hydratedCopy(metadata, endOfFileContents.data) : null) ?? metadata,
		[endOfFileContents.data, endsAtEndOfFile, annotationRange, metadata],
	);
 const annotationReveal=useFileAnnotationReveal(containerRef,annotationRange,{ready:annotationReady && (!annotationRange || !metadata || (endOfFileContents.isSuccess && !endOfFileContents.isFetching)),contentKey:fileDiff});
	const beginLineAnnotation = useCallback((side: "deletions" | "additions", lineNumber: number) => {
		const { row, rowIndex } = rowForLine(rows, side, lineNumber);
		if (!row || row.kind === "hunk") return;
		annotation.begin({
			path: detail.path,
			previousPath: detail.previousPath,
			side: side === "deletions" ? "old" : "new",
			line: lineNumber,
			oldLine: row.oldNo ?? undefined,
			newLine: row.newNo ?? undefined,
			lineKind: row.kind,
			lineText: row.text,
			rowIndex,
			scope,
			surface: "focused",
			workspaceVersion: detail.workspaceVersion,
			fileFingerprint: detail.fileFingerprint,
		});
	}, [annotation, detail.fileFingerprint, detail.path, detail.previousPath, detail.workspaceVersion, rows, scope]);

	// Highlighted code gets an "Ask in chat" button (or Cmd/Ctrl+L) while the
	// session has a Chat composer.
	const askInChat = useAskInChat(sessionId, hostId);
	const captureSource = fileReferenceSource(source, commitSha);
	const codeSelection = useCodeSelection(containerRef, (selection) => captureSource ? askInChat?.({ ...codeReference(detail.path, selection, true), ...captureSource, scope: source.kind === "snapshot" ? "committed" : scope, previousPath: detail.previousPath, fileFingerprint: detail.fileFingerprint, workspaceVersion: detail.workspaceVersion }) ?? false : false, askInChat !== undefined && captureSource !== undefined);

	if (!metadata) return <>{fallback}</>;

	return (
		<div
			className="ao-pierre-surface relative min-w-0 select-text"
			onPointerLeave={gutterHover.onPointerLeave}
			onPointerMove={gutterHover.onPointerMove}
			ref={containerRef}
		>
			<FileDiff
				disableWorkerPool={typeof Worker === "undefined"}
				fileDiff={fileDiff ?? metadata}
				selectedLines={annotationReveal.selectedLines}
				lineAnnotations={lineAnnotations}
				options={{
					collapsedContextThreshold: annotationRange ? Number.MAX_SAFE_INTEGER : 8,
					diffIndicators: "classic",
					diffStyle: split ? "split" : "unified",
					disableFileHeader: hideFileHeader,
					enableGutterUtility: true,
					expansionLineCount: 20,
					hunkSeparators: "line-info",
					lineDiffType: "word-alt",
					loadDiffFiles,
					maxLineDiffLength: 400,
					lineHoverHighlight: "line",
					onPostRender: () => { gutterHover.restoreAfterRender(); annotationReveal.reveal(); },
					overflow: "wrap",
					preferredHighlighter: PIERRE_HIGHLIGHTER,
					theme: { dark: "github-dark", light: "github-light" },
					themeType: resolvedTheme,
					tokenizeMaxLength: 200_000,
					tokenizeMaxLineLength: 2_000,
					unsafeCSS: AO_PIERRE_SURFACE_CSS + extraCSS,
				}}
				renderAnnotation={(line) => {
					const target = activeTargets.find((open) => open.line === line.lineNumber && (open.side === "old" ? "deletions" : "additions") === line.side);
					return target ? <FileAnnotationComposer annotation={annotation} target={target} /> : null;
				}}
				renderGutterUtility={(getHoveredLine) => (
					<LineFeedbackButtonControl
						gutter
						label={t("files.addFeedback")}
						onClick={() => {
							const line = getHoveredLine();
							if (line) beginLineAnnotation(line.side, line.lineNumber);
						}}
					/>
				)}
			/>
			{annotationRange && endOfFileContents.isError ? <div role="alert" className="px-3 py-2 text-xs text-muted-foreground">
				{t("chat.fileAnnotationLoadFailed")}
				<span data-file-annotation-retry={annotationRange.requestKey}><RetryButton onClick={() => void endOfFileContents.refetch({cancelRefetch:false})}/></span>
			</div> : annotationReveal.loading ? <p role="status" className="px-3 py-2 text-xs text-muted-foreground">{t("chat.fileAnnotationLoading")}</p>
			: annotationReveal.changed || annotationReveal.unverified ? <p role="status" className="px-3 py-2 text-xs text-muted-foreground">{t("chat.fileAnnotationChangedDuringOpen")}</p> : null}
			<SelectionAskButton onAsk={codeSelection.ask} source={codeSelection.source} />
			{detail.diffTruncated ? (
				<div className="border-t border-border bg-warning/10 px-3 py-1.5 text-xs text-warning">
					{t("files.diffTruncated")}
				</div>
			) : null}
		</div>
	);
}
