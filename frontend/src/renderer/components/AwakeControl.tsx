import { useId } from "react";
import type { TFunction } from "i18next";
import { Coffee, TriangleAlert } from "@/components/app-icons";
import { useTranslation } from "react-i18next";
import { useStayAwake } from "../hooks/useStayAwake";
import { isMacPlatform } from "../lib/platform";
import { cn } from "../lib/utils";
import { NAV_ROW_HIGHLIGHT_HOST_CLASS, NavRowHighlight } from "./NavRowHighlight";
import { DropdownMenuSwitchItem } from "./ui/dropdown-menu";
import { Pressable } from "./ui/pressable";
import { Tooltip, TooltipContent, TooltipTrigger } from "./ui/tooltip";

type AwakeControlProps = { className?: string; tabIndex?: number };

/** Reflect toggle intent immediately; paused requests remain visible and clearable. */
export function AwakeControl(props: AwakeControlProps) {
	if (!isMacPlatform()) return null;
	return <MacAwakeControl {...props} />;
}

export type StayAwake = ReturnType<typeof useStayAwake>;

/** Why Stay Awake paused itself, or "" when it did not. */
function awakePauseReason(t: TFunction, stay: StayAwake): string {
	return stay.reason === "low_battery"
		? t("awake.pausedBattery", "Paused: battery below {{percent}}%.\nResumes after charging.", { percent: stay.stopBatteryPercent })
		: stay.reason === "critical_heat"
			? t("awake.pausedHeat", "Paused while your Mac cools. Toggle to retry.")
			: stay.reason === "battery_unknown"
				? t("awake.pausedUnknownBattery", "Battery status unavailable. Toggle to retry.")
				: "";
}

function MacAwakeControl({ className, tabIndex }: AwakeControlProps) {
	const { t } = useTranslation();
	const stay = useStayAwake(true);
	const title = t("awake.title", "Stay Awake");
	const descriptionId = useId();
	const highlighted = stay.starting || (stay.active && !stay.stopping);
	const reason = awakePauseReason(t, stay);
	const description = stay.error
		? t("awake.toggleFailed", "Couldn’t update Stay Awake. Try again.")
		: highlighted
			? t("awake.activeTooltip", "Stay Awake is on. Click to turn off.")
			: stay.on
				? reason || t("awake.pausedTooltip", "Stay Awake is paused. Click to turn off.")
				: t("awake.offTooltip", "Stay Awake is off. Click to turn on.");
	return (
		<>
			<Tooltip>
				<TooltipTrigger asChild>
					<Pressable
						tabIndex={tabIndex}
						aria-label={title}
						aria-describedby={descriptionId}
						aria-pressed={stay.on}
						disabled={!stay.supported}
						data-testid="awake-button"
						onClick={stay.toggle}
						className={cn(
							NAV_ROW_HIGHLIGHT_HOST_CLASS,
							"inline-flex h-9 w-9 shrink-0 cursor-pointer items-center justify-center rounded-lg disabled:opacity-50",
							// The shared navigation highlight owns both the selected fill and hover pill.
							highlighted ? "text-foreground" : "text-muted-foreground",
							className,
						)}
					>
						<NavRowHighlight active={highlighted} disabled={!stay.supported} />
						<span className="relative z-[1] grid place-items-center">
							<Coffee className="size-icon-md" aria-hidden="true" />
							{stay.attention && <TriangleAlert data-testid="awake-inactive-indicator" className="absolute -right-1 -top-1 size-2.5 fill-background text-error" strokeWidth={2.5} aria-hidden="true" />}
						</span>
					</Pressable>
				</TooltipTrigger>
				<TooltipContent className="max-w-72 whitespace-pre-line">{description}</TooltipContent>
			</Tooltip>
			<span id={descriptionId} className="sr-only">{description}</span>
			{stay.error ? (
				<span className="sr-only" role="alert">
					{stay.error}
				</span>
			) : null}
		</>
	);
}

/**
 * Stay Awake for the sidebar footer's Mobile menu. The always-mounted trigger
 * holds it and hands it to {@link AwakeMenuItem}, so a failed toggle is still
 * badged and explained after the menu closes. Disabled off macOS.
 */
export function useFooterStayAwake(): StayAwake {
	return useStayAwake(isMacPlatform());
}

/** True when Stay Awake needs a look: it paused, or the last toggle failed. */
export function awakeNeedsAttention(stay: StayAwake): boolean {
	return isMacPlatform() && (stay.attention || !!stay.error);
}

/**
 * Stay Awake as a switch row in a dropdown (the sidebar footer's Mobile menu).
 * A second line appears only when it paused or failed, saying why. Renders
 * nothing off macOS.
 */
export function AwakeMenuItem({ stay }: { stay: StayAwake }) {
	if (!isMacPlatform()) return null;
	return <MacAwakeMenuItem stay={stay} />;
}

function MacAwakeMenuItem({ stay }: { stay: StayAwake }) {
	const { t } = useTranslation();
	const highlighted = stay.starting || (stay.active && !stay.stopping);
	const problem = stay.error
		? t("awake.toggleFailed", "Couldn’t update Stay Awake. Try again.")
		: !highlighted && stay.on
			? awakePauseReason(t, stay) || t("awake.pausedTooltip", "Stay Awake is paused. Click to turn off.")
			: "";
	return (
		<DropdownMenuSwitchItem
			checked={stay.on}
			className={problem ? "items-start" : undefined}
			data-testid="awake-menu-item"
			disabled={!stay.supported}
			onSelect={stay.toggle}
		>
			<span className={cn("relative grid shrink-0 place-items-center", problem && "mt-px")}>
				<Coffee aria-hidden="true" />
				{awakeNeedsAttention(stay) ? <TriangleAlert className="absolute -right-1 -top-1 size-2.5! fill-background text-error" strokeWidth={2.5} aria-hidden="true" /> : null}
			</span>
			<span className="flex min-w-0 flex-1 flex-col gap-0.5">
				<span>{t("awake.title", "Stay Awake")}</span>
				{problem ? (
					<span className="whitespace-pre-line text-[length:var(--font-size-xs)] leading-snug text-error">{problem}</span>
				) : null}
			</span>
		</DropdownMenuSwitchItem>
	);
}
