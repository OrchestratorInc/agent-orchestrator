/**
 * An artifact's hover preview: its picture, its name, and one line saying what
 * it is and when it changed. The chat's artifact list and an artifact link in a
 * reply both open it, so an artifact previews the same way wherever it is named.
 */

import type { ComponentProps } from "react";
import { ArtifactThumbnail } from "./ArtifactThumbnail";
import { HoverCardContent } from "./ui/hover-card";
import type { ArtifactRow } from "../lib/artifacts";
import { cn } from "../lib/utils";
import type { ArtifactKind } from "../types/workspace";

/** What an artifact is, in a word: `Page` for HTML, `Markdown`, else its file type (`SVG`, `PNG`, `CSV`). */
export function artifactTypeLabel(path: string, kind: ArtifactKind): string {
	if (kind === "html") return "Page";
	if (kind === "markdown") return "Markdown";
	const extension = /\.([a-z0-9]+)$/i.exec(path)?.[1];
	return extension ? extension.toUpperCase() : "File";
}

/** The card itself, for a hover card already open on an artifact. */
export function ArtifactHoverPreview({
	row,
	detail,
	previewLive = true,
	className,
	...props
}: {
	row: ArtifactRow;
	detail: string;
	/** False keeps a page unframed (its picture falls back), for a link to another host's or Cloud's artifact. */
	previewLive?: boolean;
} & Omit<ComponentProps<typeof HoverCardContent>, "children">) {
	return (
		<HoverCardContent collisionPadding={8} className={cn("w-72 rounded-xl p-3", className)} {...props}>
			<div role="status" aria-label={row.name} className="flex flex-col">
				<span className="relative block aspect-[16/10] overflow-hidden rounded-md bg-[var(--solid-muted,var(--muted))] ring-1 ring-border">
					<ArtifactThumbnail key={`${row.rawUrl}:${row.inlineUrl}:${row.updatedAt}`} previewLive={previewLive} row={row} />
				</span>
				<span className="mt-3 truncate text-sm font-medium leading-5 text-foreground">{row.name}</span>
				<span className="truncate text-control leading-5 text-muted-foreground">{detail}</span>
			</div>
		</HoverCardContent>
	);
}
