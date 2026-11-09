import { useState } from "react";
import { X } from "lucide-react";
import { useTranslation } from "react-i18next";
import { cn } from "../../lib/utils";
import { Button } from "../ui/button";
import { Pressable } from "../ui/pressable";
import { ChatImage, ChatImageAttachmentRow, ChatImageDialog } from "./ChatImage";
import { ATTACHMENT_TILE_CLASS, ATTACHMENT_TILE_PILL_CLASS, attachmentName, attachmentTypeLabel, attachmentURL, formatAttachmentSize, IMAGE_ATTACHMENT_PATH } from "./messageAttachments";

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
	const { t } = useTranslation();
	if (paths.length === 0) return null;
	const images = paths.filter((path) => IMAGE_ATTACHMENT_PATH.test(path));
	return (
		<ChatImageAttachmentRow>
			<ul aria-label={ariaLabel} className={cn("flex max-w-full flex-wrap gap-2", className)}>
				{paths.map((path) => {
					const name = attachmentName(path);
					const image = IMAGE_ATTACHMENT_PATH.test(path);
					// An image with no preview URL shows as a file chip, but it still can't go.
					const remove = image ? undefined : onRemove;
					return image && apiBaseUrl !== null ? (
						<li key={path} className="max-w-full">
							<ChatImage
								src={attachmentURL(apiBaseUrl, sessionId, path)}
								alt={t("chat.image.numbered", { index: images.indexOf(path) + 1 })}
							/>
						</li>
					) : (
						<li
							key={path}
							title={path}
							className={cn("group/attachment", ATTACHMENT_TILE_CLASS)}
						>
							<AttachmentFileCard name={name} />
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
				})}
			</ul>
		</ChatImageAttachmentRow>
	);
}
