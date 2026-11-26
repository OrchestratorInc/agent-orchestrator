import { Info } from "lucide-react";
import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { agentLabel } from "../../lib/agent-options";
import { agentTone } from "../../lib/agent-tone";
import { formatPeriod, formatShare, formatTokens, formatUsd, formatUsdCompact } from "../../lib/usage-format";
import { useNavigateToSession } from "../../lib/navigate-to-session";
import { useUsagePrices } from "../../hooks/useUsageReport";
import {
	partsTotal,
	totalsOf,
	usageByHarness,
	usageByModel,
	usageByPeriod,
	usageByProject,
	usageSeries,
	type SpeedGroup,
	type UsageParts,
	type UsageReport,
	type UsageUnit,
} from "../../lib/usage-report";
import { cn } from "../../lib/utils";
import { AgentAvatar } from "../AgentAvatar";
import { SettingsOptionMenu } from "../settings/SettingsOptionMenu";
import { Button } from "../ui/button";
import { Tooltip, TooltipContent, TooltipTrigger } from "../ui/tooltip";
import { seriesTone, UsageBarChart } from "./UsageChart";
import { UsagePricesDialog } from "./UsagePrices";

type Breakdown = "model" | "day" | "project" | "session";

const PART_LABELS = {
	input: "usage.type.input",
	cacheRead: "usage.type.cacheRead",
	cacheWrite: "usage.type.cacheWrite",
	output: "usage.type.output",
} as const satisfies Record<keyof UsageParts, string>;
const SPEED_LABELS = {
	standard: "usage.speed.standard",
	fast: "usage.speed.fast",
	flex: "usage.speed.flex",
	unknown: "usage.speed.unknown",
} as const satisfies Record<SpeedGroup, string>;
const SESSION_ROWS = 50;
const BREAKDOWN_ROWS = 10;

