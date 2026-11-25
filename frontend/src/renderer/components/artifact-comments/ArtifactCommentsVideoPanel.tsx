import { useState } from "react";
import { useTranslation } from "react-i18next";
import { X } from "@/components/app-icons";
import { cn } from "../../lib/utils";
import {
	formatArtifactCommentTime,
	isPointRegion,
	type ArtifactCommentAnchor,
	type ArtifactCommentGroup,
	type ArtifactCommentGroups,
} from "../../../shared/artifact-comments";
import { Button } from "../ui/button";
import { RetryButton } from "../WorkspaceDiffView";
import { ArtifactCommentsPanel, Avatar, useAuthorName, type Viewer } from "./ArtifactCommentsPanel";
import type { ArtifactCommentsSendTarget } from "./SendThreadToAgentDialog";
import type { ArtifactCommentsState } from "./useArtifactComments";

/** One row in the list: a group with comments, or the new comment being written. */
type Row = { key: string; time: number; anchor: ArtifactCommentAnchor; group: ArtifactCommentGroup | null };

/**
 * A video's comments beside the player, by timestamp: each row is a moment
 * (and the point or box on its frame) with its newest comment. Clicking a row
 * seeks the video there, paused, and opens the row in place: its threads,
 * reply, resolve, Send to agent and Add comment, for another comment on the
 * same moment. A comment being written shows as a row at its time. Resolved
 * moments are listed under "Resolved (N)".
 */
