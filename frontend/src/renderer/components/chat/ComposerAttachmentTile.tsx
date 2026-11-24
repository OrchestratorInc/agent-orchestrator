import { lazy, Suspense, useEffect, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { Play, X } from "@/components/app-icons";
import { mediaCrossOrigin } from "../../lib/file-media";
import { cn } from "../../lib/utils";
import { artifactFileIcon } from "../artifact-file-icon";
import { Pressable } from "../ui/pressable";
import { AttachmentLightbox } from "./AttachmentLightbox";
import { readTextPrefix, TEXT_PREVIEW_BYTES } from "./AttachmentFilePreview";
import { attachmentPreviewKind, attachmentTypeLabel, formatAttachmentSize } from "./messageAttachments";

const PdfPreview = lazy(() => import("../PdfPreview").then((module) => ({ default: module.PdfPreview })));

/** Every composer tile is this tall, so images, videos and file cards sit on one line. */
const TILE_CLASS = "relative h-14 shrink-0 rounded-lg border border-border bg-interactive-hover";
const SQUARE_CLASS = "size-14 overflow-hidden rounded-[inherit]";

/**
 * A tile's corner remove control: a solid disc ringed in the composer's color,
 * so it reads over any picture. Its look lives in `.composer-tile-remove`:
 * the `secondary` button fill turns clear in a translucent window.
 */
export function ComposerTileRemove({ label, disabled, onRemove }: { label: string; disabled?: boolean; onRemove: () => void }) {
	return (
		<Pressable
			disabled={disabled}
			aria-label={label}
			// Keep focus in the editor: removing a tile shouldn't move the caret.
			onMouseDown={(event) => {
				if (event.button === 0) event.preventDefault();
			}}
			onClick={onRemove}
			className="composer-tile-remove absolute -right-1.5 -top-1.5 z-10 flex size-5 items-center justify-center rounded-full focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring disabled:pointer-events-none disabled:opacity-50"
		>
			<X aria-hidden="true" className="size-3" strokeWidth={2.25} />
		</Pressable>
	);
}

/** An object URL for a picked file, revoked when the tile goes. */
function useFileObjectUrl(file: File | undefined): string | undefined {
	const [url, setUrl] = useState<string>();
	useEffect(() => {
		if (!file || typeof URL.createObjectURL !== "function") return;
		const next = URL.createObjectURL(file);
		setUrl(next);
		return () => {
			URL.revokeObjectURL(next);
			setUrl(undefined);
		};
	}, [file]);
	return url;
}

function formatDuration(seconds: number): string {
	const whole = Math.max(0, Math.round(seconds));
	const hours = Math.floor(whole / 3600);
	const minutes = Math.floor((whole % 3600) / 60);
	const rest = String(whole % 60).padStart(2, "0");
	return hours > 0 ? `${hours}:${String(minutes).padStart(2, "0")}:${rest}` : `${minutes}:${rest}`;
}

/**
 * One staged file in the composer. Images and videos are squares of their own
 * picture; other files are a card with a thumbnail (a PDF's first page, else
 * the file type's icon), the name and its type and size.
 */
export function ComposerAttachmentTile({
	name,
	bytes,
	imageSrc,
	file,
	mediaUrl,
	removeLabel,
	removeDisabled,
	onRemove,
}: {
	name: string;
	bytes?: number;
	/** The image's preview, when the file is one. */
	imageSrc?: string;
	/** The picked file, previewed from disk while it is in memory. */
	file?: File;
	/** The staged copy's URL (videos, PDFs and text files), used when the picked file is gone. */
	mediaUrl?: string;
	removeLabel: string;
	removeDisabled?: boolean;
	onRemove: () => void;
}) {
	const remove = <ComposerTileRemove label={removeLabel} disabled={removeDisabled} onRemove={onRemove} />;
	const previewKind = attachmentPreviewKind(name);
	const localUrl = useFileObjectUrl(!imageSrc && (previewKind === "video" || previewKind === "pdf") ? file : undefined);
	const src = localUrl ?? mediaUrl;
	const details = [attachmentTypeLabel(name), bytes === undefined ? undefined : formatAttachmentSize(bytes)].filter(Boolean).join(" · ") || undefined;
	if (imageSrc) return <ImageTile src={imageSrc} name={name} details={details}>{remove}</ImageTile>;
	if (previewKind === "video" && src) return <VideoTile src={src} name={name} details={details}>{remove}</VideoTile>;
	return (
		<FileCard name={name} bytes={bytes}>
			{previewKind === "pdf" && src ? <PdfThumbnail src={src} name={name} /> : null}
			{previewKind === "text" ? <TextThumbnail file={file} url={mediaUrl} /> : null}
			{remove}
		</FileCard>
	);
}

function ImageTile({ src, name, details, children }: { src: string; name: string; details?: string; children: ReactNode }) {
	const { t } = useTranslation();
	const [open, setOpen] = useState(false);
	return (
		<li title={name} className={cn(TILE_CLASS, "size-14")}>
			<Pressable
				onClick={() => setOpen(true)}
				aria-label={t("chat.image.open", { name })}
				className={cn(SQUARE_CLASS, "block cursor-zoom-in focus-visible:outline-2 focus-visible:-outline-offset-2 focus-visible:outline-ring")}
			>
				<img src={src} alt="" className="block size-full object-cover" />
			</Pressable>
			<AttachmentLightbox open={open} onOpenChange={setOpen} kind="image" src={src} name={name} details={details} />
			{children}
		</li>
	);
}

function VideoTile({ src, name, details, children }: { src: string; name: string; details?: string; children: ReactNode }) {
	const [open, setOpen] = useState(false);
	const [duration, setDuration] = useState<number>();
	const [failed, setFailed] = useState(false);
	return (
		<li title={name} className={cn(TILE_CLASS, "size-14")}>
			<Pressable
				onClick={() => setOpen(true)}
				aria-label={name}
				className={cn(SQUARE_CLASS, "relative block cursor-zoom-in focus-visible:outline-2 focus-visible:-outline-offset-2 focus-visible:outline-ring")}
			>
				{failed ? null : (
					<video
						crossOrigin={mediaCrossOrigin(src)}
						muted
						playsInline
						preload="metadata"
						src={src}
						tabIndex={-1}
						onLoadedMetadata={(event) => {
							const value = event.currentTarget.duration;
							if (Number.isFinite(value)) setDuration(value);
						}}
						onError={() => setFailed(true)}
						className="pointer-events-none size-full object-cover"
					/>
				)}
				<span aria-hidden="true" className="pointer-events-none absolute inset-0 flex items-center justify-center">
					<span className="flex size-6 items-center justify-center rounded-full bg-black/55 text-white">
						<Play className="size-3 translate-x-px fill-current" />
					</span>
				</span>
				{duration === undefined ? null : (
					<span className="pointer-events-none absolute bottom-1 left-1 rounded-xs bg-black/60 px-1 font-mono text-micro leading-4 text-white tabular-nums">
						{formatDuration(duration)}
					</span>
				)}
			</Pressable>
			<AttachmentLightbox open={open} onOpenChange={setOpen} kind="video" src={src} name={name} details={details} />
			{children}
		</li>
	);
}

/** The inset square a file card shows its preview in: a small page with rounded corners. */
const THUMB_CLASS = "relative flex size-11 shrink-0 items-center justify-center overflow-hidden rounded-md";

/**
 * A file card: its thumbnail, then the name and its type and size. The
 * thumbnail is the first child that draws one, else the file type's icon.
 */
function FileCard({ name, bytes, children }: { name: string; bytes?: number; children: ReactNode }) {
	const Icon = artifactFileIcon({ kind: "file", path: name });
	const details = [attachmentTypeLabel(name), bytes === undefined ? undefined : formatAttachmentSize(bytes)].filter(Boolean).join(" · ");
	return (
		<li title={name} className={cn(TILE_CLASS, "flex w-50 items-center gap-2.5 p-1.5 pr-4")}>
			{/* The icon shows until a preview paints over it, or when there is none. */}
			<span aria-hidden="true" className={cn(THUMB_CLASS, "bg-interactive-hover")}>
				<Icon className="size-5 text-muted-foreground" />
			</span>
			<span className="flex min-w-0 flex-col gap-0.5 leading-tight">
				<span className="truncate text-control text-foreground">{name}</span>
				{details ? <span className="truncate text-sm-md text-muted-foreground tabular-nums">{details}</span> : null}
			</span>
			{children}
		</li>
	);
}

/** Lays a preview over the card's icon square. */
function ThumbnailPage({ children }: { children: ReactNode }) {
	return (
		<span aria-hidden="true" inert className={cn(THUMB_CLASS, "pointer-events-none absolute left-1.5 top-1.5 block bg-white")}>
			{children}
		</span>
	);
}

/** A PDF's first page, cropped to the card's square. */
function PdfThumbnail({ src, name }: { src: string; name: string }) {
	const [failed, setFailed] = useState(false);
	if (failed) return null;
	return (
		<ThumbnailPage>
			<span className="absolute inset-0 flex flex-col">
				<Suspense fallback={null}>
					<PdfPreview cover onError={() => setFailed(true)} path={name} src={src} />
				</Suspense>
			</span>
		</ThumbnailPage>
	);
}

/** The start of a text file, as a tiny page of monospace lines. */
function TextThumbnail({ file, url }: { file?: File; url?: string }) {
	const [text, setText] = useState<string>();
	useEffect(() => {
		setText(undefined);
		const controller = new AbortController();
		const read = file
			? file.slice(0, TEXT_PREVIEW_BYTES).text()
			: url
				? fetch(`${url}${url.includes("?") ? "&" : "?"}raw=true`, {
					signal: controller.signal,
					headers: { Range: `bytes=0-${TEXT_PREVIEW_BYTES - 1}` },
				}).then((response) => {
					if (!response.ok) throw new Error(`Preview failed: ${response.status}`);
					return readTextPrefix(response);
				})
				: undefined;
		void read?.then((value) => {
			if (!controller.signal.aborted) setText(value);
		}).catch(() => undefined);
		return () => controller.abort();
	}, [file, url]);
	if (!text?.trim()) return null;
	return (
		<ThumbnailPage>
			{/* Drawn at a readable size and scaled down, so it reads as a page in miniature. */}
			<pre className="absolute left-0 top-0 m-0 w-[275%] origin-top-left scale-[0.364] overflow-hidden whitespace-pre-wrap break-all p-2.5 font-mono text-micro leading-snug text-black/75">
				{text}
			</pre>
		</ThumbnailPage>
	);
}

/** A file still being read or staged: a card-shaped placeholder that can be cancelled. */
export function ComposerPendingTile({ name, statusLabel, cancelLabel, onCancel }: { name: string; statusLabel: string; cancelLabel: string; onCancel: () => void }) {
	return (
		<li role="status" aria-label={statusLabel} className={cn(TILE_CLASS, "flex w-50 items-center gap-2.5 p-1.5 pr-4")}>
			<span aria-hidden="true" className={cn(THUMB_CLASS, "bg-surface motion-safe:animate-pulse")} />
			<span aria-hidden="true" className="flex min-w-0 flex-1 flex-col gap-1.5">
				<span className="relative truncate text-sm motion-safe:animate-pulse">
					<span className="invisible">{name}</span>
					<span className="absolute inset-x-0 top-1/2 h-2.5 -translate-y-1/2 rounded-sm bg-surface" />
				</span>
				<span className="h-2 w-12 rounded-sm bg-surface motion-safe:animate-pulse" />
			</span>
			<ComposerTileRemove label={cancelLabel} onRemove={onCancel} />
		</li>
	);
}