export function UsageSpend({
	report,
	unit,
	onUnitChange,
	projectNames,
}: {
	report: UsageReport;
	unit: UsageUnit;
	onUnitChange: (unit: UsageUnit) => void;
	projectNames: ReadonlyMap<string, string>;
}) {
	const { t, i18n } = useTranslation();
	const locale = i18n.language;
	const totals = useMemo(() => totalsOf(report.rows), [report.rows]);
	const harnesses = useMemo(() => usageByHarness(report, unit), [report, unit]);
	const series = useMemo(() => usageSeries(report, unit), [report, unit]);
	const processed = partsTotal(totals.tokens);
	const format = (value: number) => (unit === "cost" ? formatUsd(value, locale) : formatTokens(value, locale));
	const total = unit === "cost" ? totals.costNanos : processed;
	const unpriced = totals.events - totals.pricedEvents;
	const hourly = report.resolution === "hour";

	return (
		<div className="flex flex-col gap-8">
			<section className="grid gap-8 lg:grid-cols-[minmax(0,19rem)_minmax(0,1fr)]">
				<div className="flex min-h-0 min-w-0 flex-col gap-5">
					<div className="flex flex-col gap-1">
						<SettingsOptionMenu
							aria-label={t("usage.unit")}
							value={unit}
							options={[{ value: "cost", label: t("usage.view.cost") }, { value: "tokens", label: t("usage.view.tokens") }]}
							onChange={onUnitChange}
							menuAlign="start"
							triggerClassName="h-6! w-fit gap-1! self-start bg-transparent! px-1.5! -ms-1.5 text-xs! text-muted-foreground! hover:text-foreground! data-[state=open]:text-foreground!"
							renderTrigger={() => <span>{t(unit === "cost" ? "usage.total.cost" : "usage.total.tokens")}</span>}
						/>
						<p className="flex items-center gap-1.5 text-heading-lg font-semibold tabular-nums text-foreground">
							{format(total)}
							{unit === "cost" ? (
								<Tooltip>
									<TooltipTrigger asChild>
										<button
											type="button"
											aria-label={t("usage.estimateNote")}
											className="rounded-sm text-muted-foreground hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
										>
											<Info aria-hidden="true" className="size-icon-sm" />
										</button>
									</TooltipTrigger>
									<TooltipContent>{t("usage.estimateNote")}</TooltipContent>
								</Tooltip>
							) : null}
						</p>
						<p className="truncate text-xs text-muted-foreground">
							{t("usage.sessionCount", { count: report.sessions.length })}
							{unit === "cost" && unpriced > 0 ? ` · ${t("usage.unpricedNote", { value: unpriced.toLocaleString(locale) })}` : null}
						</p>
					</div>
					<div className="lg:min-h-0 lg:flex-1 lg:overflow-y-auto lg:pe-2 lg:[contain:size]">
						<ul className="flex flex-col gap-2.5" aria-label={t("usage.byAgent")}>
							{harnesses.map((harness) => {
								const value = unit === "cost" ? harness.costNanos : harness.tokens;
								const share = total > 0 ? value / total : 0;
								const unpricedAgent = unit === "cost" && harness.pricedEvents === 0;
								const detail = unpricedAgent
									? t("usage.harnessUnpricedLine", { tokens: formatTokens(harness.tokens, locale) })
									: unit === "cost"
										? t("usage.harnessCostLine", { share: formatShare(share, locale), tokens: formatTokens(harness.tokens, locale) })
										: t("usage.harnessTokenLine", { share: formatShare(share, locale), cost: formatUsd(harness.costNanos, locale) });
								return (
									<li key={harness.harness} className="flex min-w-0 flex-col gap-1.5" title={detail}>
										<div className="flex min-w-0 items-center gap-2.5">
											<span aria-hidden="true" className="size-2 shrink-0 rounded-full" style={{ backgroundColor: agentTone(harness.harness) }} />
											<AgentAvatar provider={harness.harness} decorative className="size-4 shrink-0" />
											<span className="min-w-0 flex-1 truncate text-control text-foreground">
												{agentLabel(harness.harness)}
												<span className="ms-1.5 text-xs text-muted-foreground">{t("usage.sessionCount", { count: harness.sessions })}</span>
											</span>
											{unpricedAgent ? (
												<span className="shrink-0 text-control text-muted-foreground">{t("usage.notPriced")}</span>
											) : (
												<>
													<span className="shrink-0 text-xs tabular-nums text-muted-foreground">{formatShare(share, locale)}</span>
													<span className="w-16 shrink-0 text-end text-control tabular-nums text-foreground">{format(value)}</span>
												</>
											)}
										</div>
										<ShareBar fraction={share} tone={agentTone(harness.harness)} className="ms-11" />
										<span className="sr-only">{detail}</span>
									</li>
								);
							})}
						</ul>
					</div>
				</div>
				<div className="flex min-w-0 flex-col gap-3">
					<h2 className="text-control font-medium text-foreground">
						{unit === "cost"
							? t(hourly ? "usage.chart.hourlyCost" : "usage.chart.dailyCost")
							: t(hourly ? "usage.chart.hourlyTokens" : "usage.chart.dailyTokens")}
					</h2>
					<UsageBarChart
						series={series}
						label={unit === "cost" ? t(hourly ? "usage.chart.hourlyCost" : "usage.chart.dailyCost") : t(hourly ? "usage.chart.hourlyTokens" : "usage.chart.dailyTokens")}
						formatValue={(value) => (unit === "cost" ? formatUsdCompact(value, locale) : formatTokens(value, locale))}
						formatPeriod={(period) => formatPeriod(period, locale, "short")}
						formatPeriodLong={(period) => formatPeriod(period, locale, "long")}
					/>
				</div>
			</section>

			<section aria-labelledby="usage-totals" className="flex flex-col gap-3">
				<h2 id="usage-totals" className="text-control font-medium text-foreground">{t("usage.totals")}</h2>
				<dl className="grid grid-cols-2 gap-x-6 gap-y-4 sm:grid-cols-3 lg:grid-cols-5">
					<Stat label={t("usage.totals.processed")} value={formatTokens(processed, locale)} />
					<Stat label={t("usage.totals.cachedInput")} value={formatTokens(totals.tokens.cacheRead, locale)} />
					<Stat label={t("usage.totals.uncachedInput")} value={formatTokens(totals.tokens.input + totals.tokens.cacheWrite, locale)} />
					<Stat label={t("usage.totals.output")} value={formatTokens(totals.tokens.output, locale)} />
					<Stat label={t("usage.totals.cacheSavings")} value={formatUsd(totals.cacheSavingsNanos, locale)} />
				</dl>
			</section>

			<section className="grid gap-8 lg:grid-cols-2">
				<PartsBar
					title={unit === "cost" ? t("usage.byType.cost") : t("usage.byType.tokens")}
					items={(Object.keys(PART_LABELS) as (keyof UsageParts)[]).map((key) => ({ key, label: t(PART_LABELS[key]), value: (unit === "cost" ? totals.costs : totals.tokens)[key] }))}
					format={format}
				/>
				<PartsBar
					title={unit === "cost" ? t("usage.bySpeed.cost") : t("usage.bySpeed.tokens")}
					aside={unit === "cost" && totals.premiumNanos > 0 ? t("usage.premium", { value: formatUsd(totals.premiumNanos, locale) }) : undefined}
					items={(Object.keys(SPEED_LABELS) as SpeedGroup[]).map((key) => ({ key, label: t(SPEED_LABELS[key]), value: unit === "cost" ? totals.bySpeed[key].costNanos : totals.bySpeed[key].tokens }))
						.filter((item) => item.value > 0 || item.key === "standard")}
					format={format}
				/>
			</section>

			<UsageBreakdown report={report} unit={unit} projectNames={projectNames} total={total} />
		</div>
	);
}