export function ArtifactCommentsVideoPanel({ comments, element, groups, onClose, onDeselect, onPosted, onSelect, onWriteOn, sendTarget }: {
	comments: ArtifactCommentsState;
	groups: ArtifactCommentGroups;
	/** The open row: a group's key, with an anchor when a new comment is being written on it. */
	element: { path: string; anchor: ArtifactCommentAnchor | null } | null;
	/** Leaves comments. */
	onClose: () => void;
	/** Closes the open row. */
	onDeselect: () => void;
	onPosted: () => void;
	/** Opens a row (and seeks the video to it). */
	onSelect: (key: string) => void;
	/** Starts a new comment on an anchor: Add comment on an existing moment. */
	onWriteOn: (anchor: ArtifactCommentAnchor) => void;
	sendTarget?: ArtifactCommentsSendTarget;
}) {
	const { t } = useTranslation();
	const authorName = useAuthorName();
	const snapshot = comments.snapshot;
	const viewer: Viewer = snapshot?.you ?? { userId: "", isOwner: false };
	const [showResolved, setShowResolved] = useState(false);
	const isVideo = (group: ArtifactCommentGroup) => group.anchor.kind === "video";
	const byTime = (a: Row, b: Row) => a.time - b.time || a.key.localeCompare(b.key);
	const rows: Row[] = groups.open.filter(isVideo).map((group) => ({ key: group.path, time: group.anchor.time ?? 0, anchor: group.anchor, group }));
	const picked = element?.anchor ?? null;
	if (picked && !rows.some((row) => row.key === element!.path)) {
		const resolved = groups.resolved.find((group) => group.path === element!.path) ?? null;
		rows.push({ key: element!.path, time: picked.time ?? 0, anchor: picked, group: resolved });
	}
	rows.sort(byTime);
	const resolvedRows: Row[] = groups.resolved
		.filter((group) => isVideo(group) && !rows.some((row) => row.key === group.path))
		.map((group) => ({ key: group.path, time: group.anchor.time ?? 0, anchor: group.anchor, group }))
		.sort(byTime);
	const openCount = groups.open.filter(isVideo).reduce((sum, group) => sum + group.openCount, 0);

	const row = (item: Row) => {
		const selected = element?.path === item.key;
		const region = item.anchor.region;
		const newest = item.group?.threads[0];
		const first = newest?.comments[0];
		const head = (
			<>
				<span className="flex items-center gap-1.5">
					<span className="rounded bg-logo-accent/15 px-1.5 py-0.5 font-medium text-logo-accent-text tabular-nums">{formatArtifactCommentTime(item.time)}</span>
					{region ? (
						<span className="text-caption text-muted-foreground">
							{isPointRegion(region) ? t("artifactComments.onPoint") : t("artifactComments.onRegion")}
						</span>
					) : null}
					{item.group && item.group.threads.length > 1 ? (
						<span className="ml-auto text-caption text-muted-foreground">{t("artifactComments.openCount", { n: item.group.threads.length })}</span>
					) : null}
				</span>
				{!selected && first ? (
					<span className="flex min-w-0 items-center gap-1.5">
						<Avatar author={first.author} name={authorName(first, viewer)} />
						<span className={cn("min-w-0 flex-1 truncate", first.deleted ? "italic text-muted-foreground" : "text-foreground")}>
							{first.deleted ? t("artifactComments.deleted") : first.body}
						</span>
					</span>
				) : null}
			</>
		);
		return (
			<li
				className={cn("rounded-lg border", selected ? "border-logo-accent bg-background" : "border-transparent")}
				data-selected={selected ? "" : undefined}
				data-testid="artifact-comments-video-row"
				data-time={item.time}
				key={item.key}
			>
				{selected ? (
					<button
						aria-expanded
						className="flex w-full flex-col gap-1.5 rounded-lg p-2.5 text-left text-xs"
						onClick={onDeselect}
						type="button"
					>
						{head}
					</button>
				) : (
					<button
						aria-expanded={false}
						className="flex w-full flex-col gap-1.5 rounded-lg p-2.5 text-left text-xs hover:bg-interactive-hover"
						onClick={() => onSelect(item.key)}
						type="button"
					>
						{head}
					</button>
				)}
				{selected && element ? (
					<ArtifactCommentsPanel
						comments={comments}
						element={element}
						embedded
						groups={groups}
						onAddComment={() => item.group && onWriteOn(item.group.anchor)}
						onClose={onDeselect}
						onPosted={onPosted}
						sendTarget={sendTarget}
					/>
				) : null}
			</li>
		);
	};

	return (
		<aside aria-label={t("artifactComments.title")} className="flex w-[340px] shrink-0 flex-col border-l border-border bg-background" data-testid="artifact-comments-video-panel">
			<div className="flex h-10 shrink-0 items-center gap-1 border-b border-border pr-1.5 pl-3">
				<h2 className="min-w-0 flex-1 truncate text-sm font-semibold text-foreground">
					{t("artifactComments.title")}
					{openCount > 0 ? <span className="font-normal text-muted-foreground">{" · "}{t("artifactComments.openCount", { n: openCount })}</span> : null}
				</h2>
				<Button aria-label={t("artifactComments.close")} onClick={onClose} size="icon-sm" type="button" variant="quiet">
					<X aria-hidden="true" className="size-icon-sm" />
				</Button>
			</div>
			<div className="board-scrollbar min-h-0 flex-1 overflow-y-auto p-2">
				{!snapshot && comments.error ? (
					<div className="flex flex-col items-center gap-2 p-4 text-center text-xs text-muted-foreground" role="alert">
						{t("artifactComments.loadError")}
						<RetryButton onClick={() => void comments.refetch()} />
					</div>
				) : null}
				{snapshot && rows.length === 0 && resolvedRows.length === 0 ? (
					<p className="p-3 text-center text-xs text-muted-foreground">{t("artifactComments.videoEmpty")}</p>
				) : null}
				<ul className="flex flex-col gap-1">{rows.map(row)}</ul>
				{resolvedRows.length > 0 ? (
					<div className="mt-2 flex flex-col gap-1 border-t border-border pt-2">
						<button
							aria-expanded={showResolved}
							className="px-2.5 text-left text-caption text-logo-accent-text hover:underline"
							onClick={() => setShowResolved((current) => !current)}
							type="button"
						>
							{t("artifactComments.resolvedSection", { n: resolvedRows.reduce((sum, item) => sum + (item.group?.threads.length ?? 0), 0) })}
						</button>
						{showResolved ? <ul className="flex flex-col gap-1 opacity-80">{resolvedRows.map(row)}</ul> : null}
					</div>
				) : null}
			</div>
		</aside>
	);
}
