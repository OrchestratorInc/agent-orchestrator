/**
 * Small pictures of the images a step read, beside its row or its group's summary,
 * so a reader sees at a glance which screenshots or images the agent looked at.
 *
 * A harness keeps an image result's size but not its bytes, so the picture comes
 * from the file the step read, through the workspace's image route. Only a file
 * inside the session's workspace has one: anything else draws no picture.
 */

import { useState } from "react";
import type { ConversationActivity } from "../../types/conversation";
import { cn } from "../../lib/utils";
import { useChatArtifactFiles, useChatImageSrc, useChatSessionId } from "./chat-image-source";

const IMAGE_PATH = /\.(?:png|jpe?g|gif|webp|avif|bmp|svg)$/i;

/** The workspace-relative path of the image a tool call read, when it read one inside the session's workspace. */
export function toolImagePath(activity: ConversationActivity): string | undefined {
	if (activity.activityKind !== "mcp_tool" || activity.status !== "completed") return undefined;
	const detail = activity.detail;
	if (!detail || !Array.isArray(detail.content) || !JSON.stringify(detail.content).includes('"type":"image"')) return undefined;
	const input = detail.input && typeof detail.input === "object" ? (detail.input as Record<string, unknown>) : {};
	const locations = (detail as { locations?: unknown }).locations;
	const located = Array.isArray(locations) ? (locations[0] as { path?: unknown } | undefined)?.path : undefined;
	const path = [input.file_path, input.path, input.filePath, input.absolute_path, input.absolutePath, located].find(
		(value): value is string => typeof value === "string" && value !== "",
	);
	if (!path || !IMAGE_PATH.test(path)) return undefined;
	return workspaceRelative(path, detail.cwd);
}

/** A path as the workspace names it: relative already, under the step's folder, or under an Orchestrator worktree. */
function workspaceRelative(path: string, cwd: string | undefined): string | undefined {
	const normalized = path.replace(/\\/g, "/");
	if (!normalized.startsWith("/") && !/^[A-Za-z]:\//.test(normalized)) return normalized.replace(/^\.\//, "");
	const root = cwd?.replace(/\\/g, "/").replace(/\/$/, "");
	if (root && normalized.startsWith(`${root}/`)) return normalized.slice(root.length + 1);
	// ~/.ao/…/worktrees/<project>/<session>/<path>
	return /\/\.ao\/(?:[^/]+\/)*worktrees\/[^/]+\/[^/]+\/(.+)$/.exec(normalized)?.[1];
}

/** Up to three of the images these steps read, once each. */
export function ToolImageThumbs({ activities, max = 3, className }: { activities: readonly ConversationActivity[]; max?: number; className?: string }) {
	const sessionId = useChatSessionId();
	const cloud = Boolean(useChatArtifactFiles()?.cloud);
	if (!sessionId || cloud) return null;
	const paths: string[] = [];
	for (const activity of activities) {
		const path = toolImagePath(activity);
		if (path && !paths.includes(path)) paths.push(path);
		if (paths.length === max) break;
	}
	if (paths.length === 0) return null;
	return (
		<span aria-hidden="true" data-tool-image-thumbs="" className={cn("ml-1 flex shrink-0 items-center gap-1", className)}>
			{paths.map((path) => <ToolImageThumb key={path} path={path} />)}
		</span>
	);
}

function ToolImageThumb({ path }: { path: string }) {
	const src = useChatImageSrc(path);
	const [failed, setFailed] = useState(false);
	if (!src || failed) return null;
	return (
		<img
			src={src}
			alt=""
			loading="lazy"
			decoding="async"
			draggable={false}
			onError={() => setFailed(true)}
			className="h-5 w-8 shrink-0 rounded-[3px] bg-surface object-cover object-top ring-1 ring-border"
		/>
	);
}
