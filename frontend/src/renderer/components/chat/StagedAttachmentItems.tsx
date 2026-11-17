import { useRef, useState, type ReactNode } from "react";
import { X } from "lucide-react";
import { useTranslation } from "react-i18next";
import { useEdgeFade } from "../../hooks/useEdgeFade";
import { readAttachmentNames } from "../../lib/chat-attachment-names";
import { useSentAttachmentPreview } from "../../lib/sent-attachment-previews";
import { cn } from "../../lib/utils";
import { Button } from "../ui/button";
import { Pressable } from "../ui/pressable";
import { ChatImage, ChatImageAttachmentRow, ChatImageDialog } from "./ChatImage";
import { AttachmentFilePreview } from "./AttachmentFilePreview";
import { ATTACHMENT_TILE_CLASS, ATTACHMENT_TILE_PILL_CLASS, attachmentName, attachmentPreviewKind, attachmentTypeLabel, attachmentURL, formatAttachmentSize, IMAGE_ATTACHMENT_PATH } from "./messageAttachments";

/**
 * The attachments above a user's bubble: one row, capped to the bubble's width,
 * that scrolls sideways and fades out at an edge with more past it. Outside the
 * bubble so a long message's fold can never clip them. Lists inside it use
 * `contents`, so every kind of attachment shares the row. The single inner row
 * is what the fade watches resize as images load.
 */
export function AttachmentStrip({ className, children }: { className?: string; children: ReactNode }) {
	const scroller = useRef<HTMLDivElement>(null);
	useEdgeFade(scroller, "x");
	return (
		// Padded out and pulled back so a tile's focus ring isn't clipped by the scroller.
		<div ref={scroller} className={cn("edge-fade edge-fade-x scrollbar-none -m-0.5 max-w-[min(78%,560px)] overflow-x-auto overflow-y-hidden overscroll-x-contain p-0.5", className)}>
			<div className="flex w-max items-start gap-2">{children}</div>
		</div>
	);
}

/** A file chip's words: the name, then its type and, when it is known, its size. */
export function AttachmentFileText({ name, bytes, nameClassName }: { name: string; bytes?: number; nameClassName?: string }) {
	const details = [attachmentTypeLabel(name), bytes === undefined ? undefined : formatAttachmentSize(bytes)].filter(Boolean).join(" · ");
	return (
		<>
			<span className={cn("truncate", nameClassName)}>{name}</span>
			{details ? <span className="shrink-0 tabular-nums opacity-70">{details}</span> : null}
		</>
	);
}

/** A file tile's contents: the name on top, then its type as a pill and its size when known. */
export function AttachmentFileCard({ name, bytes }: { name: string; bytes?: number }) {
	const type = attachmentTypeLabel(name);
	return (
		<>
			<span className="line-clamp-2 text-foreground">{name}</span>
			<span className="flex min-w-0 items-center gap-1.5 overflow-hidden">
				{type ? <span className={ATTACHMENT_TILE_PILL_CLASS}>{type}</span> : null}
				{bytes === undefined ? null : <span className="whitespace-nowrap text-micro tabular-nums">{formatAttachmentSize(bytes)}</span>}
			</span>
		</>
	);
}

/** The composer strip's image: a small square that opens the picture at full size. */
export function AttachmentThumbnail({ src, name }: { src: string; name: string }) {
	const { t } = useTranslation();
	const [open, setOpen] = useState(false);
	return (
		<>
			<Pressable
				onClick={() => setOpen(true)}
				aria-label={t("chat.image.open", { name })}
				className="size-6 shrink-0 cursor-zoom-in overflow-hidden rounded-sm focus-visible:ring-2 focus-visible:ring-ring"
			>
				<img src={src} alt="" className="block size-full object-cover" />
			</Pressable>
			<ChatImageDialog open={open} onOpenChange={setOpen} url={src} label={name} />
		</>
	);
}

/**
 * A message's staged files, as its bubble draws them. The message editor draws
 * the same list, so starting an edit moves nothing.
 */
export function StagedAttachmentItems({
	paths,
	sessionId,
	apiBaseUrl,
	ariaLabel,
	className,
	onRemove,
}: {
	paths: string[];
	sessionId: string;
	apiBaseUrl: string | null;
	ariaLabel: string;
	className?: string;
	/** Offered on files only: an edit always resends the original image data. */
	onRemove?: (path: string) => void;
}) {
	if (paths.length === 0) return null;
	const images = paths.filter((path) => IMAGE_ATTACHMENT_PATH.test(path));
	const originalNames = readAttachmentNames();
	return (
		<ChatImageAttachmentRow>
			<ul aria-label={ariaLabel} className={cn("flex max-w-full flex-wrap gap-2", className)}>
				{paths.map((path) => (
					<StagedAttachmentItem
						key={path}
						path={path}
						name={originalNames[path] ?? attachmentName(path)}
						imageIndex={images.indexOf(path)}
						sessionId={sessionId}
						apiBaseUrl={apiBaseUrl}
						onRemove={onRemove}
					/>
				))}
			</ul>
		</ChatImageAttachmentRow>
	);
}

/**
 * One staged file's tile. A file the user just sent draws from the copy the
 * composer left behind, so it shows with the message instead of after a read.
 */
function StagedAttachmentItem({
	path,
	name,
	imageIndex,
	sessionId,
	apiBaseUrl,
	onRemove,
}: {
	path: string;
	name: string;
	imageIndex: number;
	sessionId: string;
	apiBaseUrl: string | null;
	onRemove?: (path: string) => void;
}) {
	const { t } = useTranslation();
	// A local copy that fails to draw hands the tile to the daemon's copy.
	const local = useSentAttachmentPreview(path);
	const url = local.url ?? (apiBaseUrl === null ? undefined : attachmentURL(apiBaseUrl, sessionId, path));
	const image = imageIndex >= 0;
	// An image with no preview URL shows as a file chip, but it still can't go.
	const remove = image ? undefined : onRemove;
	const previewKind = attachmentPreviewKind(path);
	return image && url ? (
		<li className="max-w-full">
			<ChatImage src={url} alt={t("chat.image.numbered", { index: imageIndex + 1 })} onError={local.onError} />
		</li>
	) : (
		<li title={name} className={cn("group/attachment", ATTACHMENT_TILE_CLASS)}>
			<AttachmentFileCard name={name} />
			{previewKind && url ? <AttachmentFilePreview kind={previewKind} url={url} name={name} onError={local.onError} /> : null}
			{/* Remove appears in the corner on hover, so the card keeps its size. */}
			{remove ? (
				<span className="absolute right-0.5 top-0.5 flex opacity-0 group-focus-within/attachment:opacity-100 group-hover/attachment:opacity-100">
					<Button
						type="button"
						variant="destructive-ghost"
						size="icon-xs"
						onClick={() => remove(path)}
						aria-label={t("chat.attachment.remove", { name })}
					>
						<X aria-hidden="true" className="size-3" />
					</Button>
				</span>
			) : null}
		</li>
	);
}
