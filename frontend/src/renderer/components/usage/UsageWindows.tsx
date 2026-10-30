import type { TFunction } from "i18next";
import { TriangleAlert } from "lucide-react";
import { useTranslation } from "react-i18next";
import { agentAccountProviderNames } from "../../hooks/useAgentAccounts";
import { agentTone } from "../../lib/agent-tone";
import { DAY_MINUTES, formatCountdown, spanPace, type UsageLimitLogin, type UsageLimitSpan, type UsageLimitWindows } from "../../lib/usage-limits";
import { cn } from "../../lib/utils";
import { AgentAvatar } from "../AgentAvatar";
import { formatPlanName } from "../settings/CodexAccountDetails";
import { Tooltip, TooltipContent, TooltipTrigger } from "../ui/tooltip";

const PROVIDER_HARNESS = { codex: "codex", claude: "claude-code" } as const;
const HOUR_MS = 3_600_000;
/** A weekly window not read for this long is shown as stale. */
const STALE_MS = HOUR_MS;

type Sessions = { login: UsageLimitLogin; spans: UsageLimitSpan[] };

/**
 * The experimental Windows view: each login's five-hour windows and weekly
 * allowance, drawn only from the limit readings AO took. Nothing is inferred.
 */
export function UsageWindows({ history, now }: { history: UsageLimitWindows; now: Date }) {
	const { t, i18n } = useTranslation();
	const locale = i18n.language;
	const since = Date.parse(history.since);
	const until = Date.parse(history.until);
	const sessions: Sessions[] = history.logins
		.map((login) => ({ login, spans: login.windows.filter((span) => (span.durationMinutes ?? 0) > 0 && (span.durationMinutes ?? 0) < DAY_MINUTES) }))
		.filter((entry) => entry.spans.length > 0);
	// A weekly limit with nothing used says nothing; only ones in use are drawn.
	const weekly = history.logins.flatMap((login) =>
		login.windows
			.filter((span) => (span.durationMinutes ?? 0) >= DAY_MINUTES && span.state === "open" && span.peakUsedPercent > 0)
			.map((span) => ({ login, span })),
	);
	const firstReading = history.firstReadingAt ? Date.parse(history.firstReadingAt) : null;
	// The timeline starts on the day readings start, not on empty days before it.
	const firstDay = firstReading === null ? since : new Date(firstReading).setHours(0, 0, 0, 0);
	const timelineStart = Math.max(since, firstDay);
	const dateTime = new Intl.DateTimeFormat(locale, { weekday: "short", day: "numeric", month: "short", hour: "2-digit", minute: "2-digit" });

	if (sessions.length === 0 && weekly.length === 0) {
		return (
			<div className="flex flex-col items-center gap-1 py-16 text-center">
				<p className="text-control font-medium text-foreground">{t("usage.windows.empty")}</p>
				<p className="max-w-sm text-xs text-muted-foreground">{t("usage.windows.emptyBody")}</p>
			</div>
		);
	}

	return (
		<div className="flex flex-col gap-10">
			<p className="text-xs text-muted-foreground">
				{t("usage.windows.about")}
				{/* Say when recording began only when it began inside the period. */}
				{firstReading !== null && firstReading - since > HOUR_MS ? ` ${t("usage.windows.recordingSince", { value: dateTime.format(firstReading) })}` : null}
			</p>
			{sessions.length > 0 ? <SessionTimeline sessions={sessions} since={timelineStart} until={until} now={now} dateTime={dateTime} /> : null}
			{sessions.length > 0 ? <Fullness sessions={sessions} now={now} dateTime={dateTime} /> : null}
			{weekly.length > 0 ? (
				<section aria-labelledby="usage-windows-weekly" className="flex flex-col gap-4">
					<SectionHeading id="usage-windows-weekly" title={t("usage.windows.allowance")} help={t("usage.windows.allowanceHelp")} />
					<div className="grid gap-8 lg:grid-cols-2">
						{weekly.map(({ login, span }) => <WeeklyAllowance key={`${login.provider}:${login.email}:${span.windowId}`} login={login} span={span} now={now} dateTime={dateTime} />)}
					</div>
				</section>
			) : null}
		</div>
	);
}

function LoginName({ login }: { login: UsageLimitLogin }) {
	const { t } = useTranslation();
	const plan = formatPlanName(login.plan);
	return (
		<span className="flex min-w-0 items-center gap-2">
			<AgentAvatar provider={PROVIDER_HARNESS[login.provider]} decorative className="size-4 shrink-0" />
			<span className="flex min-w-0 flex-col">
				<span className="truncate text-xs text-foreground">{login.email || agentAccountProviderNames[login.provider]}</span>
				{plan ? <span className="truncate text-2xs text-muted-foreground">{t("settings.codexAccounts.planLabel", { name: plan })}</span> : null}
			</span>
		</span>
	);
}

