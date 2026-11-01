/**
 * One shape for every attachment in chat, in the composer and in sent messages.
 *
 * Every tile is 40px tall: images are 40px squares and files and annotations are
 * 40px-tall cards of a fixed width, so a mixed row lines up and no attachment, however large its
 * source, takes over the conversation. Tapping an image opens it at full size.
 */

import { File as FileIcon, X } from "lucide-react";
import type { ReactNode } from "react";
import { cn } from "../../lib/utils";

/** The round × pinned to a tile's corner: hidden until the tile is hovered or the button focused. */
export const ATTACHMENT_REMOVE_BUTTON = "pointer-events-none absolute -right-1.5 -top-1.5 flex size-4 items-center justify-center rounded-full border border-border bg-popover text-muted-foreground opacity-0 shadow-sm transition-[opacity,color] duration-150 after:absolute after:-inset-1 hover:text-foreground focus-visible:pointer-events-auto focus-visible:opacity-100 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring group-hover/attachment:pointer-events-auto group-hover/attachment:opacity-100 disabled:cursor-not-allowed group-hover/attachment:disabled:opacity-50 motion-reduce:transition-none";

export function AttachmentList({ ariaLabel, className, children }: { ariaLabel: string; className?: string; children: ReactNode }) {
	return (
		<ul aria-label={ariaLabel} className={cn("flex max-w-full flex-wrap gap-2", className)}>
			{children}
		</ul>
	);
}

/** A list item with an optional remove button pinned to its top-right corner. */
export function AttachmentTile({
	children,
	removeLabel,
	onRemove,
	removeDisabled,
}: {
	children: ReactNode;
	removeLabel?: string;
	onRemove?: () => void;
	removeDisabled?: boolean;
}) {
	return (
		<li className={"group/attachment relative max-w-full"}>
			{children}
			{onRemove ? (
				<button
					type="button"
					onMouseDown={(event) => {
						if (event.button === 0) event.preventDefault();
					}}
					onClick={onRemove}
					disabled={removeDisabled}
					aria-label={removeLabel}
					className={ATTACHMENT_REMOVE_BUTTON}
				>
					<X aria-hidden="true" className="size-2.5" />
				</button>
			) : null}
		</li>
	);
}

/** "report.final.PDF" reads as "PDF"; a name with no extension reads as "File". */
function fileKind(name: string): string {
	const dot = name.lastIndexOf(".");
	const extension = dot > 0 ? name.slice(dot + 1) : "";
	return extension && extension.length <= 5 ? extension.toUpperCase() : "File";
}

/** A file still being read and staged: shown at once, dimmed, and swapped for its tile when ready. */
export function AttachmentPendingTile({ name, previewUrl }: { name: string; previewUrl?: string }) {
	return (
		<li role="status" aria-label={`Attaching ${name}`} className="relative max-w-full opacity-60">
			{previewUrl ? (
				<img src={previewUrl} alt="" className="block size-10 rounded-lg border border-border object-cover" />
			) : (
				<AttachmentFileCard name={name} />
			)}
		</li>
	);
}

/** The frame every card in the row shares: files, and annotations beside them. */
export const ATTACHMENT_CARD = "flex h-10 w-40 max-w-full items-center gap-2 rounded-lg bg-interactive-hover pl-1.5 pr-2.5 text-left";

export function AttachmentCardBody({ icon, title, subtitle }: { icon: ReactNode; title: string; subtitle?: string }) {
	return (
		<>
			<span className="flex size-7 shrink-0 items-center justify-center rounded-md bg-surface text-muted-foreground">{icon}</span>
			<span className="flex min-w-0 flex-col">
				<span className="truncate text-xs font-medium leading-4 text-foreground">{title}</span>
				{subtitle ? <span className="truncate text-[11px] leading-3.5 text-muted-foreground">{subtitle}</span> : null}
			</span>
		</>
	);
}

export function AttachmentFileCard({ name, title }: { name: string; title?: string }) {
	return (
		<div title={title ?? name} className={ATTACHMENT_CARD}>
			<AttachmentCardBody icon={<FileIcon aria-hidden="true" className="size-3.5" />} title={name} subtitle={fileKind(name)} />
		</div>
	);
}
