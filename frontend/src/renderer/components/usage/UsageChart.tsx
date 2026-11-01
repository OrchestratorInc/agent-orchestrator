import { useTranslation } from "react-i18next";
import { agentLabel } from "../../lib/agent-options";
import { agentTone } from "../../lib/agent-tone";
import { niceScale, type UsageSeries } from "../../lib/usage-report";
import { Tooltip, TooltipContent, TooltipTrigger } from "../ui/tooltip";

/** Neutral series tones, strongest first: colour stays rare and semantic. */
export const SERIES_TONES = ["bg-foreground/85", "bg-foreground/45", "bg-foreground/25", "bg-foreground/15"] as const;

export function seriesTone(index: number): string {
	return SERIES_TONES[Math.min(index, SERIES_TONES.length - 1)];
}

const CHART_HEIGHT = 200;
const MAX_AXIS_LABELS = 7;

/**
 * Stacked bars, one per period and one segment per agent. Bars rather than a
 * smoothed curve: the data is per day or hour, so a curve would invent values
 * between the buckets.
 */
export function UsageBarChart({
	series,
	formatValue,
	formatPeriod,
	formatPeriodLong,
	label,
}: {
	series: UsageSeries;
	formatValue: (value: number) => string;
	formatPeriod: (period: string) => string;
	formatPeriodLong: (period: string) => string;
	label: string;
}) {
	const { t } = useTranslation();
	const totals = series.values.map((values) => values.reduce((sum, value) => sum + value, 0));
	const { max, ticks } = niceScale(Math.max(0, ...totals));
	const labelEvery = Math.max(1, Math.ceil(series.periods.length / MAX_AXIS_LABELS));
	return (
		<figure aria-label={label} className="m-0 flex flex-col gap-2">
			<div className="flex gap-3">
				<div className="relative w-14 shrink-0 text-end text-2xs tabular-nums text-muted-foreground" style={{ height: CHART_HEIGHT }} aria-hidden="true">
					{ticks.map((tick) => (
						<span key={tick} className="absolute end-0" style={{ bottom: `${(tick / max) * 100}%`, transform: "translateY(50%)" }}>
							{formatValue(tick)}
						</span>
					))}
				</div>
				<div className="relative min-w-0 flex-1" style={{ height: CHART_HEIGHT }}>
					{ticks.map((tick) => (
						<div key={tick} aria-hidden="true" className="absolute inset-x-0 border-t border-border/60" style={{ bottom: `${(tick / max) * 100}%` }} />
					))}
					<div className="absolute inset-0 flex items-end gap-px">
						{series.periods.map((period, periodIndex) => {
							const total = totals[periodIndex];
							return (
								<Tooltip key={period}>
									<TooltipTrigger asChild>
										<div
											tabIndex={0}
											aria-label={t("usage.chart.barLabel", { period: formatPeriodLong(period), value: formatValue(total) })}
											className="group flex h-full min-w-0 flex-1 flex-col justify-end rounded-sm outline-none hover:bg-foreground/5 focus-visible:bg-foreground/5"
										>
											<div className="flex flex-col-reverse overflow-hidden rounded-t-sm" style={{ height: `${(total / max) * 100}%` }}>
												{series.values[periodIndex].map((value, harnessIndex) => (
													value > 0 ? (
														<div
															key={series.harnesses[harnessIndex]}
															style={{ height: `${(value / total) * 100}%`, backgroundColor: agentTone(series.harnesses[harnessIndex]) }}
														/>
													) : null
												))}
											</div>
										</div>
									</TooltipTrigger>
									<TooltipContent side="top" className="flex flex-col gap-1 tabular-nums">
										<span className="font-medium">{formatPeriodLong(period)}</span>
										{series.harnesses.map((harness, harnessIndex) => (
											<span key={harness} className="flex items-center justify-between gap-4">
												<span className="inline-flex items-center gap-1.5">
													<span aria-hidden="true" className="size-2 rounded-full" style={{ backgroundColor: agentTone(harness) }} />
													{agentLabel(harness)}
												</span>
												<span>{formatValue(series.values[periodIndex][harnessIndex])}</span>
											</span>
										))}
										{series.harnesses.length > 1 ? (
											<span className="flex justify-between gap-4 border-t border-border/60 pt-1">
												<span>{t("usage.chart.total")}</span>
												<span>{formatValue(total)}</span>
											</span>
										) : null}
									</TooltipContent>
								</Tooltip>
							);
						})}
					</div>
				</div>
			</div>
			<div className="flex gap-3" aria-hidden="true">
				<div className="w-14 shrink-0" />
				<div className="flex min-w-0 flex-1 gap-px text-2xs tabular-nums text-muted-foreground">
					{series.periods.map((period, index) => (
						<span key={period} className="min-w-0 flex-1 overflow-visible whitespace-nowrap text-center">
							{index % labelEvery === 0 || index === series.periods.length - 1 ? formatPeriod(period) : ""}
						</span>
					))}
				</div>
			</div>
			{series.harnesses.length > 1 ? (
				<ul className="flex flex-wrap gap-x-4 gap-y-1 ps-[4.25rem] text-xs text-muted-foreground">
					{series.harnesses.map((harness) => (
						<li key={harness} className="inline-flex items-center gap-1.5">
							<span aria-hidden="true" className="size-2 rounded-xs" style={{ backgroundColor: agentTone(harness) }} />
							{agentLabel(harness)}
						</li>
					))}
				</ul>
			) : null}
		</figure>
	);
}
