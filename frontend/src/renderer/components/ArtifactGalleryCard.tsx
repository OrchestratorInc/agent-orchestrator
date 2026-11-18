import { artifactFileIcon } from "./artifact-file-icon";
import { useId, type ReactNode } from "react";
import type { ArtifactRow } from "../lib/artifacts";
import { formatTimeCompact } from "../lib/format-time";
import { cn } from "../lib/utils";
import { ArtifactShareMark } from "./ArtifactShareMark";
import { ArtifactSnapshotThumbnail } from "./ArtifactSnapshotThumbnail";
import { Checkbox } from "./ui/checkbox";
import { Pressable } from "./ui/pressable";

/**
 * While the page is selecting, the card toggles its checkbox instead of
 * opening a preview. The checkbox is the control keyboard and screen-reader
 * users reach; the card is a larger target for the pointer.
 */
export type ArtifactCardSelection = {
	selected: boolean;
	/** Set when this person may not delete the file, with why. */
	disabledReason?: string;
	label: string;
	/**
	 * Which project, session and path the file belongs to. The card is hidden
	 * from assistive technology while selecting, so this describes the
	 * checkbox and tells same-named files apart.
	 */
	description: string;
	onToggle: () => void;
};

export function ArtifactGalleryCard({ row, context, onPreview, actions, previewLive = true, selection }: {
	row: ArtifactRow;
	context: string;
	onPreview: () => void;
	actions: ReactNode;
	previewLive?: boolean;
	selection?: ArtifactCardSelection;
}) {
	const KindIcon = artifactFileIcon(row);
	const toggle = selection && !selection.disabledReason ? selection.onToggle : undefined;
	const descriptionId = useId();
	return (
		<li className={cn("group relative min-w-0 rounded-md bg-card ring-1 ring-foreground/10", selection?.selected && "ring-2 ring-primary")}>
			<span className="pointer-events-none absolute inset-x-0 top-0 z-[1] block aspect-[16/9] overflow-hidden rounded-t-md border-b border-border">
				<ArtifactSnapshotThumbnail key={`${row.rawUrl}:${row.inlineUrl}:${row.updatedAt}`} row={row} previewLive={previewLive} />
			</span>
			<Pressable
				className={cn("block w-full overflow-hidden rounded-md hover:bg-accent", selection?.disabledReason && "cursor-not-allowed hover:bg-transparent")}
				onClick={selection ? toggle : onPreview}
				title={selection?.disabledReason ?? `${row.sessionTitle} · ${row.path}`}
				{...(selection ? { tabIndex: -1, "aria-hidden": true } : {})}
			>
				<span className="relative block aspect-[16/9] border-b border-border bg-[var(--solid-muted,var(--muted))]" />
				<span className="block space-y-1 px-3 py-2.5">
					<span className="flex min-w-0 items-center gap-1.5 text-control font-medium leading-5 text-foreground">
						<KindIcon aria-hidden="true" className="size-icon-sm shrink-0 text-muted-foreground" />
						<span className="truncate">{row.name}</span>
					</span>
					<span className="flex min-w-0 items-center gap-1.5 text-caption leading-4 text-muted-foreground">
						{context ? <><span className="truncate">{context}</span><span aria-hidden="true">·</span></> : null}
						<ArtifactShareMark row={row} className="relative z-[2]" iconClassName="size-3" />
						<time dateTime={row.updatedAt} className="shrink-0 tabular-nums">{formatTimeCompact(row.updatedAt)}</time>
					</span>
				</span>
			</Pressable>
			{selection ? <span id={descriptionId} className="sr-only">{selection.description}</span> : null}
			{selection ? (
				<Checkbox
					aria-describedby={descriptionId}
					aria-label={selection.label}
					checked={selection.selected}
					className="absolute left-3 top-3 z-[2] size-5 rounded-[5px] border-foreground/40 bg-[var(--solid-bg-secondary,var(--color-bg-secondary))] shadow-sm transition-none duration-0 hover:border-foreground/70 data-[state=checked]:border-primary [&_svg]:size-3.5"
					disabled={Boolean(selection.disabledReason)}
					onCheckedChange={toggle}
					title={selection.disabledReason}
				/>
			) : null}
			<div className="absolute right-2 top-2 z-[2] flex items-center gap-1">{actions}</div>
		</li>
	);
}
