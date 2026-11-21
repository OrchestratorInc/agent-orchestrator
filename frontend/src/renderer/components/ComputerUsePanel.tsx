import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { InspectorSection, NAV_ROW_HIGHLIGHT_HOST_CLASS, NavRowHighlight } from "@aoagents/product-ui";
import { MousePointerClick } from "@/components/app-icons";
import { computerUseScreenshotUrl, type ComputerUseTimelineEntry } from "../hooks/useComputerUseTimeline";
import { mediaCrossOrigin } from "../lib/file-media";
import { formatTimeCompact } from "../lib/format-time";
import { computerUseStep } from "../lib/mcp-tool";
import { cn } from "../lib/utils";
import { ChatImageDialog } from "./chat/ChatImage";
import { Pressable } from "./ui/pressable";

type Screenshot = { url: string; label: string };

/**
 * The inspector's Computer Use tab: the latest screenshot an agent's
 * computer-use call saved, where the calls run, and every step taken, newest
 * first. A screenshot opens at full size.
 */
export function ComputerUsePanel({ entries, sessionId }: { entries: ComputerUseTimelineEntry[]; sessionId: string }) {
	const { t } = useTranslation();
	const now = useMinuteClock();
	const [opened, setOpened] = useState<Screenshot | null>(null);
	const steps = entries
		.map((entry) => {
			const step = computerUseStep(entry.tool, entry.arguments, !entry.ok, entry.error);
			const name = entry.screenshots?.at(-1);
			return { ...entry, ...step, screenshot: name ? computerUseScreenshotUrl(sessionId, name) : undefined };
		})
		.reverse();
	const latest = steps.find((step) => step.screenshot);

	return (
		<div className="scrollbar-none flex h-full min-h-0 flex-col overflow-x-hidden overflow-y-auto pb-3" data-testid="computer-use-panel">
			<div className="px-3 pt-3">
				{latest?.screenshot ? (
					<Pressable
						aria-label={t("computerUse.openScreenshot", { step: latest.label })}
						className="block w-full cursor-zoom-in overflow-hidden rounded-md border border-border bg-settings-row transition-opacity duration-150 hover:opacity-90 focus-visible:ring-2 focus-visible:ring-ring motion-reduce:transition-none"
						onClick={() => setOpened({ url: latest.screenshot!, label: latest.label })}
					>
						<img
							alt={latest.label}
							className="block max-h-[50vh] w-full object-contain"
							crossOrigin={mediaCrossOrigin(latest.screenshot)}
							src={latest.screenshot}
						/>
					</Pressable>
				) : (
					<div className="grid aspect-video place-items-center rounded-md bg-settings-row text-xs text-passive">
						{t("computerUse.noScreenshot")}
					</div>
				)}
				{latest ? (
					<p className="mt-1.5 flex min-w-0 items-baseline justify-between gap-2 text-xs text-muted-foreground">
						<span className="min-w-0 truncate">{latest.label}</span>
						<time className="shrink-0 tabular-nums text-passive" dateTime={latest.at}>{formatTimeCompact(latest.at, { now })}</time>
					</p>
				) : null}
			</div>

			<div className="mx-3 mt-3 mb-2 flex min-h-8 items-center justify-between gap-2 text-sm">
				<span className="text-muted-foreground">{t("computerUse.runsOn")}</span>
				<span className="text-foreground">{entries.at(-1)?.ranOn || t("computerUse.thisMachine")}</span>
			</div>

			<InspectorSection surface={false} title={t("computerUse.timeline")}>
				<ol className="-mx-1.5 flex flex-col" aria-label={t("computerUse.timeline")}>
					{steps.map((step) => {
						const content = (
							<>
								<span className="relative z-[1] grid h-10 w-16 shrink-0 place-items-center overflow-hidden rounded-sm border border-border bg-settings-row">
									{step.screenshot ? (
										<img alt="" className="size-full object-cover object-top" crossOrigin={mediaCrossOrigin(step.screenshot)} loading="lazy" src={step.screenshot} />
									) : (
										<MousePointerClick aria-hidden="true" className="size-icon-sm text-passive" />
									)}
								</span>
								<span className="relative z-[1] min-w-0 flex-1">
									<span className="block truncate text-sm text-foreground">{step.label}</span>
									{step.error ? <span className="block truncate text-xs text-destructive" title={step.error}>{step.error}</span> : null}
								</span>
								<time className="relative z-[1] shrink-0 text-xs tabular-nums text-passive" dateTime={step.at}>{formatTimeCompact(step.at, { now })}</time>
							</>
						);
						const rowClass = "relative flex w-full min-w-0 items-center gap-2.5 rounded-md p-1.5";
						return (
							<li key={step.id}>
								{step.screenshot ? (
									<Pressable
										aria-label={t("computerUse.openScreenshot", { step: step.label })}
										className={cn(NAV_ROW_HIGHLIGHT_HOST_CLASS, rowClass, "focus-visible:ring-2 focus-visible:ring-ring/60")}
										onClick={() => setOpened({ url: step.screenshot!, label: step.label })}
									>
										<NavRowHighlight />
										{content}
									</Pressable>
								) : (
									<div className={rowClass}>{content}</div>
								)}
							</li>
						);
					})}
				</ol>
			</InspectorSection>

			{opened ? (
				<ChatImageDialog label={opened.label} onOpenChange={(open) => !open && setOpened(null)} open url={opened.url} />
			) : null}
		</div>
	);
}

/** The current time, refreshed each minute so "2m ago" stays true. */
function useMinuteClock(): number {
	const [now, setNow] = useState(() => Date.now());
	useEffect(() => {
		const timer = window.setInterval(() => setNow(Date.now()), 60_000);
		return () => window.clearInterval(timer);
	}, []);
	return now;
}
