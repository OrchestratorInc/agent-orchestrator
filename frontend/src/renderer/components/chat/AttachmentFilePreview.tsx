import { lazy, Suspense, useCallback, useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { mediaCrossOrigin } from "../../lib/file-media";
import { cn } from "../../lib/utils";
import { useUiStore } from "../../stores/ui-store";
import { Button } from "../ui/button";
import { Dialog, DialogContent, DialogTitle } from "../ui/dialog";
import { Pressable } from "../ui/pressable";
import { AttachmentLightbox } from "./AttachmentLightbox";
import { CopyButton } from "./CopyButton";
import { useChatImageSrc } from "./chat-image-source";
import {
	ATTACHMENT_TILE_OVERLAY_PILL_CLASS,
	ATTACHMENT_TILE_PILL_CLASS,
	ATTACHMENT_TILE_SOLID_FILL_CLASS,
	attachmentTypeLabel,
	formatAttachmentSize,
	type AttachmentPreviewKind,
} from "./messageAttachments";

// Loaded on first use: the PDF viewer pulls in the diff library, which registers
// custom elements, and the composer and every chat import this file.
const PdfPreview = lazy(() => import("../PdfPreview").then((module) => ({ default: module.PdfPreview })));

/** Enough of a text file to fill a tile. */
export const TEXT_PREVIEW_BYTES = 2048;

/** As much of a text file as the viewer reads into the window. */
const TEXT_VIEWER_BYTES = 4 * 1024 * 1024;

/**
 * The first `limit` bytes of a response, and whether it had more, then the read
 * is cancelled. A file just sent is read from its local copy, which takes no
 * Range, and it may be gigabytes: reading it whole would stream all of it.
 */
async function readTextUpTo(response: Response, limit: number): Promise<{ text: string; more: boolean }> {
	const reader = response.body?.getReader();
	if (!reader) {
		const text = await response.text();
		return { text: text.slice(0, limit), more: text.length > limit };
	}
	const chunks: Uint8Array[] = [];
	let size = 0;
	try {
		while (size <= limit) {
			const { done, value } = await reader.read();
			if (done) break;
			chunks.push(value);
			size += value.byteLength;
		}
	} finally {
		void reader.cancel().catch(() => undefined);
	}
	const bytes = new Uint8Array(Math.min(size, limit));
	let offset = 0;
	for (const chunk of chunks) {
		if (offset >= bytes.length) break;
		const part = chunk.subarray(0, bytes.length - offset);
		bytes.set(part, offset);
		offset += part.length;
	}
	return { text: new TextDecoder().decode(bytes, { stream: size > limit }), more: size > limit };
}

/** The first TEXT_PREVIEW_BYTES of a response, for a tile. */
export async function readTextPrefix(response: Response): Promise<string> {
	return (await readTextUpTo(response, TEXT_PREVIEW_BYTES)).text;
}
const PREVIEW_CLASS = "pointer-events-none absolute inset-0 flex flex-col overflow-hidden";

function rawTextUrl(src: string): string {
	return src.startsWith("blob:") ? src : `${src}${src.includes("?") ? "&" : "?"}raw=true`;
}

async function saveFile(src: string, name: string) {
	const local = src.startsWith("blob:");
	let url = src;
	if (!local) {
		const response = await fetch(rawTextUrl(src));
		if (!response.ok) throw new Error(`Save failed: ${response.status}`);
		url = URL.createObjectURL(await response.blob());
	}
	const link = document.createElement("a");
	link.href = url;
	link.download = name;
	link.click();
	if (!local) setTimeout(() => URL.revokeObjectURL(url), 0);
}

function TextViewer({ src, name, start, onOpenChange }: {
	src: string;
	name: string;
	start?: string;
	onOpenChange: (open: boolean) => void;
}) {
	const { t } = useTranslation();
	const [full, setFull] = useState<{ text: string; more: boolean }>();
	const [failed, setFailed] = useState(false);
	const [attempt, setAttempt] = useState(0);
	const [saving, setSaving] = useState(false);
	useEffect(() => {
		const controller = new AbortController();
		void fetch(rawTextUrl(src), {
			signal: controller.signal,
			headers: src.startsWith("blob:") ? undefined : { Range: `bytes=0-${TEXT_VIEWER_BYTES}` },
		})
			.then((response) => {
				if (!response.ok) throw new Error(`Read failed: ${response.status}`);
				return readTextUpTo(response, TEXT_VIEWER_BYTES);
			})
			.then(setFull)
			.catch(() => {
				if (!controller.signal.aborted) setFailed(true);
			});
		return () => controller.abort();
	}, [src, attempt]);
	return (
		<Dialog open onOpenChange={onOpenChange}>
			<DialogContent aria-describedby={undefined} className="flex h-[85vh] max-w-4xl flex-col gap-2 p-3">
				<div className="flex min-w-0 items-center gap-2 pr-8">
					<DialogTitle className="min-w-0 flex-1 truncate text-sm">{name}</DialogTitle>
					{full?.more ? (
						<>
							<span className="text-xs text-muted-foreground">{t("chat.attachment.viewerTruncated", { size: formatAttachmentSize(TEXT_VIEWER_BYTES) })}</span>
							<Button
								type="button"
								variant="secondary"
								size="xs"
								disabled={saving}
								onClick={() => {
									setSaving(true);
									void saveFile(src, name)
										.catch(() => useUiStore.getState().showGlobalToast(t("chat.attachment.saveFailed"), undefined, "error"))
										.finally(() => setSaving(false));
								}}
							>
								{t("chat.attachment.saveWhole")}
							</Button>
						</>
					) : full ? (
						<CopyButton text={full.text} label="Copy text" />
					) : failed ? (
						<>
							<span role="alert" className="text-xs text-destructive">{t("chat.attachment.readFailed")}</span>
							<Button
								type="button"
								variant="secondary"
								size="xs"
								onClick={() => {
									setFailed(false);
									setAttempt((count) => count + 1);
								}}
							>
								{t("files.retry")}
							</Button>
						</>
					) : (
						<span className="text-xs text-muted-foreground">{t("common.loading")}</span>
					)}
				</div>
				<pre className="m-0 min-h-0 flex-1 select-text overflow-auto whitespace-pre-wrap break-words font-mono text-sm text-foreground">{full?.text ?? start}</pre>
			</DialogContent>
		</Dialog>
	);
}

/**
 * A preview laid over a file tile: a video's first frame, a PDF's first page, or
 * the start of a text file, with its type as a chip. It loads only near the
 * viewport, and the name card under it shows until it paints or if it fails.
 */
export function AttachmentFilePreview({ kind, url, name, onError: onLoadError }: {
	kind: AttachmentPreviewKind;
	url: string;
	name: string;
	/** Told when `url` fails, so the caller can offer another source in its place. */
	onError?: () => void;
}) {
	const src = useChatImageSrc(url);
	const sentinel = useRef<HTMLSpanElement>(null);
	const [near, setNear] = useState(false);
	// The failed URL rather than a flag: a replacement source gets its own attempt.
	const [failedSrc, setFailedSrc] = useState<string>();
	const failed = src !== undefined && src === failedSrc;
	const [open, setOpen] = useState(false);
	const [text, setText] = useState<string>();
	const onLoadErrorRef = useRef(onLoadError);
	onLoadErrorRef.current = onLoadError;
	const fail = useCallback((failedUrl: string) => {
		setFailedSrc(failedUrl);
		onLoadErrorRef.current?.();
	}, []);

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
		// A file just sent is read from its in-memory copy, which takes no query or range.
		const local = src.startsWith("blob:");
		void fetch(rawTextUrl(src), {
			signal: controller.signal,
			headers: local ? undefined : { Range: `bytes=0-${TEXT_PREVIEW_BYTES - 1}` },
		})
			.then((response) => {
				if (!response.ok) throw new Error(`Preview failed: ${response.status}`);
				return readTextPrefix(response);
			})
			.then(setText)
			.catch(() => {
				if (!controller.signal.aborted) fail(src);
			});
		return () => controller.abort();
	}, [near, kind, src, fail]);

	const onError = () => {
		if (src) fail(src);
	};
	const type = attachmentTypeLabel(name);
	const preview = !near || failed || !src
		? null
		: kind === "video"
			? <video crossOrigin={mediaCrossOrigin(src)} muted onError={onError} playsInline preload="metadata" src={src} tabIndex={-1} className="size-full object-cover" />
			: kind === "pdf"
				? <PdfPreview cover onError={onError} path={name} src={src} />
				: text
					? <pre className={cn("m-0 size-full overflow-hidden whitespace-pre-wrap break-words p-2 font-mono text-micro leading-tight text-foreground", ATTACHMENT_TILE_SOLID_FILL_CLASS)}>{text}</pre>
					: null;

	return (
		<>
			<span ref={sentinel} aria-hidden="true" className="pointer-events-none absolute inset-0" />
			{src && (preview || kind === "text") ? (
				<>
					<Pressable
						onClick={() => setOpen(true)}
						aria-label={name}
						className="absolute inset-0 cursor-zoom-in focus-visible:outline-2 focus-visible:-outline-offset-2 focus-visible:outline-ring"
					>
						{preview ? (
							<span inert className={PREVIEW_CLASS}>
								<Suspense fallback={null}>{preview}</Suspense>
							</span>
						) : null}
					</Pressable>
					{preview && type ? <span className={cn(ATTACHMENT_TILE_PILL_CLASS, ATTACHMENT_TILE_OVERLAY_PILL_CLASS)}>{type}</span> : null}
				</>
			) : null}
			{open && src && kind === "video" ? (
				<AttachmentLightbox open onOpenChange={setOpen} kind="video" src={src} name={name} details={type} onError={onError} />
			) : open && src && kind === "text" ? (
				<TextViewer src={src} name={name} start={text} onOpenChange={setOpen} />
			) : open && src ? (
				<Dialog open onOpenChange={setOpen}>
					<DialogContent aria-describedby={undefined} className="flex h-[85vh] max-w-4xl flex-col gap-2 p-3">
						<DialogTitle className="truncate text-sm">{name}</DialogTitle>
						<div className="flex min-h-0 flex-1 flex-col overflow-hidden">
							<Suspense fallback={null}>
								<PdfPreview onError={onError} path={name} src={src} />
							</Suspense>
						</div>
					</DialogContent>
				</Dialog>
			) : null}
		</>
	);
}
