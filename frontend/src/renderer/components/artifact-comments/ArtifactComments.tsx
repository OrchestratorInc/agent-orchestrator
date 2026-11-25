import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { MessageSquare } from "@/components/app-icons";
import { cn } from "../../lib/utils";
import { SharedArtifactFrame } from "../SharedArtifactFrame";
import { Button } from "../ui/button";
import { Tooltip, TooltipContent, TooltipTrigger } from "../ui/tooltip";
import { PanelMessage } from "../WorkspaceDiffView";
import { ArtifactCommentsPanel } from "./ArtifactCommentsPanel";
import {
	artifactCommentMarkers,
	groupArtifactCommentThreads,
	placeCommentsPopover,
	type CommentsSelectionRect,
	type ArtifactCommentAnchor,
	type ArtifactCommentsPickKind,
} from "../../../shared/artifact-comments";
import type { ArtifactCommentsSendTarget } from "./SendThreadToAgentDialog";
import { useArtifactComments } from "./useArtifactComments";
import { useCommentBridge } from "./useCommentBridge";

/** Files whose published page takes comments: Markdown and HTML. */
export function artifactCommentsKind(path: string): ArtifactCommentsPickKind | null {
	if (/\.(md|markdown)$/i.test(path)) return "markdown";
	if (/\.html?$/i.test(path)) return "html";
	return null;
}

/**
 * The viewer header's Comments button, between Publish and Full screen, with
 * the published version's open-thread count. Pressed while comments are open.
 */
export function ArtifactCommentsButton({ onToggle, open, slug }: { onToggle: () => void; open: boolean; slug: string }) {
	const { t } = useTranslation();
	const { openCount } = useArtifactComments(slug);
	const label = openCount > 0 ? t("artifactComments.buttonCount", { n: openCount }) : t("artifactComments.button");
	return (
		<Tooltip>
			<TooltipTrigger asChild>
				<Button
					aria-label={label}
					aria-pressed={open}
					className={cn("relative", open && "bg-(--color-accent-weak) text-foreground")}
					onClick={onToggle}
					size="icon-sm"
					type="button"
					variant="quiet"
				>
					<MessageSquare aria-hidden="true" className="size-icon-sm" />
					{openCount > 0 ? (
						<span
							aria-hidden="true"
							className="pointer-events-none absolute -top-0.5 -right-0.5 min-w-3.5 rounded-full bg-logo-accent px-1 text-center text-[10px] leading-3.5 font-medium text-logo-accent-foreground tabular-nums"
							data-testid="artifact-comments-count"
						>
							{openCount}
						</span>
					) : null}
				</Button>
			</TooltipTrigger>
			<TooltipContent side="bottom">{t("artifactComments.button")}</TooltipContent>
		</Tooltip>
	);
}

/**
 * The open file's published page in comment mode: the same bytes the link
 * shows, framed from its share host with this account's view token. Elements
 * with comments carry a count; the page outlines what's under the pointer.
 * Clicking an element's count opens a popover beside it with that element's
 * comments; clicking the element itself opens one with a box for a new
 * comment on it; nothing else pops up. Comments
 * belong to the published version, so the local file is not shown meanwhile;
 * a notice says so when the local file has changed since publishing.
 */