/** Fill colour of a window: the agent's, or the warning tone once it hit the limit. */
function spanTone(login: UsageLimitLogin, span: UsageLimitSpan): string {
	return span.peakUsedPercent >= 100 ? "var(--color-warning)" : agentTone(PROVIDER_HARNESS[login.provider]);
}

function spanSummary(span: UsageLimitSpan, dateTime: Intl.DateTimeFormat, t: TFunction): string {
	const start = span.startsAt ? Date.parse(span.startsAt) : Date.parse(span.firstReadAt);
	return t("usage.windows.blockLabel", {
		range: dateTime.formatRange(start, Date.parse(span.resetsAt)),
		value: `${Math.round(span.peakUsedPercent)}%`,
		state: t(`usage.windows.state.${span.state}`),
	});
}

/** A section title with the one line that says how to read it. */
function SectionHeading({ id, title, help, aside }: { id: string; title: string; help: string; aside?: string }) {
	return (
		<div className="flex flex-col gap-1">
			<div className="flex items-baseline justify-between gap-3">
				<h2 id={id} className="text-control font-medium text-foreground">{title}</h2>
				{aside ? <span className="text-xs tabular-nums text-muted-foreground">{aside}</span> : null}
			</div>
			<p className="max-w-3xl text-xs text-muted-foreground">{help}</p>
		</div>
	);
}

/** What a window's tooltip says: when, how full, and how sure that number is. */
function SpanDetails({ span, now, dateTime }: { span: UsageLimitSpan; now: Date; dateTime: Intl.DateTimeFormat }) {
	const { t } = useTranslation();
	const start = span.startsAt ? Date.parse(span.startsAt) : Date.parse(span.firstReadAt);
	const lastAt = Date.parse(span.lastReadAt);
	const before = formatCountdown(span.resetsAt, new Date(lastAt)) ?? "0m";
	const pace = span.state === "open" ? spanPace(span) : null;
	return (
		<span className="flex max-w-xs flex-col gap-0.5 tabular-nums">
			<span className="font-medium">{dateTime.formatRange(start, Date.parse(span.resetsAt))}</span>
			<span>{span.peakUsedPercent >= 100 ? t("usage.windows.detail.hit") : t("usage.windows.detail.used", { value: `${Math.round(span.peakUsedPercent)}%` })}</span>
			<span className="opacity-80">
				{span.state === "open"
					? t("usage.windows.detail.open", { value: formatCountdown(span.resetsAt, now) ?? "0m" })
					: span.state === "final"
						? t("usage.windows.detail.final", { value: before })
						: t("usage.windows.detail.partial", { time: dateTime.format(lastAt), value: before })}
			</span>
			{pace ? (
				<span className="opacity-80">
					{pace.runsOutAt
						? t("usage.windows.projectedRunsOut", { value: dateTime.format(pace.runsOutAt) })
						: t("usage.windows.projected", { value: `${Math.round(pace.atReset)}%` })}
				</span>
			) : null}
		</span>
	);
}

/** A legend key drawn like the window it names. */
function LegendBlock({ state }: { state: UsageLimitSpan["state"] }) {
	return (
		<span aria-hidden="true" className={cn("relative h-3 w-4 overflow-hidden rounded-xs bg-foreground/15", state === "partial" && "border border-dashed border-foreground/60")}>
			<span className={cn("absolute inset-x-0 bottom-0 h-1/2", state === "open" ? "bg-foreground/30" : "bg-foreground/60")} />
		</span>
	);
}

