import { mediaCrossOrigin } from "../lib/file-media";
import { fileReferenceSource } from "../lib/file-reference-source";
import { useCallback, useEffect, useId, useRef, useState } from "react";
import type { FileAnnotationRange } from "../../shared/chat-file-reference";
import { useFileAnnotationReveal } from "./diffs/useFileAnnotationReveal";
import { useTranslation } from "react-i18next";
import { type FileContents, type LineAnnotation } from "@pierre/diffs";
import { File } from "@pierre/diffs/react";
import { getApiBaseUrl } from "../lib/api-client";
import { useAskInChat } from "../lib/chat-context-bus";
import { useHostConnection } from "../hooks/useHostConnection";
import { sessionUiKey } from "../lib/hosts";
import type { WorkspaceDiffScope, WorkspaceFileDetail } from "../hooks/useSessionWorkspaceFiles";
import { useUiStore } from "../stores/ui-store";
import { FileAnnotationComposer, LineFeedbackButtonControl, PanelMessage, type FileAnnotationModel } from "./WorkspaceDiffView";
import { AO_PIERRE_SURFACE_CSS } from "./diffs/pierreTheme";
import { SelectionAskButton } from "./diffs/SelectionAskButton";
import { codeReference, useCodeSelection } from "./diffs/useCodeSelection";
import { usePersistentGutterUtility } from "./diffs/usePersistentGutterUtility";

/** How long a revealed line stays marked, matching the chat and settings focus highlights. */
export const REVEALED_LINE_HIGHLIGHT_MS = 2_000;

export function formatBytes(bytes: number): string {
	if (bytes < 1024) return `${bytes} B`;
	if (bytes < 1024 * 1024) return `${Math.round(bytes / 1024)} KB`;
	return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}

function workspaceRawImageUrl(sessionId: string, path: string, side: "before" | "after", hostId?: string, remoteBaseUrl?: string): string | undefined {
	const base = hostId ? remoteBaseUrl : getApiBaseUrl();
	if (base === undefined) return undefined;
	const query = new URLSearchParams({ path, side });
	return `${base}/api/v1/sessions/${encodeURIComponent(sessionId)}/workspace/file/blob?${query}`;
}