function ShareBar({ fraction, tone, className }: { fraction: number; tone?: string; className?: string }) {
	return (
		<span aria-hidden="true" className={cn("block h-1 overflow-hidden rounded-full bg-muted", className)}>
			<span className={cn("block h-full rounded-full", !tone && "bg-foreground/45")} style={{ width: `${Math.min(1, Math.max(0, fraction)) * 100}%`, backgroundColor: tone }} />
		</span>
	);
}

function Stat({ label, value }: { label: string; value: string }) {
	return (
		<div className="flex min-w-0 flex-col gap-1">
			<dt className="truncate text-xs text-muted-foreground">{label}</dt>
			<dd className="m-0 text-heading-sm tabular-nums text-foreground">{value}</dd>
		</div>
	);
}

function PartsBar({
	title,
	aside,
	items,
	format,
}: {
	title: string;
	aside?: string;
	items: { key: string; label: string; value: number }[];
	format: (value: number) => string;
}) {
	const sum = items.reduce((total, item) => total + item.value, 0);
	return (
		<div className="flex min-w-0 flex-col gap-3">
			<div className="flex items-baseline justify-between gap-3">
				<h2 className="text-control font-medium text-foreground">{title}</h2>
				{aside ? <span className="text-xs tabular-nums text-muted-foreground">{aside}</span> : null}
			</div>
			<div className="flex h-2 w-full gap-0.5 overflow-hidden rounded-full bg-muted" aria-hidden="true">
				{sum > 0
					? items.map((item, index) => (item.value > 0 ? (
						<div key={item.key} className={cn("h-full first:rounded-s-full last:rounded-e-full", seriesTone(index))} style={{ width: `${(item.value / sum) * 100}%` }} />
					) : null))
					: null}
			</div>
			<ul className="flex flex-wrap gap-x-5 gap-y-1.5 text-xs">
				{items.map((item, index) => (
					<li key={item.key} className="inline-flex items-center gap-1.5 text-muted-foreground">
						<span aria-hidden="true" className={cn("size-2 rounded-xs", seriesTone(index))} />
						{item.label}
						<span className="tabular-nums text-foreground">{format(item.value)}</span>
					</li>
				))}
			</ul>
		</div>
	);
}