function SessionTimeline({ sessions, since, until, now, dateTime }: { sessions: Sessions[]; since: number; until: number; now: Date; dateTime: Intl.DateTimeFormat }) {
	const { t, i18n } = useTranslation();
	const span = until - since;
	const at = (ms: number) => `${Math.min(100, Math.max(0, ((ms - since) / span) * 100))}%`;
	const midnights: number[] = [];
	const day = new Date(since);
	day.setHours(24, 0, 0, 0);
	for (; day.getTime() < until; day.setDate(day.getDate() + 1)) midnights.push(day.getTime());
	// A timeline that starts at midnight labels its first day too.
	const labels = new Date(since).getHours() === 0 && new Date(since).getMinutes() === 0 ? [since, ...midnights] : midnights;
	const dayLabel = new Intl.DateTimeFormat(i18n.language, { weekday: "short", day: "numeric" });
	return (
		<section aria-labelledby="usage-windows-timeline" className="flex flex-col gap-3">
			<SectionHeading id="usage-windows-timeline" title={t("usage.windows.timeline")} help={t("usage.windows.timelineHelp")} />
			<div className="grid grid-cols-[minmax(0,13rem)_minmax(0,1fr)] gap-x-4 gap-y-2">
				{sessions.map(({ login, spans }) => (
					<div key={`${login.provider}:${login.email}:${login.accountId}`} className="contents">
						<LoginName login={login} />
						<div className="relative h-11 overflow-hidden rounded-md bg-muted/60">
							{midnights.map((ms) => <div key={ms} aria-hidden="true" className="absolute inset-y-0 border-s border-border/70" style={{ insetInlineStart: at(ms) }} />)}
							{spans.map((window) => {
								const start = window.startsAt ? Date.parse(window.startsAt) : Date.parse(window.firstReadAt);
								const end = Date.parse(window.resetsAt);
								const tone = spanTone(login, window);
								const label = spanSummary(window, dateTime, t);
								const used = Math.min(100, window.peakUsedPercent);
								const pace = window.state === "open" ? spanPace(window) : null;
								return (
									<Tooltip key={window.resetsAt}>
										<TooltipTrigger asChild>
											<div
												tabIndex={0}
												aria-label={label}
												className={cn(
													"absolute inset-y-1 overflow-hidden rounded-sm outline-none focus-visible:ring-2 focus-visible:ring-ring",
													window.state === "partial" && "border border-dashed",
												)}
												style={{
													insetInlineStart: at(start),
													width: `calc(${at(Math.min(end, until))} - ${at(start)})`,
													backgroundColor: `color-mix(in oklab, ${tone} 18%, transparent)`,
													borderColor: tone,
												}}
											>
												<div
													className={cn("absolute inset-x-0 bottom-0", window.state === "open" && "opacity-60")}
													style={{ height: `${used}%`, backgroundColor: tone }}
												/>
												{/* The projection: where the open window lands at its reset if its pace holds. */}
												{pace && pace.atReset > used ? (
													<div
														aria-hidden="true"
														className="absolute inset-x-0 border-t border-dashed"
														style={{ bottom: `${used}%`, height: `${pace.atReset - used}%`, borderColor: tone, backgroundColor: `color-mix(in oklab, ${tone} 30%, transparent)` }}
													/>
												) : null}
												<span aria-hidden="true" className="absolute inset-x-0 top-0.5 truncate text-center text-2xs font-medium tabular-nums text-foreground">
													{Math.round(window.peakUsedPercent)}%{pace && pace.atReset > used ? ` → ${Math.round(pace.atReset)}%` : ""}
												</span>
											</div>
										</TooltipTrigger>
										<TooltipContent><SpanDetails span={window} now={now} dateTime={dateTime} /></TooltipContent>
									</Tooltip>
								);
							})}
						</div>
					</div>
				))}
				<div />
				<div aria-hidden="true" className="relative h-4 text-2xs tabular-nums text-muted-foreground">
					{labels.map((ms) => (
						<span key={ms} className="absolute whitespace-nowrap ps-1" style={{ insetInlineStart: at(ms) }}>{dayLabel.format(ms)}</span>
					))}
				</div>
			</div>
			<ul className="flex flex-wrap gap-x-5 gap-y-1 text-xs text-muted-foreground">
				{(["final", "partial", "open"] as const).map((state) => (
					<li key={state} className="inline-flex items-center gap-1.5"><LegendBlock state={state} />{t(`usage.windows.state.${state}`)}</li>
				))}
			</ul>
		</section>
	);
}

function Fullness({ sessions, now, dateTime }: { sessions: Sessions[]; now: Date; dateTime: Intl.DateTimeFormat }) {
	const { t } = useTranslation();
	const closed = sessions.map(({ login, spans }) => ({ login, spans: spans.filter((span) => span.state !== "open") })).filter((entry) => entry.spans.length > 0);
	if (closed.length === 0) return null;
	const total = closed.reduce((sum, entry) => sum + entry.spans.length, 0);
	const hit = closed.reduce((sum, entry) => sum + entry.spans.filter((span) => span.peakUsedPercent >= 100).length, 0);
	return (
		<section aria-labelledby="usage-windows-fullness" className="flex flex-col gap-3">
			<SectionHeading
				id="usage-windows-fullness"
				title={t("usage.windows.fullness")}
				help={t("usage.windows.fullnessHelp")}
				aside={t("usage.windows.hitLimit", { hit, total })}
			/>
			<div className="grid grid-cols-[minmax(0,13rem)_minmax(0,1fr)] items-end gap-x-4 gap-y-4">
				{closed.map(({ login, spans }) => (
					<div key={`${login.provider}:${login.email}:${login.accountId}`} className="contents">
						<span className="self-center"><LoginName login={login} /></span>
						<div className="flex h-16 items-end gap-1.5">
							{spans.map((span) => {
								const tone = spanTone(login, span);
								const label = spanSummary(span, dateTime, t);
								return (
									<Tooltip key={span.resetsAt}>
										<TooltipTrigger asChild>
											<div tabIndex={0} aria-label={label} className="group flex h-full w-5 flex-col items-center justify-end gap-1 outline-none">
												<div
													className={cn("w-full rounded-t-sm group-focus-visible:ring-2 group-focus-visible:ring-ring", span.state === "partial" && "border border-dashed")}
													style={{
														height: `${Math.max(4, Math.min(100, span.peakUsedPercent))}%`,
														backgroundColor: span.state === "partial" ? "transparent" : tone,
														borderColor: tone,
													}}
												/>
											</div>
										</TooltipTrigger>
										<TooltipContent><SpanDetails span={span} now={now} dateTime={dateTime} /></TooltipContent>
									</Tooltip>
								);
							})}
						</div>
					</div>
				))}
			</div>
		</section>
	);
}

