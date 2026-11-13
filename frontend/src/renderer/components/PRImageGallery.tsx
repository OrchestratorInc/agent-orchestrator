import { ArrowUpRight, ChevronLeft, ChevronRight, ImageIcon } from "lucide-react";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { cn } from "../lib/utils";
import { ProductExternalLink } from "./ProductExternalLink";
import { Dialog, DialogContent, DialogDescription, DialogTitle } from "./ui/dialog";

export type DescriptionImage = { alt: string; src: string };

/** A thumbnail that shows an icon instead when the picture can't load. */
function Thumbnail({ image }: { image: DescriptionImage }) {
	const [failed, setFailed] = useState(false);
	if (failed) return <ImageIcon aria-hidden="true" className="size-icon-sm text-passive" />;
	return <img alt="" className="size-full object-cover object-top" onError={() => setFailed(true)} src={image.src} />;
}

/**
 * The description's images, one at a time over the app: full size, stepped
 * through with the arrows, the arrow keys or the thumbnail row. An image the
 * provider only serves to a signed-in browser says so and links to the original.
 */
export function PRImageGallery({ images, index, onIndexChange }: {
	images: DescriptionImage[];
	/** The image shown, or null when the gallery is closed. */
	index: number | null;
	onIndexChange: (index: number | null) => void;
}) {
	const { t } = useTranslation();
	// The last image stays in place while the gallery fades out, instead of an empty frame.
	const [last, setLast] = useState(0);
	const [failed, setFailed] = useState<ReadonlySet<string>>(() => new Set());
	useEffect(() => { if (index !== null) setLast(index); }, [index]);
	// Images can change under an open gallery (fresh details); past the end it closes.
	useEffect(() => { if (index !== null && index >= images.length) onIndexChange(null); }, [images.length, index, onIndexChange]);
	const shown = index ?? last;
	const image = images[shown];
	const count = images.length;
	const step = (delta: number) => { if (index !== null && count > 1) onIndexChange((index + delta + count) % count); };
	useEffect(() => {
		if (index === null || count < 2) return;
		const onKey = (event: KeyboardEvent) => {
			if (event.key === "ArrowRight") { event.preventDefault(); onIndexChange((index + 1) % count); }
			if (event.key === "ArrowLeft") { event.preventDefault(); onIndexChange((index - 1 + count) % count); }
		};
		window.addEventListener("keydown", onKey);
		return () => window.removeEventListener("keydown", onKey);
	}, [count, index, onIndexChange]);
	if (!image) return null;
	const name = image.alt.trim() || t("pr.description.image");
	const broken = failed.has(image.src);

	return (
		<Dialog onOpenChange={(open) => { if (!open) onIndexChange(null); }} open={index !== null}>
			<DialogContent className="flex w-[min(1040px,92vw)] max-w-none flex-col gap-2 p-3 sm:max-w-none" data-testid="pr-image-gallery">
				<DialogTitle className="m-0 flex min-w-0 items-baseline gap-1.5 pr-8 text-sm font-medium">
					<span className="min-w-0 truncate">{name}</span>
					{count > 1 ? <span className="shrink-0 text-xs font-normal text-muted-foreground">{t("pr.description.imagePosition", { index: shown + 1, count })}</span> : null}
				</DialogTitle>
				<DialogDescription className="sr-only">{t("pr.description.imageFromDescription")}</DialogDescription>
				{/* Room for the picture from the first frame, so the gallery doesn't open as a thin strip while it loads. */}
				<div className="relative flex min-h-[50vh] items-center justify-center overflow-hidden rounded-md bg-muted/40">
					{broken ? (
						<div className="flex max-w-sm flex-col items-center gap-2 p-6 text-center text-xs text-muted-foreground">
							<ImageIcon aria-hidden="true" className="size-icon-lg text-passive" />
							<p className="m-0">{t("pr.description.imageFailed")}</p>
						</div>
					) : (
						<img
							alt={name}
							className="max-h-[72vh] max-w-full object-contain"
							key={image.src}
							onError={() => setFailed((current) => new Set(current).add(image.src))}
							src={image.src}
						/>
					)}
					{count > 1 ? (
						<>
							<button aria-label={t("pr.description.previousImage")} className="absolute top-1/2 left-2 inline-flex size-9 -translate-y-1/2 items-center justify-center rounded-full bg-background/90 text-foreground shadow-md transition-colors hover:bg-background focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60" onClick={() => step(-1)} type="button">
								<ChevronLeft aria-hidden="true" className="size-icon-md" />
							</button>
							<button aria-label={t("pr.description.nextImage")} className="absolute top-1/2 right-2 inline-flex size-9 -translate-y-1/2 items-center justify-center rounded-full bg-background/90 text-foreground shadow-md transition-colors hover:bg-background focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60" onClick={() => step(1)} type="button">
								<ChevronRight aria-hidden="true" className="size-icon-md" />
							</button>
						</>
					) : null}
				</div>
				<div className="flex min-w-0 items-center gap-2">
					{count > 1 ? (
						<div className="flex min-w-0 flex-1 justify-center gap-1.5 overflow-x-auto">
							{images.map((item, i) => (
								<button
									aria-current={i === shown ? "true" : undefined}
									aria-label={t("pr.description.showImage", { index: i + 1 })}
									className={cn("inline-flex h-10 w-14 shrink-0 items-center justify-center overflow-hidden rounded border bg-muted/40 transition-opacity focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60", i === shown ? "border-ring ring-2 ring-ring/40" : "border-border opacity-70 hover:opacity-100")}
									key={`${item.src}-${i}`}
									onClick={() => onIndexChange(i)}
									type="button"
								>
									<Thumbnail image={item} />
								</button>
							))}
						</div>
					) : <span className="flex-1" />}
					<ProductExternalLink className="inline-flex shrink-0 items-center gap-0.5 text-xs font-medium text-muted-foreground transition-colors hover:text-foreground" href={image.src}>
						{t("pr.description.openOriginal")}
						<ArrowUpRight aria-hidden="true" className="size-icon-2xs" />
					</ProductExternalLink>
				</div>
			</DialogContent>
		</Dialog>
	);
}