function UsageBreakdown({ report, unit, projectNames, total }: { report: UsageReport; unit: UsageUnit; projectNames: ReadonlyMap<string, string>; total: number }) {
	const { t, i18n } = useTranslation();
	const locale = i18n.language;
	const [breakdown, setBreakdownState] = useState<Breakdown>("model");
	const [showAll, setShowAll] = useState(false);
	const setBreakdown = (next: Breakdown) => {
		setBreakdownState(next);
		setShowAll(false);
	};
	const navigateToSession = useNavigateToSession();
	const [pricesFor, setPricesFor] = useState<string | null>(null);
	const customPrices = new Set((useUsagePrices().data?.overrides ?? []).map((override) => override.modelId));
	const seenModels = useMemo(() => [...new Set(report.rows.map((row) => row.model))].sort(), [report.rows]);
	const fraction = (cost: number, tokens: number) => (total > 0 ? (unit === "cost" ? cost : tokens) / total : 0);
	const share = (cost: number, tokens: number, pricedEvents: number, harness?: string) =>
		unit === "cost" && pricedEvents === 0 ? (
			<span className="block text-end">—</span>
		) : (
			<span className="flex items-center justify-end gap-2">
				<ShareBar fraction={fraction(cost, tokens)} tone={harness ? agentTone(harness) : undefined} className="w-14" />
				<span className="w-12 text-end">{formatShare(fraction(cost, tokens), locale)}</span>
			</span>
		);
	const costCell = (costNanos: number, pricedEvents: number) =>
		pricedEvents === 0 ? <span className="text-muted-foreground">{t("usage.notPriced")}</span> : formatUsd(costNanos, locale);
	const projectName = (projectId: string | undefined) => (projectId ? projectNames.get(projectId) ?? projectId : t("usage.noProject"));
	const models = breakdown === "model" ? usageByModel(report.rows, unit) : [];
	const periods = breakdown === "day" ? usageByPeriod(report) : [];
	const projects = breakdown === "project" ? usageByProject(report.sessions, unit) : [];
	const sessions = breakdown === "session"
		? [...report.sessions].sort((a, b) => (unit === "cost" ? b.costNanos - a.costNanos : b.tokens - a.tokens)).slice(0, SESSION_ROWS)
		: [];
	const rowCount = models.length + periods.length + projects.length + sessions.length;
	const limit = <T,>(items: T[]) => (showAll ? items : items.slice(0, BREAKDOWN_ROWS));

	return (
		<section aria-labelledby="usage-breakdown" className="flex flex-col gap-3">
			<div className="flex items-center justify-between gap-3">
				<h2 id="usage-breakdown" className="text-control font-medium text-foreground">{t("usage.breakdown")}</h2>
				<span className="flex-1" />
				<Button type="button" variant="quiet" size="sm" onClick={() => setPricesFor("")}>{t("usage.prices.title")}</Button>
				<SettingsOptionMenu
					aria-label={t("usage.groupBy")}
					value={breakdown}
					options={[
						{ value: "model", label: t("usage.breakdown.model") },
						{ value: "day", label: report.resolution === "hour" ? t("usage.breakdown.hour") : t("usage.breakdown.day") },
						{ value: "project", label: t("usage.breakdown.project") },
						{ value: "session", label: t("usage.breakdown.session") },
					]}
					onChange={setBreakdown}
					menuAlign="end"
					triggerClassName="h-(--size-control-md)! text-xs!"
					renderTrigger={(selected) => (
						<span className="min-w-0 truncate">
							<span className="text-muted-foreground">{t("usage.groupBy")}</span> {selected?.label}
						</span>
					)}
				/>
			</div>
			<table className="w-full table-fixed border-collapse text-control">
				<thead>
					<tr className="text-start text-xs text-muted-foreground">
						<th scope="col" className="w-8 py-2 text-start font-normal" aria-hidden="true" />
						<th scope="col" className="py-2 text-start font-normal">
							{breakdown === "model" ? t("usage.breakdown.model") : breakdown === "day" ? (report.resolution === "hour" ? t("usage.breakdown.hour") : t("usage.breakdown.day")) : breakdown === "project" ? t("usage.breakdown.project") : t("usage.breakdown.session")}
						</th>
						{breakdown === "model" ? <th scope="col" className="w-24 py-2 text-end font-normal">{t("usage.column.cacheHit")}</th> : null}
						{breakdown === "model" ? <th scope="col" className="w-28 py-2 text-end font-normal">{t("usage.column.perMillion")}</th> : null}
						{breakdown === "project" ? <th scope="col" className="w-24 py-2 text-end font-normal">{t("usage.column.sessions")}</th> : null}
						<th scope="col" className="w-28 py-2 text-end font-normal">{t("usage.column.cost")}</th>
						<th scope="col" className="w-36 py-2 ps-6 text-end font-normal">{t("usage.column.share")}</th>
						<th scope="col" className="w-24 py-2 text-end font-normal">{t("usage.column.tokens")}</th>
					</tr>
				</thead>
				<tbody className="tabular-nums">
					{limit(models).map((model, index) => (
						<tr key={`${model.harness}:${model.model}`} className="border-t border-border/60">
							<td className="py-2.5 text-xs text-muted-foreground">{index + 1}</td>
							<td className="min-w-0 py-2.5">
								<span className="flex min-w-0 items-center gap-2">
									<AgentAvatar provider={model.harness} decorative className="size-4 shrink-0" />
									<span className="truncate font-mono text-xs text-foreground">{model.model}</span>
									{customPrices.has(model.model) ? <span className="shrink-0 text-xs text-muted-foreground">{t("usage.prices.custom")}</span> : null}
								</span>
							</td>
							<td className="py-2.5 text-end text-muted-foreground">{formatShare(model.cacheHitRate, locale)}</td>
							<td className="py-2.5 text-end text-muted-foreground">{model.costPerMillionNanos === null ? "—" : formatUsd(model.costPerMillionNanos, locale)}</td>
							<td className="py-2.5 text-end text-foreground">
									{model.pricedEvents === 0 ? (
										<span className="text-muted-foreground">
											<Button type="button" variant="link" title={t("usage.prices.setFor", { model: model.model })} aria-label={t("usage.prices.setFor", { model: model.model })} onClick={() => setPricesFor(model.model)}>
												{t("usage.notPriced")}
											</Button>
										</span>
									) : formatUsd(model.costNanos, locale)}
								</td>
							<td className="py-2.5 ps-6 text-muted-foreground">{share(model.costNanos, model.tokens, model.pricedEvents, model.harness)}</td>
							<td className="py-2.5 text-end text-foreground">{formatTokens(model.tokens, locale)}</td>
						</tr>
					))}
					{limit(periods).map((period, index) => (
						<tr key={period.period} className="border-t border-border/60">
							<td className="py-2.5 text-xs text-muted-foreground">{index + 1}</td>
							<td className="py-2.5 text-foreground">{formatPeriod(period.period, locale, "long")}</td>
							<td className="py-2.5 text-end text-foreground">{costCell(period.costNanos, period.pricedEvents)}</td>
							<td className="py-2.5 ps-6 text-muted-foreground">{share(period.costNanos, period.tokens, period.pricedEvents)}</td>
							<td className="py-2.5 text-end text-foreground">{formatTokens(period.tokens, locale)}</td>
						</tr>
					))}
					{limit(projects).map((project, index) => (
						<tr key={project.projectId} className="border-t border-border/60">
							<td className="py-2.5 text-xs text-muted-foreground">{index + 1}</td>
							<td className="truncate py-2.5 text-foreground">{projectName(project.projectId)}</td>
							<td className="py-2.5 text-end text-muted-foreground">{project.sessions.toLocaleString(locale)}</td>
							<td className="py-2.5 text-end text-foreground">{costCell(project.costNanos, project.pricedEvents)}</td>
							<td className="py-2.5 ps-6 text-muted-foreground">{share(project.costNanos, project.tokens, project.pricedEvents)}</td>
							<td className="py-2.5 text-end text-foreground">{formatTokens(project.tokens, locale)}</td>
						</tr>
					))}
					{limit(sessions).map((session, index) => (
						<tr key={session.sessionId} className="border-t border-border/60">
							<td className="py-2.5 text-xs text-muted-foreground">{index + 1}</td>
							<td className="min-w-0 py-1.5">
								<button
									type="button"
									className="flex w-full min-w-0 items-center gap-2 rounded-md px-1 py-1 text-start hover:bg-foreground/5 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
									onClick={() => navigateToSession(session.projectId, session.sessionId)}
								>
									<AgentAvatar provider={session.harness} decorative className="size-4 shrink-0" />
									<span className="flex min-w-0 flex-col">
										<span className="truncate text-foreground">{session.title}</span>
										<span className="truncate text-xs text-muted-foreground">{projectName(session.projectId)}</span>
									</span>
								</button>
							</td>
							<td className="py-2.5 text-end text-foreground">{costCell(session.costNanos, session.pricedEvents)}</td>
							<td className="py-2.5 ps-6 text-muted-foreground">{share(session.costNanos, session.tokens, session.pricedEvents, session.harness)}</td>
							<td className="py-2.5 text-end text-foreground">{formatTokens(session.tokens, locale)}</td>
						</tr>
					))}
				</tbody>
			</table>
			{rowCount > BREAKDOWN_ROWS ? (
				<button
					type="button"
					className="self-start rounded-md px-1.5 py-0.5 -ms-1.5 text-xs text-muted-foreground hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
					aria-expanded={showAll}
					onClick={() => setShowAll((all) => !all)}
				>
					{t(showAll ? "shell.showLess" : "shell.showMore")}
				</button>
			) : null}
			<UsagePricesDialog open={pricesFor !== null} onOpenChange={(open) => !open && setPricesFor(null)} model={pricesFor || undefined} seenModels={seenModels} />
		</section>
	);
}
