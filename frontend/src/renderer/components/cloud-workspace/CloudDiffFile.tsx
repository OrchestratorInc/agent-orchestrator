import { parsePatchFiles, type DiffLineAnnotation, type FileDiffMetadata } from "@pierre/diffs";
import { FileDiff } from "@pierre/diffs/react";
import { useQuery } from "@tanstack/react-query";
import { useCallback, useEffect, useMemo, useRef, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import type { FileAnnotationRange } from "../../../shared/chat-file-reference";
import type { FilesSource } from "../../hooks/useSessionWorkspaceFiles";
import { useLatestCallback } from "../../hooks/useStable";
import type { CloudCpClient, CloudCpWorkspaceReviewFileSummary, CloudCpWorkspaceReviewScope, CloudCpWorkspaceReviewView } from "../../lib/cloud-cp";
import { workspaceRepositoryOf } from "../../lib/cloud-cp";
import { useAskInChat } from "../../lib/chat-context-bus";
import { parseUnifiedDiff } from "../../lib/diff-parser";
import { fileReferenceSource } from "../../lib/file-reference-source";
import { workspaceSelectionKey } from "../../lib/files-review-selection";
import { useUiStore } from "../../stores/ui-store";
import { FileAnnotationComposer, LineFeedbackButtonControl, RetryButton, type FileAnnotationModel } from "../WorkspaceDiffView";
import { AO_PIERRE_SURFACE_CSS, PIERRE_HIGHLIGHTER } from "../diffs/pierreTheme";
import { SelectionAskButton } from "../diffs/SelectionAskButton";
import { hydratedCopy, patchIdentity } from "../diffs/trailingContext";
import { codeReference, useCodeSelection } from "../diffs/useCodeSelection";
import { useFileAnnotationReveal } from "../diffs/useFileAnnotationReveal";

function metadataForPath(patch: string, path: string): FileDiffMetadata | null {
	if (!patch) return null;
	try {
		const files = parsePatchFiles(patch, path, true).flatMap((entry) => entry.files);
		return files.find((file) => file.name === path || file.prevName === path) ?? null;
	} catch {
		return null;
	}
}

export function CloudDiffFile({
	annotation, annotationRange, baseUrl, chatReferences = true, client, commitSha, fallback, file, onActiveSelectionChange, orgId, patch, scope,
	sessionId, source, split, surface, view, workspaceVersion,
}: {
	annotation: FileAnnotationModel;
	/** A chat reference's lines to reveal: the full file is loaded, so no context stays collapsed around them. */
	annotationRange?: FileAnnotationRange;
	baseUrl: string;
	/** Highlighted code can be added to the chat; off where the path would not name the file in the session. */
	chatReferences?: boolean;
	client: CloudCpClient;
	commitSha?: string;
	fallback?: ReactNode;
	file: CloudCpWorkspaceReviewFileSummary;
	onActiveSelectionChange: (active: boolean) => void;
	orgId: string;
	patch: string;
	scope: CloudCpWorkspaceReviewScope;
	sessionId: string;
	/** What the diff shows, for chat references: a branch's or PR's picked commits, or (unset) the worktree. */
	source?: FilesSource;
	split: boolean;
	/** Where this diff is shown: comment boxes opened here stay apart from the other surface's, as in local review. */
	surface: "focused" | "review";
	/** Another branch or a PR: the full file's revisions are read from it too. */
	view?: CloudCpWorkspaceReviewView;
	workspaceVersion: string;
}) {
	const { t } = useTranslation();
	const resolvedTheme = useUiStore((state) => state.resolvedTheme);
	const containerRef = useRef<HTMLDivElement>(null);
	const metadata = useMemo(() => metadataForPath(patch, file.path), [file.path, patch]);
	const rows = useMemo(() => parseUnifiedDiff(patch), [patch]);
	const otherSurface = surface === "focused" ? "review" : "focused";
	const activeTargets = annotation.targets.filter((target) => target.surface !== otherSurface && target.path === file.path && target.side !== "file" && target.line != null && target.scope === scope);
	const annotations: DiffLineAnnotation<"feedback">[] | undefined = activeTargets.length > 0
		? activeTargets.map((target) => ({ lineNumber: target.line as number, side: target.side === "old" ? "deletions" : "additions", metadata: "feedback" }))
		: undefined;
	useEffect(() => {
		const onSelectionChange = () => {
			const selection = window.getSelection();
			onActiveSelectionChange(Boolean(selection && !selection.isCollapsed && selection.anchorNode && containerRef.current?.contains(selection.anchorNode)));
		};
		document.addEventListener("selectionchange", onSelectionChange);
		return () => { document.removeEventListener("selectionchange", onSelectionChange); onActiveSelectionChange(false); };
	}, [onActiveSelectionChange]);
	const loadDiffFiles = useCallback(async (diff: FileDiffMetadata) => {
		// A view is fenced by its SHAs, not the worktree's version; a revealed
		// reference checks the rows it renders instead of a version.
		const base = view
			? { ...view, path: file.path, scope: "committed" as const }
			: { path: file.path, scope, commitSha, workspaceVersion: annotationRange ? undefined : workspaceVersion };
		const [before, after] = await Promise.all([
			client.getWorkspaceReviewRevision(orgId, sessionId, { ...base, side: "before" }),
			client.getWorkspaceReviewRevision(orgId, sessionId, { ...base, side: "after" }),
		]);
		if (before.binary || after.binary || before.truncated || after.truncated) throw new Error(t("files.error.loadFile"));
		const newFile = { name: file.path, contents: after.content, cacheKey: after.revision ?? workspaceVersion };
		if (diff.type === "rename-pure") return { oldFile: null, newFile };
		return { oldFile: { name: file.previousPath ?? file.path, contents: before.content, cacheKey: before.revision ?? workspaceVersion }, newFile };
	}, [annotationRange, client, commitSha, file.path, file.previousPath, orgId, scope, sessionId, t, view, workspaceVersion]);
	// A revealed reference needs every row of the file around it, so its full
	// contents load up front and no unchanged context stays collapsed.
	const hydration = useQuery({
		queryKey: ["cloud-file-annotation-contents", baseUrl, orgId, sessionId, workspaceRepositoryOf(client) ?? "", scope, commitSha ?? "", view ? workspaceSelectionKey(view, view.commitShas ?? []) : "", file.path, annotationRange && metadata ? patchIdentity(metadata) : "", annotationRange?.requestKey ?? ""] as const,
		queryFn: () => loadDiffFiles(metadata as FileDiffMetadata),
		enabled: Boolean(annotationRange && metadata),
		retry: false,
		refetchOnWindowFocus: false,
		staleTime: Infinity,
	});
	const fileDiff = useMemo(
		() => (annotationRange && metadata && hydration.data ? hydratedCopy(metadata, hydration.data) : null) ?? metadata,
		[annotationRange, hydration.data, metadata],
	);
	const annotationReveal = useFileAnnotationReveal(containerRef, annotationRange, { ready: !annotationRange || !metadata || (hydration.isSuccess && !hydration.isFetching), contentKey: fileDiff });
	const onPostRender = useLatestCallback(() => annotationReveal.reveal());
	const beginLineAnnotation = useCallback((side: "deletions" | "additions", line: number) => {
		const rowIndex = rows.findIndex((row) => (side === "deletions" ? row.oldNo : row.newNo) === line);
		const row = rowIndex >= 0 ? rows[rowIndex] : undefined;
		if (!row || row.kind === "hunk") return;
		annotation.begin({
			path: file.path, previousPath: file.previousPath, side: side === "deletions" ? "old" : "new", line,
			oldLine: row.oldNo ?? undefined, newLine: row.newNo ?? undefined, lineKind: row.kind, lineText: row.text,
			rowIndex, scope, surface, workspaceVersion, fileFingerprint: file.fileFingerprint,
		});
	}, [annotation, file.fileFingerprint, file.path, file.previousPath, rows, scope, surface, workspaceVersion]);

	// Highlighted code gets an "Add to chat" button (or Cmd/Ctrl+L) while the
	// session has a Chat composer, as in a local diff.
	const askInChat = useAskInChat(sessionId);
	// A PR's single picked commit is named by its PR; a branch view or several
	// commits cannot be named by a reference, so their code is not offered.
	const captureSource = fileReferenceSource(source, commitSha);
	const codeSelection = useCodeSelection(containerRef, (selection) => captureSource ? askInChat?.({
		...codeReference(file.path, selection, true), ...captureSource, scope: source?.kind === "snapshot" ? "committed" : scope === "untracked" ? "combined" : scope,
		previousPath: file.previousPath, fileFingerprint: file.fileFingerprint, workspaceVersion,
	}) ?? false : false, chatReferences && askInChat !== undefined && captureSource !== undefined);

	if (!metadata) return <>{fallback}</>;
	return <div className="ao-pierre-surface relative min-w-0 select-text" ref={containerRef}>
		<FileDiff
			disableWorkerPool={typeof Worker === "undefined"}
			fileDiff={fileDiff ?? metadata}
			lineAnnotations={annotations}
			selectedLines={annotationRange ? annotationReveal.selectedLines : undefined}
			options={{
				collapsedContextThreshold: annotationRange ? Number.MAX_SAFE_INTEGER : 8, diffIndicators: "classic", diffStyle: split ? "split" : "unified",
				enableGutterUtility: true, expansionLineCount: 20, hunkSeparators: "line-info", lineDiffType: "word-alt",
				lineHoverHighlight: "line", loadDiffFiles, maxLineDiffLength: 400, onPostRender, overflow: "wrap",
				preferredHighlighter: PIERRE_HIGHLIGHTER,
				theme: { dark: "github-dark", light: "github-light" }, themeType: resolvedTheme,
				tokenizeMaxLength: 200_000, tokenizeMaxLineLength: 2_000, unsafeCSS: AO_PIERRE_SURFACE_CSS,
			}}
			renderAnnotation={(line) => {
				const target = activeTargets.find((open) => open.line === line.lineNumber && (open.side === "old" ? "deletions" : "additions") === line.side);
				return target ? <FileAnnotationComposer annotation={annotation} target={target} /> : null;
			}}
			renderGutterUtility={(getHoveredLine) => <LineFeedbackButtonControl gutter label={t("files.addFeedback")} onClick={() => {
				const line = getHoveredLine();
				if (line) beginLineAnnotation(line.side, line.lineNumber);
			}} />}
		/>
		{annotationRange && hydration.isError ? <div className="px-3 py-2 text-xs text-muted-foreground" role="alert">
			{t("chat.fileAnnotationLoadFailed")}
			<span data-file-annotation-retry={annotationRange.requestKey}><RetryButton onClick={() => void hydration.refetch({ cancelRefetch: false })} /></span>
		</div> : annotationReveal.loading ? <p className="px-3 py-2 text-xs text-muted-foreground" role="status">{t("chat.fileAnnotationLoading")}</p>
		: annotationReveal.changed || annotationReveal.unverified ? <p className="px-3 py-2 text-xs text-muted-foreground" role="status">{t("chat.fileAnnotationChangedDuringOpen")}</p> : null}
		<SelectionAskButton onAsk={codeSelection.ask} onDismiss={codeSelection.dismiss} source={codeSelection.source} />
	</div>;
}
