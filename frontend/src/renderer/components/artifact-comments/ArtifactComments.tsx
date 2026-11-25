import { useCallback, useEffect, useMemo, useState } from "react";
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
	type ArtifactCommentAnchor,
	type ArtifactCommentsPickKind,
	type BridgeAnchorPosition,
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
 * The open file's published page with its comments: the same bytes the link
 * shows, framed from its share host with this account's view token, and the
 * comments in a popover floating over its top right corner, under the header's
 * Comments button, so the page keeps its full width. Comments belong to the
 * published version, so the local file is not shown while they are open; a
 * notice says so when the local file has changed since publishing.
 *
 * With no comments yet nothing pops up: comment mode is on (the button stays
 * pressed and the page outlines what's under the pointer), and the popover
 * opens once an element is picked.
 */
export function ArtifactCommentsWorkspace({ kind, onClose, outdated, sendTarget, slug, title }: {
	kind: ArtifactCommentsPickKind;
	onClose: () => void;
	/** The local file changed since it was published (the amber dot on Publish). */
	outdated: boolean;
	sendTarget?: ArtifactCommentsSendTarget;
	slug: string;
	title: string;
}) {
	const { t } = useTranslation();
	const comments = useArtifactComments(slug, { panelOpen: true });
	const [frame, setFrame] = useState<HTMLIFrameElement | null>(null);
	const [positions, setPositions] = useState<BridgeAnchorPosition[]>([]);
	const [picked, setPicked] = useState<ArtifactCommentAnchor | null>(null);
	const [expandedPath, setExpandedPath] = useState<string | null>(null);
	const bridge = useCommentBridge(frame, {
		onPick: (anchor) => {
			setPicked(anchor);
			setExpandedPath(anchor.path);
		},
		onMarker: (path) => {
			setPicked(null);
			setExpandedPath(path);
		},
		onPositions: setPositions,
		onEscape: onClose,
	});
	const { setAnchors, setMode, select } = bridge;
	useEffect(() => setMode({ type: "mode", on: true, kind }), [kind, setMode]);

	const threads = comments.snapshot?.threads;
	const groups = useMemo(() => groupArtifactCommentThreads(threads ?? [], positions), [positions, threads]);
	// The picked element is reported too, so its new group takes its place in the page.
	const anchorsKey = useMemo(() => {
		const markers = artifactCommentMarkers(threads ?? []);
		if (picked && !markers.some((marker) => marker.path === picked.path)) markers.push({ path: picked.path, textHash: picked.textHash, count: 0 });
		return JSON.stringify(markers);
	}, [picked, threads]);
	useEffect(() => setAnchors(JSON.parse(anchorsKey)), [anchorsKey, setAnchors]);
	const selectedPath = picked?.path ?? expandedPath;
	useEffect(() => select(selectedPath), [select, selectedPath]);

	const expand = useCallback((path: string | null) => {
		setExpandedPath(path);
		setPicked((current) => (current && current.path !== path ? null : current));
	}, []);
	const cancelPick = useCallback(() => setPicked(null), []);

	if (comments.lostAccess) {
		return (
			<PanelMessage>
				<span className="font-medium text-foreground">{t("artifactComments.lostAccess")}</span>
				<br />
				{t("artifactComments.lostAccessHint")}
			</PanelMessage>
		);
	}
	const version = comments.snapshot?.currentVersion;
	// Shown once there is something to show: threads, a picked element, a load
	// error, or a page whose parts can't be picked (which says so).
	const showPopover = (threads?.length ?? 0) > 0 || picked !== null || (Boolean(comments.error) && !comments.snapshot) || bridge.status === "unsupported";
	return (
		<div className="flex h-full min-h-0 flex-col" data-testid="artifact-comments-workspace">
			{outdated && version !== undefined ? (
				<p className="shrink-0 border-b border-border px-3 py-1.5 text-caption text-muted-foreground" role="status">
					{t("artifactComments.publishedVersion", { version })}
				</p>
			) : null}
			<div className="relative min-h-0 flex-1">
				<SharedArtifactFrame frameRef={setFrame} linkId={slug} title={title} />
				{showPopover ? (
					<ArtifactCommentsPanel
						bridgeStatus={bridge.status}
						className="absolute top-2 right-2 z-overlay max-h-[calc(100%-1rem)] max-w-[calc(100%-1rem)] rounded-lg border border-(--color-border-overlay) bg-overlay text-popover-foreground shadow-(--shadow-popover) animate-popover-in"
						comments={comments}
						expandedPath={expandedPath}
						groups={groups}
						onCancelPick={cancelPick}
						onClose={onClose}
						onExpand={expand}
						picked={picked}
						positions={positions}
						sendTarget={sendTarget}
					/>
				) : null}
			</div>
		</div>
	);
}