export function ArtifactCommentsWorkspace({ kind, onClose, onShown, outdated, sendTarget, slug, title }: {
	kind: ArtifactCommentsPickKind;
	/** Leaves comment mode. */
	onClose: () => void;
	/** Called once there is something to see: the published page loaded, or a message in its place. */
	onShown?: () => void;
	/** The local file changed since it was published (the amber dot on Publish). */
	outdated: boolean;
	sendTarget?: ArtifactCommentsSendTarget;
	slug: string;
	title: string;
}) {
	const { t } = useTranslation();
	const comments = useArtifactComments(slug, { panelOpen: true });
	const [frame, setFrame] = useState<HTMLIFrameElement | null>(null);
	// The element whose comments are open: a picked one brings its anchor for a new comment.
	const [element, setElement] = useState<{ path: string; anchor: ArtifactCommentAnchor | null } | null>(null);
	// Where the selected element is in the frame, from the bridge: the popover sits beside it.
	const [selection, setSelection] = useState<({ path: string } & CommentsSelectionRect) | null>(null);
	const closePopover = useCallback(() => setElement(null), []);
	// From a new comment to the element's threads, once it is posted.
	const showThreads = useCallback(() => setElement((current) => current && { path: current.path, anchor: null }), []);
	const elementRef = useRef(element);
	elementRef.current = element;
	const bridge = useCommentBridge(frame, {
		onPick: (anchor) => setElement({ path: anchor.path, anchor }),
		onMarker: (path) => setElement({ path, anchor: null }),
		onSelection: ({ path, top, bottom }) => setSelection(path === null ? null : { path, top, bottom }),
		// Esc closes the popover first, then leaves comment mode.
		onEscape: () => {
			if (elementRef.current === null) onClose();
			else setElement(null);
		},
	});
	const { setAnchors, setMode, select } = bridge;
	useEffect(() => setMode({ type: "mode", on: true, kind }), [kind, setMode]);

	const threads = comments.snapshot?.threads;
	const groups = useMemo(() => groupArtifactCommentThreads(threads ?? []), [threads]);
	// Counts on the elements; a picked element without comments is sent too, so the bridge can find it.
	const pickedAnchor = element?.anchor ?? null;
	const anchorsKey = useMemo(() => {
		const markers = artifactCommentMarkers(threads ?? []);
		if (pickedAnchor && !markers.some((marker) => marker.path === pickedAnchor.path)) markers.push({ path: pickedAnchor.path, textHash: pickedAnchor.textHash, count: 0 });
		return JSON.stringify(markers);
	}, [pickedAnchor, threads]);
	useEffect(() => setAnchors(JSON.parse(anchorsKey)), [anchorsKey, setAnchors]);
	const selectedPath = element?.path ?? null;
	useEffect(() => select(selectedPath), [select, selectedPath]);

	// The popover's place: beside the selected element, so the element, its
	// highlight and the popover all show.
	const stageRef = useRef<HTMLDivElement>(null);
	const [stageHeight, setStageHeight] = useState(0);
	const [popoverHeight, setPopoverHeight] = useState(0);
	useEffect(() => {
		const stage = stageRef.current;
		if (!stage || typeof ResizeObserver === "undefined") return;
		const observer = new ResizeObserver(() => setStageHeight(stage.clientHeight));
		observer.observe(stage);
		setStageHeight(stage.clientHeight);
		return () => observer.disconnect();
	}, []);
	const placement = stageHeight > 0
		? placeCommentsPopover(selection && selection.path === selectedPath ? selection : null, stageHeight, popoverHeight)
		: null;

	const lostAccess = comments.lostAccess;
	const onShownRef = useRef(onShown);
	onShownRef.current = onShown;
	useEffect(() => {
		if (lostAccess) onShownRef.current?.();
	}, [lostAccess]);
	const shown = useCallback(() => onShownRef.current?.(), []);

	if (lostAccess) {
		return (
			<PanelMessage>
				<span className="font-medium text-foreground">{t("artifactComments.lostAccess")}</span>
				<br />
				{t("artifactComments.lostAccessHint")}
			</PanelMessage>
		);
	}
	const version = comments.snapshot?.currentVersion;
	return (
		<div className="flex h-full min-h-0 flex-col" data-testid="artifact-comments-workspace">
			{outdated && version !== undefined ? (
				<p className="shrink-0 border-b border-border px-3 py-1.5 text-caption text-muted-foreground" role="status">
					{t("artifactComments.publishedVersion", { version })}
				</p>
			) : null}
			<div className="relative min-h-0 flex-1" data-testid="artifact-comments-stage" ref={stageRef}>
				<SharedArtifactFrame frameRef={setFrame} linkId={slug} onShown={shown} title={title} />
				{element ? (
					<ArtifactCommentsPanel
						className="absolute top-2 right-2 z-overlay max-h-[calc(100%-1rem)] max-w-[calc(100%-1rem)] rounded-lg border border-(--color-border-overlay) bg-overlay text-popover-foreground shadow-(--shadow-popover) transition-[top] duration-150 ease-out motion-reduce:transition-none animate-popover-in"
						comments={comments}
						element={element}
						groups={groups}
						onClose={closePopover}
						onNaturalHeight={setPopoverHeight}
						onPosted={showThreads}
						sendTarget={sendTarget}
						style={placement ? { top: placement.top, maxHeight: placement.maxHeight } : undefined}
					/>
				) : null}
			</div>
		</div>
	);
}
