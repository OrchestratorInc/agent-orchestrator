import { useFileAnnotation } from "../hooks/useFileAnnotation";
import { useCallback, useEffect, useLayoutEffect, useRef } from "react";
import { FileContentPane } from "./FileContentPane";
import type { FileAnnotationModel } from "./WorkspaceDiffView";
import type { FileOpenOptions, FileViewMode } from "./FileContentPane";
import type { WorkspaceDiffScope } from "../hooks/useSessionWorkspaceFiles";

export function SessionFileWorkspace({
	active = true,
	fileKey,
	source,
	previousPath,
	annotation,
	commitSha,
	hostId,
	initialEditing = false,
	initialLine,
	initialMode = "file",
	initialRequestKey = 0,
	onDirtyChange,
	onInitialEditingConsumed,
	onInitialLineConsumed,
	path,
	sessionId,
	split,
	scope = "combined",
}: {
	annotation: FileAnnotationModel;
	active?: boolean;
	fileKey?: string;
	source?: FileOpenOptions["source"];
	previousPath?: string;
	commitSha?: string;
	hostId?: string;
	initialEditing?: boolean;
	initialLine?: number;
	initialMode?: FileViewMode;
	initialRequestKey?: number;
	onDirtyChange?: (path: string, dirty: boolean) => void;
	onInitialEditingConsumed?: (path: string, requestKey: number) => void;
	onInitialLineConsumed?: (path: string, requestKey: number) => void;
	path: string;
	sessionId: string;
	split: boolean;
	scope?: WorkspaceDiffScope;
}) {
	const snapshotAnnotation = useFileAnnotation(sessionId, {
		hostId,
		source:
			source?.kind === "snapshot" ? `${source.label} (tip ${source.view?.expectedTipSha ?? "selected revision"}; review base ${source.view?.expectedBaseSha ?? "selected history"}; commits ${source.commitShas?.join(",") || "all"})` : "Workspace",
	});
	const identity = fileKey ?? path;
	const scrollRef = useRef<HTMLDivElement>(null);
	useEffect(() => {
		if (!active || initialEditing) return;
		const frame = requestAnimationFrame(() => scrollRef.current?.focus({ preventScroll: true }));
		return () => cancelAnimationFrame(frame);
	}, [active, initialEditing, initialRequestKey]);
	const pendingScrollRestoreRef = useRef<number | null>(null);
	const restoreFrameRef = useRef<number | null>(null);
	const scrollKey = `${hostId ?? "local"}:${sessionId}:${JSON.stringify(source)}:${path}`;
	const handleDirtyChange = useCallback((dirty: boolean) => onDirtyChange?.(identity, dirty), [onDirtyChange, identity]);
	const handleLineConsumed = useCallback((requestKey: number) => onInitialLineConsumed?.(identity, requestKey), [onInitialLineConsumed, identity]);
	const restoreScrollPosition = useCallback(
		(finish = false) => {
			const scroll = scrollRef.current;
			const target = pendingScrollRestoreRef.current;
			if (!scroll || target == null) return;
			const restoredTarget = finish ? Math.min(target, Math.max(0, scroll.scrollHeight - scroll.clientHeight)) : target;
			scroll.scrollTop = restoredTarget;
			if (finish || scroll.scrollTop === target) {
				pendingScrollRestoreRef.current = null;
				rememberFileScrollPosition(scrollKey, scroll.scrollTop);
			}
		},
		[scrollKey],
	);
	const handleContentReady = useCallback(() => {
		restoreScrollPosition();
		if (pendingScrollRestoreRef.current == null) return;
		if (restoreFrameRef.current !== null) cancelAnimationFrame(restoreFrameRef.current);
		restoreFrameRef.current = requestAnimationFrame(() => {
			restoreFrameRef.current = null;
			restoreScrollPosition(true);
		});
	}, [restoreScrollPosition]);
	useEffect(
		() => () => {
			if (initialEditing) onInitialEditingConsumed?.(identity, initialRequestKey);
		},
		[initialEditing, initialRequestKey, onInitialEditingConsumed, identity],
	);
	useLayoutEffect(() => {
		pendingScrollRestoreRef.current = fileScrollPositions.get(scrollKey) ?? 0;
		restoreScrollPosition();
		return () => {
			if (restoreFrameRef.current !== null) cancelAnimationFrame(restoreFrameRef.current);
			restoreFrameRef.current = null;
		};
	}, [restoreScrollPosition, scrollKey]);
	return (
		<section className="relative flex h-full min-h-0 flex-col bg-background" data-testid="session-file-workspace">
			<div
				className="board-scrollbar min-h-0 flex-1 overflow-x-hidden overflow-y-auto overscroll-contain"
				data-testid="session-file-scroll"
				role="region"
				aria-label={path}
				tabIndex={-1}
				onScroll={(event) => {
					const pending = pendingScrollRestoreRef.current;
					if (pending != null && event.currentTarget.scrollTop !== pending) return;
					pendingScrollRestoreRef.current = null;
					rememberFileScrollPosition(scrollKey, event.currentTarget.scrollTop);
				}}
				ref={scrollRef}
			>
				<FileContentPane
					active={active}
					source={source}
					previousPath={previousPath}
					annotation={source?.kind === "snapshot" ? snapshotAnnotation : annotation}
					commitSha={commitSha}
					hostId={hostId}
					rememberDisplayMode
					initialEditing={initialEditing}
					initialLine={initialLine}
					initialMode={initialMode}
					initialRequestKey={initialRequestKey}
					onDirtyChange={handleDirtyChange}
					onContentReady={handleContentReady}
					onInitialLineConsumed={handleLineConsumed}
					path={path}
					sessionId={sessionId}
					split={split}
					scope={scope}
				/>
			</div>
		</section>
	);
}

const fileScrollPositions = new Map<string, number>();
const maxRememberedFileScrollPositions = 256;

function rememberFileScrollPosition(key: string, scrollTop: number) {
	fileScrollPositions.delete(key);
	fileScrollPositions.set(key, scrollTop);
	if (fileScrollPositions.size <= maxRememberedFileScrollPositions) return;
	const oldestKey = fileScrollPositions.keys().next().value;
	if (oldestKey) fileScrollPositions.delete(oldestKey);
}
