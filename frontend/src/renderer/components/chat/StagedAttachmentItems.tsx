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

export function AttachmentStrip({ className, children }: { className?: string; children: ReactNode }) {
	const scroller = useRef<HTMLDivElement>(null);
	useEdgeFade(scroller, "x");
	return (
		<div ref={scroller} className={cn("edge-fade edge-fade-x scrollbar-none -m-0.5 max-w-[min(78%,560px)] overflow-x-auto overflow-y-hidden overscroll-x-contain p-0.5", className)}>
			<div className="flex w-max items-start gap-2">{children}</div>
		</div>
	);
}

export function AttachmentFileText({ name, bytes, nameClassName }: { name: string; bytes?: number; nameClassName?: string }) {
	const details = [attachmentTypeLabel(name), bytes === undefined ? undefined : formatAttachmentSize(bytes)].filter(Boolean).join(" · ");
	return (
		<>
			<span className={cn("truncate", nameClassName)}>{name}</span>
			{details ? <span className="shrink-0 tabular-nums opacity-70">{details}</span> : null}
		</>
	);
}

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
	const local = useSentAttachmentPreview(path);
	const url = local.url ?? (apiBaseUrl === null ? undefined : attachmentURL(apiBaseUrl, sessionId, path));
	const image = imageIndex >= 0;
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