// Renders an untouched (unmodified) workspace file: an agent didn't write
// this one, so there's no diff to show, just its current content. Binary and
// oversized files short-circuit before any tokenization is attempted.
export function ReadOnlyFileView({
	annotation,
	annotationRange,
 annotationReady = true,
	detail,
	editing = false,
	onContentReady,
	onEditChange,
	onRevealLineConsumed,
	revealLine,
	scope = "combined",
	source = { kind: "workspace" },
	commitSha,
	sessionId,
	hostId,
	imageUrl,
	side = "after",
}: {
	annotation: FileAnnotationModel;
	annotationRange?: FileAnnotationRange;
 annotationReady?: boolean;
	detail: WorkspaceFileDetail;
	editing?: boolean;
	onContentReady?: () => void;
	onEditChange?: (content: string) => void;
	onRevealLineConsumed?: (requestKey: number) => void;
	revealLine?: { line: number; requestKey: number };
	scope?: WorkspaceDiffScope;
	source?: import("../hooks/useSessionWorkspaceFiles").FilesSource;
	commitSha?: string;
	sessionId: string;
	hostId?: string;
	/** Where to load an image file from, when not the workspace blob route (an artifact's raw URL). */
	imageUrl?: string;
	side?: "before" | "after";
}) {
	const { t } = useTranslation();
	const { baseUrl: remoteBaseUrl } = useHostConnection(hostId);
	const resolvedTheme = useUiStore((state) => state.resolvedTheme);
	const containerRef = useRef<HTMLDivElement>(null);
	const annotationReveal=useFileAnnotationReveal(containerRef,editing ? undefined : annotationRange, {ready:annotationReady,contentKey:detail.content});
	const pendingRevealRef = useRef(revealLine);
	const consumedRevealRequestKeysRef = useRef(new Set<number>());
	// The line a reveal brought into view, marked briefly so the eye lands on it.
	const [highlightedLine, setHighlightedLine] = useState<number | null>(null);
	const editorInstanceId = useId();
	const gutterHover = usePersistentGutterUtility(containerRef);
	const revealRequestedLine = useCallback(() => {
		const target = pendingRevealRef.current;
		if (!target) return;
		if (consumedRevealRequestKeysRef.current.has(target.requestKey)) {
			pendingRevealRef.current = undefined;
			return;
		}
		const diffsContainer = containerRef.current?.querySelector("diffs-container");
		const line = diffsContainer?.shadowRoot?.querySelector<HTMLElement>(`[data-line="${target.line}"]`);
		if (!line) return;
		line.scrollIntoView({ block: "center" });
		setHighlightedLine(target.line);
		consumedRevealRequestKeysRef.current.add(target.requestKey);
		pendingRevealRef.current = undefined;
		onRevealLineConsumed?.(target.requestKey);
	}, [onRevealLineConsumed]);
	useEffect(() => {
		if (!revealLine || consumedRevealRequestKeysRef.current.has(revealLine.requestKey)) {
			pendingRevealRef.current = undefined;
			return;
		}
		pendingRevealRef.current = revealLine;
		const frame = requestAnimationFrame(revealRequestedLine);
		return () => cancelAnimationFrame(frame);
	}, [revealLine?.line, revealLine?.requestKey, revealRequestedLine]);
	useEffect(() => {
		if (highlightedLine === null) return;
		const timer = window.setTimeout(() => setHighlightedLine(null), REVEALED_LINE_HIGHLIGHT_MS);
		return () => window.clearTimeout(timer);
	}, [highlightedLine]);
	// Highlighted code gets an "Ask in chat" button (or Cmd/Ctrl+L) while the
	// session has a Chat composer; in edit mode a selection belongs to the editor.
	const askInChat = useAskInChat(sessionId, hostId);
	const captureSource = fileReferenceSource(source, commitSha);
	const codeSelection = useCodeSelection(containerRef, (selection) => captureSource ? askInChat?.({ ...codeReference(detail.path, selection, false), side: side === "before" ? "old" : "file", endSide: side === "before" ? "old" : "file", ...captureSource, scope: source.kind === "snapshot" ? "committed" : scope, previousPath: detail.previousPath, fileFingerprint: detail.fileFingerprint, workspaceVersion: detail.workspaceVersion }) ?? false : false, askInChat !== undefined && captureSource !== undefined && !editing);
	if (detail.binary) {
		if (detail.imageMediaType) {
			return (
				<div className="grid place-items-center p-3">
					<img
						crossOrigin={mediaCrossOrigin(imageUrl ?? workspaceRawImageUrl(sessionId, detail.path, side, hostId, remoteBaseUrl))}
						alt={detail.path}
						className="max-h-[70vh] max-w-full object-contain"
						src={imageUrl ?? workspaceRawImageUrl(sessionId, detail.path, side, hostId, remoteBaseUrl)}
					/>
				</div>
			);
		}
		return <PanelMessage>{t("files.binaryUnavailable")}</PanelMessage>;
	}
	if (detail.contentTruncated) {
		return <PanelMessage>{t("files.explorer.tooLarge", { size: formatBytes(detail.size) })}</PanelMessage>;
	}
	const activeTargets = annotation.targets.filter((target) => target.surface !== "review" && target.path === detail.path && target.side === "file" && target.line != null);
	const lineAnnotations: LineAnnotation<"feedback">[] | undefined = activeTargets.length > 0
		? activeTargets.map((target) => ({ lineNumber: target.line as number, metadata: "feedback" }))
		: undefined;
	const file: FileContents = {
		name: detail.path,
		contents: detail.content,
		cacheKey: detail.fileFingerprint ?? detail.workspaceVersion ?? `${detail.path}:${detail.size}`,
	};
	const beginLineAnnotation = (line: number) => {
		annotation.begin({
			path: detail.path,
			previousPath: detail.previousPath,
			side: "file",
			line,
			newLine: side === "after" ? line : undefined,
			oldLine: side === "before" ? line : undefined,
			lineKind: "context",
			lineText: detail.content.replace(/\n$/, "").split("\n")[line - 1] ?? "",
			scope,
			surface: "focused",
			workspaceVersion: detail.workspaceVersion,
			fileFingerprint: detail.fileFingerprint,
		});
	};
	return (
		<div
			className="ao-pierre-surface min-w-0 select-text"
			data-editing={editing || undefined}
			onPointerLeave={gutterHover.onPointerLeave}
			onPointerMove={gutterHover.onPointerMove}
			ref={containerRef}
		>
			<File<"feedback">
				disableWorkerPool={typeof Worker === "undefined"}
				edit={editing}
				editStateKey={`${sessionUiKey(sessionId, hostId)}:${detail.path}:file:${editorInstanceId}`}
				editorOptions={{
					onAttach: (editor) => requestAnimationFrame(() => editor.focus({ lineNumber: "first-visible" })),
					ownsVerticalViewport: true,
				}}
				file={file}
				lineAnnotations={lineAnnotations}
				selectedLines={annotationRange ? annotationReveal.selectedLines : highlightedLine === null ? null : { start: highlightedLine, end: highlightedLine }}
				options={{
					disableFileHeader: true,
					enableGutterUtility: true,
					lineHoverHighlight: "line",
					onPostRender: () => {
						gutterHover.restoreAfterRender();
						onContentReady?.();
						revealRequestedLine();
						annotationReveal.reveal();
					},
					overflow: "wrap",
					theme: { dark: "github-dark", light: "github-light" },
					themeType: resolvedTheme,
					tokenizeMaxLength: 200_000,
					tokenizeMaxLineLength: 2_000,
					unsafeCSS: AO_PIERRE_SURFACE_CSS,
				}}
				renderAnnotation={(line) => {
					const target = activeTargets.find((open) => open.line === line.lineNumber);
					return target ? <FileAnnotationComposer annotation={annotation} target={target} /> : null;
				}}
				onEditChange={(event) => onEditChange?.(event.file.contents)}
				onEditComplete={() => "reject"}
				renderGutterUtility={(getHoveredLine) => (
					<LineFeedbackButtonControl gutter label={t("files.addFeedback")} onClick={() => {
						const line = getHoveredLine();
						if (line) beginLineAnnotation(line.lineNumber);
					}} />
				)}
			/>
			{annotationReveal.loading ? <p role="status" className="px-3 py-2 text-xs text-muted-foreground">{t("chat.fileAnnotationLoading")}</p>
			: annotationReveal.changed || annotationReveal.unverified ? <p role="status" className="px-3 py-2 text-xs text-muted-foreground">{t("chat.fileAnnotationChangedDuringOpen")}</p> : null}
			<SelectionAskButton onAsk={codeSelection.ask} source={codeSelection.source} />
		</div>
	);
}
