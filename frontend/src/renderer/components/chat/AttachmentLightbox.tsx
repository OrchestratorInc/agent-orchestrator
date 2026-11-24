import { useRef, type ReactNode } from "react";
import { Dialog as DialogPrimitive } from "radix-ui";
import { useTranslation } from "react-i18next";
import { X } from "@/components/app-icons";
import { useRestoreFocusOnClose } from "../../hooks/useRestoreFocusOnClose";
import { mediaCrossOrigin } from "../../lib/file-media";
import { cn } from "../../lib/utils";

/**
 * An attached image or video at full size over a dimmed window: its name and
 * details in the top left corner, a close button in the top right, nothing
 * else. Escape, the button, or a click outside the picture closes it.
 */
export function AttachmentLightbox({
	open,
	onOpenChange,
	kind,
	src,
	name,
	details,
	wrapMedia,
	onError,
}: {
	open: boolean;
	onOpenChange: (open: boolean) => void;
	kind: "image" | "video";
	src: string;
	name: string;
	/** Type and size, shown under the name when known. */
	details?: string;
	/** Wraps the picture, e.g. in its right-click menu. */
	wrapMedia?: (media: ReactNode) => ReactNode;
	/** Told when the picture fails to load or play, so the caller can fall back to another source. */
	onError?: () => void;
}) {
	const { t } = useTranslation();
	// Nothing here is a Radix trigger, so focus goes back to whatever opened the preview.
	const restoreFocusOnClose = useRestoreFocusOnClose(open);
	const pressStartedOutside = useRef(false);
	const mediaClass = "block max-h-[calc(100svh-8rem)] max-w-[calc(100vw-8rem)] rounded-xl object-contain shadow-[0_24px_64px_-16px_rgb(0_0_0/0.6)]";
	const bare = kind === "video"
		? <video crossOrigin={mediaCrossOrigin(src)} controls autoPlay playsInline src={src} aria-label={name} onError={onError} className={cn(mediaClass, "bg-black")} />
		: <img crossOrigin={mediaCrossOrigin(src)} src={src} alt={name} onError={onError} className={mediaClass} />;
	return (
		<DialogPrimitive.Root open={open} onOpenChange={onOpenChange}>
			<DialogPrimitive.Portal>
				<DialogPrimitive.Overlay className="fixed inset-0 z-overlay bg-black/75 backdrop-blur-sm data-[state=open]:animate-overlay-in data-[state=closed]:animate-overlay-out motion-reduce:animate-none" />
				<DialogPrimitive.Content
					aria-describedby={undefined}
					onCloseAutoFocus={restoreFocusOnClose}
					// The content fills the window, so a press on its empty part is outside the picture.
					// Only a press that starts and ends there closes: dragging off the video's scrubber doesn't.
					onMouseDown={(event) => {
						pressStartedOutside.current = event.target === event.currentTarget;
					}}
					onClick={(event) => {
						if (pressStartedOutside.current && event.target === event.currentTarget) onOpenChange(false);
						pressStartedOutside.current = false;
					}}
					className="fixed inset-0 z-overlay flex items-center justify-center outline-none data-[state=open]:animate-modal-in data-[state=closed]:animate-modal-out motion-reduce:animate-none"
				>
					{/* Stops short of the close button, so a long name truncates before reaching it. */}
					<div className="pointer-events-none fixed left-5 right-20 top-4 flex flex-col gap-0.5 leading-tight">
						<DialogPrimitive.Title className="truncate text-control font-normal text-white/90">{name}</DialogPrimitive.Title>
						{details ? <span className="truncate text-sm-md text-white/55 tabular-nums">{details}</span> : null}
					</div>
					{wrapMedia ? wrapMedia(bare) : bare}
					<DialogPrimitive.Close
						aria-label={t("common.close")}
						className="fixed right-4 top-4 flex size-9 items-center justify-center rounded-full bg-white/12 text-white outline-none transition-colors hover:bg-white/22 focus-visible:ring-2 focus-visible:ring-white/70"
					>
						<X aria-hidden="true" className="size-4" strokeWidth={2.25} />
					</DialogPrimitive.Close>
				</DialogPrimitive.Content>
			</DialogPrimitive.Portal>
		</DialogPrimitive.Root>
	);
}
