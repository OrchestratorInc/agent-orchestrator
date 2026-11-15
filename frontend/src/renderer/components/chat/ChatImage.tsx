import { mediaCrossOrigin } from "../../lib/file-media";
import { createContext, useContext, useState, type ReactNode } from "react";
import type { ExtraProps } from "react-markdown";
import { useTranslation } from "react-i18next";
import { Copy, Download } from "@/components/app-icons";
import { cn } from "../../lib/utils";
import { useUiStore } from "../../stores/ui-store";
import { ContextMenu, ContextMenuContent, ContextMenuItem, ContextMenuTrigger } from "../ui/context-menu";
import { Dialog, DialogContent, DialogTitle } from "../ui/dialog";
import { Pressable } from "../ui/pressable";
import { copyChatImage, saveChatImage } from "./chat-image-actions";
import { useChatImageSrc } from "./chat-image-source";
import { ATTACHMENT_TILE_CLASS } from "./messageAttachments";

const InGallery = createContext(false);
const InAttachmentRow = createContext(false);
const InLink = createContext(false);

export function isImageOnlyParagraph(node: ExtraProps["node"]): boolean {
	if (!node) return false;
	let images = 0;
	for (const child of node.children) {
		if (child.type === "element" && child.tagName === "img") images++;
		else if (child.type === "element" && child.tagName === "br") continue;
		else if (!(child.type === "text" && child.value.trim() === "")) return false;
	}
	return images >= 2;
}

export function ChatImageGallery({ children }: { children: ReactNode }) {
	const { t } = useTranslation();
	return (
		<div role="group" aria-label={t("chat.image.gallery")} className="my-2 flex flex-wrap gap-2 first:mt-0 last:mb-0">
			<InGallery.Provider value={true}>{children}</InGallery.Provider>
		</div>
	);
}

export function ChatImageAttachmentRow({ children }: { children: ReactNode }) {
	return <InAttachmentRow.Provider value={true}>{children}</InAttachmentRow.Provider>;
}

export function ChatImageLinkScope({ children }: { children: ReactNode }) {
	return <InLink.Provider value={true}>{children}</InLink.Provider>;
}

function ChatImageMenu({ url, label, children }: { url: string; label: string; children: ReactNode }) {
	const { t } = useTranslation();
	const showGlobalToast = useUiStore((state) => state.showGlobalToast);
	const run = (action: () => Promise<unknown>, success: string | null, failure: string) => {
		void action().then(
			(done) => {
				if (success && done !== false) showGlobalToast(success);
			},
			(error: unknown) => showGlobalToast(failure, error instanceof Error ? error.message : undefined, "error"),
		);
	};
	return (
		<ContextMenu>
			<ContextMenuTrigger asChild>{children}</ContextMenuTrigger>
			<ContextMenuContent className="min-w-40">
				<ContextMenuItem onSelect={() => run(() => copyChatImage(url), null, t("chat.image.copyFailed"))}>
					<Copy aria-hidden="true" />
					{t("chat.image.copy")}
				</ContextMenuItem>
				<ContextMenuItem onSelect={() => run(() => saveChatImage(url, label), null, t("chat.image.saveFailed"))}>
					<Download aria-hidden="true" />
					{t("chat.image.save")}
				</ContextMenuItem>
			</ContextMenuContent>
		</ContextMenu>
	);
}

export function ChatImage({ src, alt, inline, onError: onLoadError }: {
	src?: string | Blob;
	alt?: string;
	inline?: boolean;
	onError?: () => void;
}) {
	const { t } = useTranslation();
	const inGallery = useContext(InGallery);
	const inAttachmentRow = useContext(InAttachmentRow);
	const inLink = useContext(InLink);
	const [open, setOpen] = useState(false);
	const [failedSrc, setFailedSrc] = useState<string | null>(null);
	const rawUrl = typeof src === "string" ? src : undefined;
	const url = useChatImageSrc(rawUrl);
	const label = alt ?? "";

	if (!url || url === failedSrc)
		return (
			<span
				className={cn(
					"text-muted-foreground",
					inGallery && "inline-flex h-40 items-center rounded-md border border-border px-3",
					inAttachmentRow && ATTACHMENT_TILE_CLASS,
				)}
			>
				{label || rawUrl}
			</span>
		);

	const onError = () => {
		setFailedSrc(url);
		onLoadError?.();
	};

	if (inLink) {
		return (
			<img
				crossOrigin={mediaCrossOrigin(url)}
				src={url}
				alt={label}
				loading="lazy"
				onError={onError}
				className="inline-block h-auto max-h-80 max-w-full rounded-md object-contain align-middle"
			/>
		);
	}

	return (
		<>
			<ChatImageMenu url={url} label={label}>
				<Pressable
					onClick={() => setOpen(true)}
					aria-label={label ? t("chat.image.open", { name: label }) : t("chat.image.openUnnamed")}
					className={cn(
						"max-w-full cursor-zoom-in overflow-hidden rounded-md transition-opacity duration-150 hover:opacity-90 focus-visible:ring-2 focus-visible:ring-ring motion-reduce:transition-none",
						!inAttachmentRow && "border border-border bg-background",
						inline
							? "mx-0.5 inline-flex items-center gap-1 py-0.5 pl-0.5 pr-1.5 align-middle text-[0.9em] leading-none"
							: "inline-block align-top",
					)}
				>
					{inline ? (
						<>
							<img crossOrigin={mediaCrossOrigin(url)} src={url} alt="" onError={onError} className="size-4 shrink-0 rounded-sm object-cover" />
							<span>{label}</span>
						</>
					) : (
					<img
						crossOrigin={mediaCrossOrigin(url)}
						src={url}
						alt={label}
						loading="lazy"
						onError={onError}
						className={cn(
							"block max-w-full object-contain",
							inAttachmentRow
								? "h-16 w-auto min-w-16 max-w-32 rounded-md border border-border bg-background object-cover"
								: inGallery
									? "h-40 w-auto min-w-24"
									: "h-auto max-h-80",
						)}
					/>
					)}
				</Pressable>
			</ChatImageMenu>
			<ChatImageDialog open={open} onOpenChange={setOpen} url={url} label={label} />
		</>
	);
}

export function ChatImageDialog({ open, onOpenChange, url, label }: { open: boolean; onOpenChange: (open: boolean) => void; url: string; label: string }) {
	const { t } = useTranslation();
	return (
		<Dialog open={open} onOpenChange={onOpenChange}>
			<DialogContent
				aria-describedby={undefined}
				className="z-overlay w-max max-w-[calc(100vw-4rem)] gap-0 bg-popover p-2 pt-10"
			>
				<DialogTitle className="sr-only">{label || t("chat.image.untitled")}</DialogTitle>
				<ChatImageMenu url={url} label={label}>
					<img
						crossOrigin={mediaCrossOrigin(url)}
						src={url}
						alt={label}
						className="block max-h-[calc(100svh-8rem)] max-w-full rounded-md object-contain"
					/>
				</ChatImageMenu>
			</DialogContent>
		</Dialog>
	);
}
