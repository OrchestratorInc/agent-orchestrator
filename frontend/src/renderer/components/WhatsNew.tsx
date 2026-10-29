import { RefreshCw, Sparkles, X } from "lucide-react";
import { useEffect } from "react";
import { useTranslation } from "react-i18next";
import type { WhatsNewEntry } from "../../shared/whats-new";
import { aoBridge } from "../lib/bridge";
import { cn } from "../lib/utils";
import {
	reportWhatsNewClicked,
	reportWhatsNewDismissed,
	reportWhatsNewShown,
	setWhatsNewPanelOpen,
	useWhatsNew,
	useWhatsNewPanelOpen,
} from "../lib/whats-new";
import { useUiStore } from "../stores/ui-store";
import { Button } from "./ui/button";
import {
	Dialog,
	DialogContent,
	DialogDescription,
	DialogHeader,
	DialogTitle,
	settingsDialogBodyClass,
	settingsDialogContentClass,
	settingsDialogHeaderClass,
} from "./ui/dialog";

function WhatsNewMedia({ item }: { item: WhatsNewEntry }) {
	if (item.videoUrl) {
		return (
			<video
				aria-hidden="true"
				autoPlay
				className="aspect-video w-full rounded-md bg-background object-cover"
				loop
				muted
				playsInline
				src={item.videoUrl}
			/>
		);
	}
	if (item.imageUrl) {
		return <img alt="" className="aspect-video w-full rounded-md bg-background object-cover" loading="lazy" src={item.imageUrl} />;
	}
	return null;
}

/** A date-only publishedAt is a calendar day, not UTC midnight, so it must not shift a day in western time zones. */
export function formatPublishedAt(value: string): string {
	const dateOnly = /^\d{4}-\d{2}-\d{2}$/.test(value);
	return new Intl.DateTimeFormat(undefined, { month: "short", day: "numeric", timeZone: dateOnly ? "UTC" : undefined }).format(new Date(value));
}

/** Runs an item's call to action: open its link, or take the user to update settings for a nudge. */
function useRunCta(onDone: (id: string) => void) {
	const openGlobalSettings = useUiStore((state) => state.openGlobalSettings);
	return (item: WhatsNewEntry) => {
		if (!item.cta) return;
		reportWhatsNewClicked(item.id);
		if ("action" in item.cta) openGlobalSettings("updates");
		else void aoBridge.app.openExternal(item.cta.url);
		onDone(item.id);
	};
}

/**
 * Sidebar card for the newest unseen What's new item. Dismissing it marks every
 * unseen item seen (one dismissal, not a queue to click through); the full
 * list stays available from "See all" and Settings > Updates.
 */
export function WhatsNewCard({ tabIndex }: { tabIndex: number }) {
	const { t } = useTranslation();
	const { unseen, markSeen } = useWhatsNew();
	const item = unseen[0];
	const runCta = useRunCta((id) => markSeen([id]));
	useEffect(() => {
		if (item) reportWhatsNewShown(item.id);
	}, [item]);
	if (!item) return null;

	const Icon = item.kind === "nudge" ? RefreshCw : Sparkles;
	const dismiss = () => {
		reportWhatsNewDismissed(item.id);
		markSeen(unseen.map((entry) => entry.id));
	};
	return (
		<section
			aria-label={t("whatsNew.title")}
			className="mb-1 flex w-full flex-col gap-2 rounded-lg bg-muted p-2.5 text-left"
			data-testid="whats-new-card"
		>
			<div className="flex items-start gap-2">
				<Icon aria-hidden="true" className="mt-0.5 size-icon-sm shrink-0 text-muted-foreground" />
				<p className="min-w-0 flex-1 text-sm font-medium leading-5 text-foreground">{item.title}</p>
				<button
					aria-label={t("whatsNew.dismiss")}
					className="-mr-1 -mt-0.5 grid size-6 shrink-0 place-items-center rounded-md text-muted-foreground hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60"
					onClick={dismiss}
					tabIndex={tabIndex}
					type="button"
				>
					<X aria-hidden="true" className="size-icon-sm" />
				</button>
			</div>
			<WhatsNewMedia item={item} />
			<p className="line-clamp-3 text-caption leading-4 text-muted-foreground">{item.body}</p>
			<div className="flex items-center gap-1">
				{item.cta ? (
					<Button onClick={() => runCta(item)} size="sm" tabIndex={tabIndex} variant="secondary">
						{item.cta.label}
					</Button>
				) : null}
				{unseen.length > 1 ? (
					<Button
						className="text-muted-foreground"
						onClick={() => setWhatsNewPanelOpen(true)}
						size="sm"
						tabIndex={tabIndex}
						variant="ghost"
					>
						{t("whatsNew.seeAll")}
					</Button>
				) : null}
			</div>
		</section>
	);
}

/** Every live item, newest first. Opening it counts as seeing them all. */
export function WhatsNewDialog() {
	const { t } = useTranslation();
	const open = useWhatsNewPanelOpen();
	const { items, unseen, markSeen } = useWhatsNew();
	const runCta = useRunCta(() => setWhatsNewPanelOpen(false));
	useEffect(() => {
		if (!open) return;
		for (const item of items) reportWhatsNewShown(item.id);
		markSeen(unseen.map((item) => item.id));
	}, [items, markSeen, open, unseen]);

	return (
		<Dialog onOpenChange={setWhatsNewPanelOpen} open={open}>
			<DialogContent className={settingsDialogContentClass}>
				<DialogHeader className={settingsDialogHeaderClass}>
					<DialogTitle>{t("whatsNew.title")}</DialogTitle>
					<DialogDescription className="sr-only">{t("whatsNew.description")}</DialogDescription>
				</DialogHeader>
				<div className={settingsDialogBodyClass} data-testid="whats-new-panel">
					{items.length === 0 ? (
						<p className="text-sm text-muted-foreground">{t("whatsNew.empty")}</p>
					) : (
						items.map((item, index) => (
							<article className={cn("flex flex-col gap-2", index > 0 && "border-t border-border pt-4")} key={item.id}>
								<div className="flex items-baseline justify-between gap-3">
									<h3 className="text-sm font-medium text-foreground">{item.title}</h3>
									<time className="shrink-0 text-caption tabular-nums text-muted-foreground" dateTime={item.publishedAt}>
										{formatPublishedAt(item.publishedAt)}
									</time>
								</div>
								<WhatsNewMedia item={item} />
								<p className="text-sm leading-5 text-muted-foreground">{item.body}</p>
								{item.cta ? (
									<div>
										<Button onClick={() => runCta(item)} size="sm" variant="secondary">
											{item.cta.label}
										</Button>
									</div>
								) : null}
							</article>
						))
					)}
				</div>
			</DialogContent>
		</Dialog>
	);
}