function WeeklyAllowance({ login, span, now, dateTime }: { login: UsageLimitLogin; span: UsageLimitSpan; now: Date; dateTime: Intl.DateTimeFormat }) {
	const { t } = useTranslation();
	const tone = agentTone(PROVIDER_HARNESS[login.provider]);
	const start = span.startsAt ? Date.parse(span.startsAt) : Date.parse(span.firstReadAt);
	const reset = Date.parse(span.resetsAt);
	const x = (ms: number) => ((ms - start) / (reset - start)) * 100;
	const y = (percent: number) => 100 - Math.min(100, Math.max(0, percent));
	const points = span.points ?? [];
	const pace = spanPace(span);
	const last = points.at(-1);
	const stale = now.getTime() - Date.parse(span.lastReadAt) > STALE_MS;
	const note = stale
		? t("usage.windows.notReadSince", { value: dateTime.format(Date.parse(span.lastReadAt)) })
		: pace?.runsOutAt
			? t("usage.windows.runsOut", { value: dateTime.format(pace.runsOutAt) })
			: pace
				? t("usage.windows.onPace", { value: `${Math.round(pace.atReset)}%` })
				: null;
	return (
		<div className="flex min-w-0 flex-col gap-2">
			<div className="flex items-baseline justify-between gap-3">
				<span className="flex min-w-0 items-baseline gap-2">
					<LoginName login={login} />
					<span className="shrink-0 text-xs text-muted-foreground">{span.label}</span>
				</span>
				<span className="shrink-0 text-xs tabular-nums text-muted-foreground">{t("usage.windows.resets", { value: dateTime.format(reset) })}</span>
			</div>
			<svg viewBox="0 0 100 100" preserveAspectRatio="none" role="img" aria-label={`${span.label}: ${Math.round(span.peakUsedPercent)}%`} className="h-28 w-full overflow-visible rounded-md bg-muted/60">
				{[25, 50, 75].map((level) => <line key={level} x1="0" x2="100" y1={y(level)} y2={y(level)} stroke="var(--color-border)" vectorEffect="non-scaling-stroke" />)}
				{points.length > 0 ? (
					<polyline
						fill="none"
						stroke={tone}
						strokeWidth="2"
						vectorEffect="non-scaling-stroke"
						points={points.map((point) => `${x(Date.parse(point.at))},${y(point.usedPercent)}`).join(" ")}
					/>
				) : null}
				{pace && last && !stale ? (
					<line
						x1={x(Date.parse(last.at))}
						y1={y(last.usedPercent)}
						x2={x(pace.runsOutAt ? pace.runsOutAt.getTime() : reset)}
						y2={y(pace.runsOutAt ? 100 : pace.atReset)}
						stroke={tone}
						strokeWidth="1.5"
						strokeDasharray="4 3"
						vectorEffect="non-scaling-stroke"
					/>
				) : null}
				<line x1={x(now.getTime())} x2={x(now.getTime())} y1="0" y2="100" stroke="var(--color-text-muted)" strokeDasharray="2 3" vectorEffect="non-scaling-stroke" />
			</svg>
			<p className={cn("inline-flex items-center gap-1.5 text-xs tabular-nums", pace?.runsOutAt && !stale ? "text-warning" : "text-muted-foreground")}>
				{pace?.runsOutAt && !stale ? <TriangleAlert aria-hidden="true" className="size-icon-sm" /> : null}
				{t("usage.windows.used", { value: `${Math.round(last?.usedPercent ?? span.peakUsedPercent)}%` })}
				{note ? ` · ${note}` : null}
			</p>
		</div>
	);
}
