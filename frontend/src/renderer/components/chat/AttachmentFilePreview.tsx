import { useEffect, useRef, useState } from "react";
import { mediaCrossOrigin } from "../../lib/file-media";
import { cn } from "../../lib/utils";
import { PdfPreview } from "../PdfPreview";
import { Dialog, DialogContent, DialogTitle } from "../ui/dialog";
import { Pressable } from "../ui/pressable";
import { useChatImageSrc } from "./chat-image-source";
import {
	ATTACHMENT_TILE_OVERLAY_PILL_CLASS,
	ATTACHMENT_TILE_PILL_CLASS,
	attachmentTypeLabel,
	type AttachmentPreviewKind,
} from "./messageAttachments";

/** Enough of a text file to fill a tile. */
const TEXT_PREVIEW_BYTES = 2048;
const PREVIEW_CLASS = "pointer-events-none absolute inset-0 flex flex-col overflow-hidden";

/**
 * A preview laid over a file tile: a video's first frame, a PDF's first page, or
 * the start of a text file, with its type as a chip. It loads only near the
 * viewport, and the name card under it shows until it paints or if it fails.
 */
export function AttachmentFilePreview({ kind, url, name }: { kind: AttachmentPreviewKind; url: string; name: string }) {
	const src = useChatImageSrc(url);
	const sentinel = useRef<HTMLSpanElement>(null);
	const [near, setNear] = useState(false);
	const [failed, setFailed] = useState(false);
	const [open, setOpen] = useState(false);
	const [text, setText] = useState<string>();

	useEffect(() => {
		const element = sentinel.current;
		if (!element || typeof IntersectionObserver === "undefined") return;
		const observer = new IntersectionObserver(([entry]) => {
			if (!entry?.isIntersecting) return;
			setNear(true);
			observer.disconnect();
		}, { rootMargin: "200px" });
		observer.observe(element);
		return () => observer.disconnect();
	}, []);

	useEffect(() => {
		if (!near || kind !== "text" || !src) return;
		const controller = new AbortController();
		void fetch(`${src}${src.includes("?") ? "&" : "?"}raw=true`, { signal: controller.signal, headers: { Range: `bytes=0-${TEXT_PREVIEW_BYTES - 1}` } })
			.then(async (response) => {
				if (!response.ok) throw new Error(`Preview failed: ${response.status}`);
				return response.text();
			})
			.then(setText)
			.catch(() => {
				if (!controller.signal.aborted) setFailed(true);
			});
		return () => controller.abort();
	}, [near, kind, src]);

	const onError = () => setFailed(true);
	const type = attachmentTypeLabel(name);
	const preview = !near || failed || !src
		? null
		: kind === "video"
			? <video crossOrigin={mediaCrossOrigin(src)} muted onError={onError} playsInline preload="metadata" src={src} tabIndex={-1} className="size-full object-cover" />
			: kind === "pdf"
				? <PdfPreview firstPageOnly onError={onError} path={name} src={src} />
				: text
					? <pre className="m-0 size-full overflow-hidden whitespace-pre-wrap break-words bg-background p-2 font-mono text-micro leading-tight text-foreground">{text}</pre>
					: null;

	return (
		<>
			<span ref={sentinel} aria-hidden="true" className="pointer-events-none absolute inset-0" />
			{preview ? (
				<>
					{kind === "text" ? (
						<span inert className={PREVIEW_CLASS}>{preview}</span>
					) : (
						<Pressable
							onClick={() => setOpen(true)}
							aria-label={name}
							className="absolute inset-0 cursor-zoom-in focus-visible:outline-2 focus-visible:-outline-offset-2 focus-visible:outline-ring"
						>
							<span inert className={PREVIEW_CLASS}>{preview}</span>
						</Pressable>
					)}
					{type ? <span className={cn(ATTACHMENT_TILE_PILL_CLASS, ATTACHMENT_TILE_OVERLAY_PILL_CLASS)}>{type}</span> : null}
				</>
			) : null}
			{open && src ? (
				<Dialog open onOpenChange={setOpen}>
					<DialogContent aria-describedby={undefined} className="flex h-[85vh] max-w-4xl flex-col gap-2 p-3">
						<DialogTitle className="truncate text-sm">{name}</DialogTitle>
						<div className="flex min-h-0 flex-1 flex-col overflow-hidden">
							{kind === "pdf" ? (
								<PdfPreview onError={onError} path={name} src={src} />
							) : (
								<video crossOrigin={mediaCrossOrigin(src)} controls onError={onError} playsInline preload="metadata" src={src} className="size-full min-h-0 object-contain" />
							)}
						</div>
					</DialogContent>
				</Dialog>
			) : null}
		</>
	);
}
