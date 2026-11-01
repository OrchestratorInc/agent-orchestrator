import { ArrowUpRight, ImageIcon, X } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { cn } from "../lib/utils";
import { ProductExternalLink } from "./ProductExternalLink";
import { Button } from "./ui/button";
import { Dialog, DialogClose, DialogContent, DialogDescription, DialogTitle, settingsDialogContentClass } from "./ui/dialog";

/** Only web images open in place; anything else stays a plain label. */
export function isWebImageUrl(src: string | undefined): src is string {
	if (!src) return false;
	try {
		const url = new URL(src);
		return url.protocol === "https:" || url.protocol === "http:";
	} catch {
		return false;
	}
}

/**
 * An image in the PR body as a small placeholder in the text. It loads nothing
 * until it is opened; then the picture shows centred over the app, with a
 * link to the original for images the provider only serves to a signed-in
 * browser (attachments on a private repository).
 */
export function PRDescriptionImage({ alt, src }: { alt?: string; src?: string }) {
	const { t } = useTranslation();
	const [open, setOpen] = useState(false);
	const [failed, setFailed] = useState(false);
	const name = alt?.trim() || t("pr.description.image");
	if (!isWebImageUrl(src)) {
		return (
			<span className="inline-flex max-w-full items-center gap-1 align-middle text-passive">
				<ImageIcon aria-hidden="true" className="size-icon-2xs shrink-0" />
				<span className="truncate">{name}</span>
			</span>
		);
	}
	return (
		<Dialog open={open} onOpenChange={(next) => { setOpen(next); if (next) setFailed(false); }}>
			<button
				aria-label={t("pr.description.openImage", { name })}
				className={cn(
					"inline-flex max-w-full items-center gap-1 rounded-sm border border-border/60 bg-muted/40 px-1.5 py-px align-middle text-xs text-muted-foreground",
					"transition-colors hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60",
				)}
				data-testid="pr-description-image"
				onClick={() => setOpen(true)}
				type="button"
			>
				<ImageIcon aria-hidden="true" className="size-icon-2xs shrink-0" />
				<span className="truncate">{name}</span>
			</button>
			{open ? (
				<DialogContent
					className={cn(settingsDialogContentClass, "max-h-[calc(100svh-var(--space-8))] w-auto max-w-[min(72rem,calc(100vw-var(--space-8)))] gap-0")}
					showCloseButton={false}
				>
					<header className="flex shrink-0 items-center gap-3 border-b border-border px-4 py-3">
						<DialogTitle className="min-w-0 flex-1 truncate text-heading-sm">{name}</DialogTitle>
						<DialogDescription className="sr-only">{t("pr.description.imageFromDescription")}</DialogDescription>
						<Button asChild className="shrink-0" size="sm" variant="secondary">
							<ProductExternalLink href={src}>
								{t("pr.description.openOriginal")}
								<ArrowUpRight aria-hidden="true" className="size-icon-sm" />
							</ProductExternalLink>
						</Button>
						<DialogClose asChild>
							<Button aria-label={t("common.close")} className="shrink-0" size="icon-sm" variant="ghost">
								<X aria-hidden="true" className="size-icon-sm" />
							</Button>
						</DialogClose>
					</header>
					<div className="flex min-h-0 flex-1 items-center justify-center overflow-auto bg-background p-4">
						{failed ? (
							<p className="max-w-sm py-10 text-center text-sm text-muted-foreground">{t("pr.description.imageFailed")}</p>
						) : (
							<img
								alt={alt ?? ""}
								className="block max-h-[calc(100svh-var(--space-8)-6rem)] max-w-full object-contain"
								onError={() => setFailed(true)}
								referrerPolicy="no-referrer"
								src={src}
							/>
						)}
					</div>
				</DialogContent>
			) : null}
		</Dialog>
	);
}
