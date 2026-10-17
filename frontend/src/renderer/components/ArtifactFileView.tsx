import { useEffect, useMemo, useRef } from "react";
import { useFileAnnotation } from "../hooks/useFileAnnotation";
import { ArtifactSelectionAsk } from "./ArtifactSelectionAsk";
import { FileContentPane, type FileViewMode } from "./FileContentPane";
import { isSvgPath } from "../lib/image-files";

// Mirrors FileContentPane's own canRenderMarkdown/canRenderSvg extension checks:
// a markdown or SVG artifact should open already rendered, the same way GitHub
// opens a README, rather than making the reader click over to "Rendered".
function initialModeFor(path: string): FileViewMode {
	return /\.(md|markdown)$/i.test(path) || isSvgPath(path) ? "rendered" : "file";
}

/**
 * Content view for one file in a session's artifact directory, reusing the
 * same `FileContentPane` the workspace/PR Files flow uses (source-agnostic:
 * dispatches on `source.kind`). Local artifacts fetch raw content through the
 * daemon API with the artifact root selected; remote and cloud artifacts keep
 * their provided content URL. Neither uses the workspace-diff machinery, since artifacts
 * live outside the git workspace and have no diff/status.
 *
 * One view, no display mode switch: a markdown artifact renders, any other
 * text file shows as code. A complete text file can be edited (saved through
 * the artifact write endpoint) unless `readOnly`. Text selected in the
 * rendered markdown can be sent to the worker.
 */
export function ArtifactFileView({
	artifactName,
	feedbackRequestKey,
	hostId,
	onFeedbackRequestConsumed,
	path,
	rawUrl,
	readOnly = false,
	sessionId,
	toolbarHost,
}: {
	artifactName: string;
	feedbackRequestKey?: number;
	hostId?: string;
	onFeedbackRequestConsumed?: (key: number) => void;
	path: string;
	rawUrl?: string;
	/** Hides the edit action, as for a cloud session's artifacts. */
	readOnly?: boolean;
	sessionId: string;
	/** The header the pane renders its mode and action buttons into. */
	toolbarHost?: HTMLElement | null;
}) {
	const annotation = useFileAnnotation(sessionId, { hostId, source: artifactName });
	const source = useMemo(() => ({ kind: "artifact" as const, rawUrl, readOnly }), [rawUrl, readOnly]);
	const scrollRef = useRef<HTMLDivElement>(null);

	useEffect(() => {
		if (feedbackRequestKey === undefined) return;
		annotation.begin({ path, side: "file", surface: "focused" });
		onFeedbackRequestConsumed?.(feedbackRequestKey);
		// This effect is intentionally keyed to the external one-shot request,
		// not the annotation model object, which changes when the composer opens.
		// The SessionView owner clears the feedback bit after this callback.
	}, [feedbackRequestKey, onFeedbackRequestConsumed, path]);

	return (
		<div className="flex h-full min-h-0 flex-col bg-background">
			{/* The app shell is unselectable; an artifact's text is meant to be selected. */}
			<div className="board-scrollbar min-h-0 flex-1 select-text overflow-x-hidden overflow-y-auto overscroll-contain" ref={scrollRef}>
				<FileContentPane annotation={annotation} initialMode={initialModeFor(path)} hostId={hostId} path={path} sessionId={sessionId} source={source} split={false} toolbarHost={toolbarHost} />
			</div>
			<ArtifactSelectionAsk annotation={annotation} containerRef={scrollRef} path={path} />
		</div>
	);
}
