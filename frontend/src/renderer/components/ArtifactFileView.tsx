import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { useFileAnnotation } from "../hooks/useFileAnnotation";
import type { ArtifactSave } from "../hooks/useSessionWorkspaceFiles";
import { ArtifactSelectionAsk } from "./ArtifactSelectionAsk";
import { FileContentPane, type FileViewMode } from "./FileContentPane";
import { isDelimitedTextPath } from "../lib/delimited-text";
import { isMermaidPath, isSvgPath } from "../lib/image-files";
import { isMarkdownPreviewPath } from "../lib/markdown-frontmatter";
import type { ArtifactMenuActions } from "../lib/artifact-menu";
import { useArtifactZoomRegistration } from "../lib/artifact-zoom";
import { fileMediaKind } from "../lib/file-media";
import { cn } from "../lib/utils";

function initialModeFor(path: string): FileViewMode {
	return isMarkdownPreviewPath(path) || isSvgPath(path) || isDelimitedTextPath(path) || isMermaidPath(path) ? "rendered" : "file";
}

export function ArtifactFileView({
	artifactName,
	feedbackRequestKey,
	initialEditing = false,
	hostId,
	onFeedbackRequestConsumed,
	path,
	rawUrl,
	readOnly = false,
	cloud = false,
	artifactRoot,
	onAccessRefused,
	save,
	sendMessage,
	sessionId,
	toolbarHost,
	menuActions,
	onEditingChange,
}: {
	artifactName: string;
	feedbackRequestKey?: number;
	initialEditing?: boolean;
	hostId?: string;
	onFeedbackRequestConsumed?: (key: number) => void;
	path: string;
	rawUrl?: string;
	readOnly?: boolean;
	onEditingChange?: (editing: boolean) => void;
	cloud?: boolean;
	artifactRoot?: string;
	onAccessRefused?: () => void;
	save?: ArtifactSave;
	sendMessage?: (message: string) => Promise<void>;
	sessionId: string;
	toolbarHost?: HTMLElement | null;
	menuActions?: ArtifactMenuActions;
}) {
	const annotation = useFileAnnotation(sessionId, { hostId, source: artifactName, sendMessage });
	const source = useMemo(
		() => ({ kind: "artifact" as const, rawUrl, readOnly, cloud, artifactRoot, onAccessRefused, save }),
		[rawUrl, readOnly, cloud, artifactRoot, onAccessRefused, save],
	);
	const scrollRef = useRef<HTMLDivElement>(null);
	const [footerHost, setFooterHost] = useState<HTMLElement | null>(null);
	const [editing, setEditing] = useState(initialEditing && !readOnly);
	const editingChanged = useCallback((next: boolean) => {
		setEditing(next);
		onEditingChange?.(next);
	}, [onEditingChange]);
	const zoomController = useArtifactZoomRegistration(!fileMediaKind(path) && !editing);
	const contentRef = useRef<HTMLDivElement>(null);
	useLayoutEffect(() => {
		const element = scrollRef.current;
		const content = contentRef.current;
		if (!element || !content || !zoomController) return;
		let zoom = 1;
		const apply = () => {
			const next = zoomController.getZoom();
			const anchor = zoomController.takeAnchor();
			if (next === zoom) return;
			const ratio = next / zoom;
			zoom = next;
			content.style.transform = next === 1 ? "" : `translateX(${next < 1 ? `${(1 - next) * 50}%` : "0"}) scale(${next})`;
			element.style.overflowX = next > 1 ? "auto" : "";
			const rect = element.getBoundingClientRect();
			const ax = anchor ? anchor.clientX - rect.left : element.clientWidth / 2;
			const ay = anchor ? anchor.clientY - rect.top : element.clientHeight / 2;
			element.scrollLeft = (element.scrollLeft + ax) * ratio - ax;
			element.scrollTop = (element.scrollTop + ay) * ratio - ay;
		};
		apply();
		const unsubscribe = zoomController.subscribe(apply);
		return () => {
			unsubscribe();
			content.style.transform = "";
			element.style.overflowX = "";
		};
	}, [zoomController]);

	useEffect(() => {
		if (feedbackRequestKey === undefined) return;
		annotation.begin({ path, side: "file", surface: "focused" });
		onFeedbackRequestConsumed?.(feedbackRequestKey);
	}, [feedbackRequestKey, onFeedbackRequestConsumed, path]);

	return (
		<div className={cn("flex h-full min-h-0 flex-col", !fileMediaKind(path) && !isSvgPath(path) ? "ao-document-surface" : "bg-background")}>
			<div className="board-scrollbar min-h-0 flex-1 select-text overflow-x-hidden overflow-y-auto overscroll-contain" data-artifact-selection-scope="" ref={scrollRef}>
				<div className="h-full origin-top-left" ref={contentRef}>
					<FileContentPane annotation={annotation} initialEditing={initialEditing && !readOnly} initialMode={initialEditing && !readOnly ? "file" : initialModeFor(path)} hostId={hostId} path={path} sessionId={sessionId} source={source} split={false} toolbarHost={toolbarHost} menuActions={menuActions} footerHost={footerHost} onEditingChange={editingChanged} />
				</div>
			</div>
			<div className="shrink-0" ref={setFooterHost} />
			<ArtifactSelectionAsk annotation={annotation} containerRef={scrollRef} path={path} />
		</div>
	);
}
