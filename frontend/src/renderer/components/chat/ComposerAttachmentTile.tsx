import { lazy, Suspense, useEffect, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { Play, X } from "@/components/app-icons";
import { mediaCrossOrigin } from "../../lib/file-media";
import { cn } from "../../lib/utils";
import { artifactFileIcon } from "../artifact-file-icon";
import { Button } from "../ui/button";
import { Dialog, DialogContent, DialogTitle } from "../ui/dialog";
import { Pressable } from "../ui/pressable";
import { ChatImageDialog } from "./ChatImage";
import { attachmentPreviewKind, attachmentTypeLabel, formatAttachmentSize } from "./messageAttachments";

const PdfPreview = lazy(() => import("../PdfPreview").then((module) => ({ default: module.PdfPreview })));

/** Every composer tile is this tall, so images, videos and file cards sit on one line. */
const TILE_CLASS = "relative h-14 shrink-0 rounded-lg border border-border bg-interactive-hover";
const SQUARE_CLASS = "size-14 overflow-hidden rounded-[inherit]";

/** A tile's corner remove control: a small raised circle that overlaps the edge. */
export function ComposerTileRemove({ label, disabled, onRemove }: { label: string; disabled?: boolean; onRemove: () => void }) {
	return (
		<Button
			type="button"
			variant="secondary"
			size="none"
			shape="pill"
			disabled={disabled}
			aria-label={label}
			// Keep focus in the editor: removing a tile shouldn't move the caret.
			onMouseDown={(event) => {
				if (event.button === 0) event.preventDefault();
			}}
			onClick={onRemove}
			className="absolute -right-1.5 -top-1.5 z-10 size-5 [&_svg]:size-3"
		>
			<X aria-hidden="true" />
		</Button>
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
	/** The staged copy's URL, used when the picked file is gone. */
	mediaUrl?: string;
	removeLabel: string;
	removeDisabled?: boolean;
	onRemove: () => void;
}) {
	const remove = <ComposerTileRemove label={removeLabel} disabled={removeDisabled} onRemove={onRemove} />;
	const previewKind = attachmentPreviewKind(name);
	const localUrl = useFileObjectUrl(!imageSrc && (previewKind === "video" || previewKind === "pdf") ? file : undefined);
	const src = localUrl ?? mediaUrl;
	if (imageSrc) return <ImageTile src={imageSrc} name={name}>{remove}</ImageTile>;
	if (previewKind === "video" && src) return <VideoTile src={src} name={name}>{remove}</VideoTile>;
	return <FileCard name={name} bytes={bytes} pdfSrc={previewKind === "pdf" ? src : undefined}>{remove}</FileCard>;
}

function ImageTile({ src, name, children }: { src: string; name: string; children: ReactNode }) {
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
			<ChatImageDialog open={open} onOpenChange={setOpen} url={src} label={name} />
			{children}
		</li>
	);
}

function VideoTile({ src, name, children }: { src: string; name: string; children: ReactNode }) {
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
			{open ? (
				<Dialog open onOpenChange={setOpen}>
					<DialogContent aria-describedby={undefined} className="flex h-[85vh] max-w-4xl flex-col gap-2 p-3">
						<DialogTitle className="truncate text-sm">{name}</DialogTitle>
						<video crossOrigin={mediaCrossOrigin(src)} controls autoPlay playsInline src={src} className="size-full min-h-0 object-contain" />
					</DialogContent>
				</Dialog>
			) : null}
			{children}
		</li>
	);
}

function FileCard({ name, bytes, pdfSrc, children }: { name: string; bytes?: number; pdfSrc?: string; children: ReactNode }) {
	const [pdfFailed, setPdfFailed] = useState(false);
	const Icon = artifactFileIcon({ kind: "file", path: name });
	const details = [attachmentTypeLabel(name), bytes === undefined ? undefined : formatAttachmentSize(bytes)].filter(Boolean).join(" · ");
	return (
		<li title={name} className={cn(TILE_CLASS, "flex max-w-60 items-center gap-2.5 pr-3")}>
			<span aria-hidden="true" className={cn(SQUARE_CLASS, "relative flex shrink-0 items-center justify-center rounded-r-none")}>
				{pdfSrc && !pdfFailed ? (
					<span inert className="pointer-events-none absolute inset-0 flex flex-col overflow-hidden bg-white">
						<Suspense fallback={null}>
							<PdfPreview cover onError={() => setPdfFailed(true)} path={name} src={pdfSrc} />
						</Suspense>
					</span>
				) : (
					<Icon className="size-5 text-muted-foreground" />
				)}
			</span>
			<span className="flex min-w-0 flex-col gap-0.5 leading-tight">
				<span className="truncate text-sm text-foreground">{name}</span>
				{details ? <span className="truncate text-xs text-muted-foreground tabular-nums">{details}</span> : null}
			</span>
			{children}
		</li>
	);
}

/** A file still being read or staged: a card-shaped placeholder that can be cancelled. */
export function ComposerPendingTile({ name, statusLabel, cancelLabel, onCancel }: { name: string; statusLabel: string; cancelLabel: string; onCancel: () => void }) {
	return (
		<li role="status" aria-label={statusLabel} className={cn(TILE_CLASS, "flex w-44 items-center gap-2.5 pr-3")}>
			<span aria-hidden="true" className={cn(SQUARE_CLASS, "shrink-0 bg-surface motion-safe:animate-pulse")} />
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
