import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, type FormEvent } from "react";
import { flushSync } from "react-dom";
import { useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { ScrollArea } from "radix-ui";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import { ArrowDownUp, ArrowUpRight, CalendarClock, CircleAlert, Folder, ChevronDown, Clock, GitPullRequest, ListChecks, FileText, Filter, Pause, Pencil, Plus, Repeat, Trash2, TriangleAlert, type AppIcon, StickyNote } from "@/components/app-icons";
import { Button, GHOST_PILL_CLASS } from "./ui/button";
import { CloseButton } from "./ui/close-button";
import { ResizeHandle } from "./ResizeHandle";
import { PageTopbar, PageTopbarTitle } from "./PageTopbar";
import { FilterChip, ToolbarSearch } from "./PageToolbar";
import {
	Dialog,
	DialogClose,
	DialogContent,
	DialogDescription,
	DialogTitle,
} from "./ui/dialog";
import {
	DropdownMenu,
	DropdownMenuContent,
	DropdownMenuItem,
	DropdownMenuLabel,
	DropdownMenuRadioGroup,
	DropdownMenuRadioItem,
	DropdownMenuSeparator,
	DropdownMenuTrigger,
} from "./ui/dropdown-menu";
import { Input } from "./ui/input";
import { Pressable } from "./ui/pressable";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "./ui/select";
import { Switch } from "./ui/switch";
import { Tooltip, TooltipContent, TooltipTrigger } from "./ui/tooltip";
import { ConfirmDialog } from "./ConfirmDialog";
import { formatDuration } from "./SessionMemoryPanel";
import { AgentModelField } from "./settings/ProjectAgentRoleControls";
import { SettingsOptionMenu } from "./settings/SettingsOptionMenu";
import { switchEffort, type PendingAgentSwitch } from "./chat/AgentSwitchPicker";
import { TRIGGER_CLASS } from "./chat/TurnSettingsBar";
import { agentModelsQueryOptions } from "../hooks/useAgentModelsQuery";
import { useAgentReadinessQuery, type AgentReadinessSnapshot } from "../hooks/useAgentReadinessQuery";
import { useProjectWorkerDefaults } from "../hooks/useProjectDefaultWorker";
import { useResizable } from "../hooks/useResizable";
import { useWorkspaceQuery } from "../hooks/useWorkspaceQuery";
import { CLOUD_PROJECT_KIND, STANDALONE_PROJECT_KIND, STANDALONE_WORKSPACE_ID } from "../types/workspace";
import { agentLabel } from "../lib/agent-options";
import {
	buildRankedAgentOptions,
	DEFAULT_AGENT_PRIORITY_RANK,
	isReadyAgent,
} from "../lib/agent-select-options";
import {
	buildRRuleFromSchedule,
	clampHourDraft,
	clampMinuteDraft,
	clampMonthDayDraft,
	describeSchedule,
	formatTimeDraft,
	nextScheduledRun,
	nowLocalHHMM,
	parseHourMinute,
	parseTimeValue,
	scheduleFieldsFromRRule,
	schedulesEqual,
	WEEKDAY_CODES,
	type ScheduleFields,
	type WeekdayCode,
} from "../lib/automation-schedule";
import {
	useAutomationRuns,
	daemonProjectId,
	useAutomations,
	useCreateAutomation,
	useDeleteAutomation,
	useUpdateAutomation,
	type Automation,
	type CreateAutomationInput,
} from "../hooks/useAutomations";
import {
	centeredOnboardingDialogClass,
	onboardingAlertErrorClass,
	onboardingFieldErrorClass,
	onboardingFieldHintClass,
	onboardingFooterActionsEndClass,
	onboardingFormLabelClass,
} from "../lib/onboarding-ui";
import { AUTOMATION_TEMPLATES, type AutomationTemplateID, type AutomationUseCase } from "../lib/automation-templates";
import { automationPromptLengthBucket, captureRendererEvent } from "../lib/telemetry";
import { cn } from "../lib/utils";

type StatusFilter = "all" | "enabled" | "paused";
type SortOrder = "nextRun" | "name" | "created";
type StarterTemplateID = Exclude<AutomationTemplateID, "custom">;

const STATUS_FILTERS: StatusFilter[] = ["all", "enabled", "paused"];
const SORT_ORDERS: SortOrder[] = ["nextRun", "name", "created"];
// The quiet captions over the form's and the details panel's sections.
const FORM_CAPTION_CLASS = "text-xs font-medium text-muted-foreground";
const TEMPLATE_ICONS = { daily_pr_review_v1: GitPullRequest, daily_issue_triage_v1: ListChecks, weekly_release_notes_v1: FileText };

/** "Yesterday, 9:15 AM" to "Tomorrow, …", then "Mon, 9:00 AM" within the coming week, "Fri, Nov 13, 9:15 AM" otherwise, and the year when it isn't this one. */
function displayRelativeTime(date: Date, t: TFunction, locale?: string) {
	const days = Math.round((new Date(date).setHours(0, 0, 0, 0) - new Date().setHours(0, 0, 0, 0)) / 86_400_000);
	if (Math.abs(days) <= 1) {
		const day = new Intl.RelativeTimeFormat(locale, { numeric: "auto" }).format(days, "day");
		const time = new Intl.DateTimeFormat(locale, { timeStyle: "short" }).format(date);
		return t("automations.relativeDayTime", { day: day.charAt(0).toLocaleUpperCase(locale) + day.slice(1), time });
	}
	const withinWeek = days > 1 && days < 7;
	const otherYear = date.getFullYear() !== new Date().getFullYear();
	return new Intl.DateTimeFormat(locale, { weekday: "short", ...(withinWeek ? {} : { month: "short", day: "numeric" }), ...(otherYear ? { year: "numeric" } : {}), hour: "numeric", minute: "2-digit" }).format(date);
}

function timestamp(value?: string) {
	return value ? Date.parse(value) || 0 : 0;
}

/** Orders the list: soonest next run first with paused ones last, by name, or newest first. */
function sortAutomations(items: Automation[], order: SortOrder) {
	const byName = (left: Automation, right: Automation) => left.displayName.localeCompare(right.displayName);
	return [...items].sort((left, right) => {
		if (order === "name") return byName(left, right);
		if (order === "created") return timestamp(right.createdAt) - timestamp(left.createdAt) || byName(left, right);
		if (left.enabled !== right.enabled) return left.enabled ? -1 : 1;
		const leftNext = timestamp(left.nextRunAt) || Number.POSITIVE_INFINITY;
		const rightNext = timestamp(right.nextRunAt) || Number.POSITIVE_INFINITY;
		return leftNext === rightNext ? byName(left, right) : leftNext - rightNext;
	});
}

/** "Weekdays at 9:00 AM": a rule's cadence and clock time as a person reads it. */
function useScheduleLabel() {
	const { t, i18n } = useTranslation();
	const locale = i18n.resolvedLanguage;
	return (rrule: string) => {
		const summary = describeSchedule(rrule);
		// Rule times are wall-clock values in the automation's timezone, so format them without shifting.
		const time = summary.time
			? new Intl.DateTimeFormat(locale, { hour: "numeric", minute: "2-digit", timeZone: "UTC" }).format(new Date(Date.UTC(2000, 0, 1, summary.time.hour, summary.time.minute)))
			: null;
		const at = time ? "At" : "";
		switch (summary.kind) {
			case "daily":
				return t(`automations.summary.daily${at}`, { time });
			case "weekdays":
				return t(`automations.summary.weekdays${at}`, { time });
			case "weekly": {
				const shortDay = (day: WeekdayCode) => new Intl.DateTimeFormat(locale, { weekday: "short", timeZone: "UTC" }).format(new Date(Date.UTC(2026, 0, 5 + WEEKDAY_CODES.indexOf(day))));
				const days = summary.days.length === 1 ? t(`automations.weekday.${summary.days[0]}`) : summary.days.map(shortDay).join(", ");
				return t(`automations.summary.weekly${at}`, { days, time });
			}
			case "monthly":
				return t(`automations.summary.monthly${at}`, { day: summary.day, time });
			default:
				return summary.frequency
					? t(`automations.summary.other${at}`, { frequency: t(`automations.frequency.${summary.frequency}`, { defaultValue: summary.frequency }), time })
					: t("automations.summary.custom");
		}
	};
}

export function AutomationsView() {
	const { t } = useTranslation();
	const query = useAutomations();
	const workspaces = useWorkspaceQuery().data ?? [];
	const harnesses = useAgentReadinessQuery().data?.agents ?? [];
	const create = useCreateAutomation();
	const update = useUpdateAutomation();
	const remove = useDeleteAutomation();
	const [createOpen, setCreateOpen] = useState(false);
	const [starter, setStarter] = useState<AutomationTemplateID>("custom");
	const [search, setSearch] = useState("");
	const [status, setStatus] = useState<StatusFilter>("all");
	const [projectId, setProjectId] = useState("");
	const [sort, setSort] = useState<SortOrder>("nextRun");
	function openCreate(template: AutomationTemplateID = "custom") {
		setStarter(template);
		setCreateOpen(true);
	}
	const [editTarget, setEditTarget] = useState<Automation | null>(null);
	const [deleteTarget, setDeleteTarget] = useState<Automation | null>(null);
	const [selectedId, setSelectedId] = useState<string | null>(null);
	const [actionError, setActionError] = useState<string | null>(null);
	const automations = useMemo(() => query.data ?? [], [query.data]);
	const projectName = (id: string) => id === STANDALONE_WORKSPACE_ID ? t("standalone.workspaceName") : workspaces.find((workspace) => workspace.id === id)?.name ?? id;
	// Projects that own at least one automation, so the filter never offers an empty choice.
	const projectOptions = [...new Set(automations.map((item) => item.projectId))].map((id) => ({ id, name: projectName(id) }));
	const needle = search.trim().toLocaleLowerCase();
	const visibleAutomations = sortAutomations(
		automations.filter((item) =>
			(status === "all" || (status === "enabled" ? item.enabled : !item.enabled)) &&
			(!projectId || item.projectId === projectId) &&
			`${item.displayName} ${projectName(item.projectId)}`.toLocaleLowerCase().includes(needle)),
		sort,
	);
	const hasAutomations = automations.length > 0;
	// Details stay open only while their automation is in the visible list.
	const selected = visibleAutomations.find((item) => item.id === selectedId);
	// Forget a selection that left the list, so clearing a filter doesn't reopen it unasked.
	useEffect(() => {
		if (selectedId && !selected) setSelectedId(null);
	}, [selectedId, selected]);
	// Re-render when the day turns, so the rows' Today/Tomorrow labels stay true between refetches.
	const [dayTick, setDayTick] = useState(0);
	useEffect(() => {
		const timer = window.setTimeout(() => setDayTick((tick) => tick + 1), new Date().setHours(24, 0, 0, 0) - Date.now() + 1000);
		return () => window.clearTimeout(timer);
	}, [dayTick]);
	// Closing the details hands focus back to the row that opened them.
	function closeDetails() {
		const id = selectedId;
		flushSync(() => setSelectedId(null));
		if (id) document.getElementById(automationRowId(id))?.focus();
	}
	async function toggle(item: Automation, enabled: boolean) {
		setActionError(null);
		try {
			await update.mutateAsync({ id: item.id, body: { enabled } });
		} catch (error) {
			setActionError(error instanceof Error ? error.message : t("automations.updateError"));
		}
	}
	const clearFilters = () => {
		setSearch("");
		setStatus("all");
		setProjectId("");
	};
	const statusCounts: Record<StatusFilter, number> = { all: automations.length, enabled: automations.filter((item) => item.enabled).length, paused: automations.filter((item) => !item.enabled).length };
	const filterLabel = t("automations.filter.label");

	return (
		<div className="flex min-h-0 flex-1 flex-col bg-background">
			<PageTopbar lead={<PageTopbarTitle as="h1" icon={<CalendarClock className="size-icon-md" aria-hidden="true" />}>{t("automations.title")}</PageTopbarTitle>} />
			<main className="@container/automations relative flex min-h-0 flex-1 overflow-hidden">
			{/* A narrow window shows the list or the open details, never both squeezed. */}
			<ScrollArea.Root type="hover" scrollHideDelay={500} className={cn("relative min-h-0 min-w-0 flex-1 overflow-hidden", selected && "@max-3xl/automations:hidden")}>
				<ScrollArea.Viewport className="h-full w-full overscroll-contain focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring/60" tabIndex={0}>
				<div className="@container pt-4 pr-4 pb-4 pl-(--space-4_5)">
					{actionError ? <p role="alert" className="mb-3 rounded-md bg-destructive/10 px-3 py-2 text-xs text-destructive">{actionError}</p> : null}
					{query.isLoading ? <p role="status" className="px-3 py-8 text-center text-sm text-muted-foreground">{t("automations.loading")}</p> : null}
					{query.error ? <p role="alert" className="text-sm text-destructive">{query.error.message}</p> : null}
					{!query.isLoading && !query.error && !hasAutomations ? (
						<section aria-labelledby="automation-templates">
							<h2 id="automation-templates" className="text-control font-medium text-foreground">{t("automations.templates.title")}</h2>
							<p className="mt-1 mb-3 text-xs text-muted-foreground">{t("automations.templates.description")}</p>
							<TemplateGrid onSelect={openCreate} />
						</section>
					) : null}
					{!query.isLoading && !query.error && !hasAutomations ? (
						<div className="mt-6 flex flex-col items-center gap-3 border-t border-border pt-12 pb-8 text-center">
							<Clock aria-hidden="true" className="size-5 text-passive" />
							<p className="text-control text-muted-foreground">{t("automations.empty.title")}</p>
							<Button type="button" size="sm" variant="secondary" onClick={() => openCreate()}>{t("automations.new")}</Button>
						</div>
					) : null}
					{hasAutomations ? (
						<section aria-label={t("automations.title")}>
							<div className="mb-2">
								<div className="flex min-h-control-md items-center gap-2">
									<div role="group" aria-label={t("automations.filter.status")} className="flex min-w-0 items-center gap-1">
										{STATUS_FILTERS.map((value) => (
											<Button key={value} type="button" size="sm" variant="ghost" aria-pressed={status === value} onClick={() => setStatus(value)} className="gap-1.5">
												{t(`automations.filter.${value}`)}{" "}
												<span className="text-caption tabular-nums text-passive">{statusCounts[value]}</span>
											</Button>
										))}
									</div>
									<div className="ms-auto flex shrink-0 items-center gap-0.5">
										<ToolbarSearch label={t("automations.search")} query={search} onQueryChange={setSearch} />
										{projectOptions.length > 1 ? (
											<DropdownMenu>
												<Tooltip>
													<TooltipTrigger asChild>
														<DropdownMenuTrigger asChild>
															<Button type="button" size="icon-sm" variant="quiet" aria-label={filterLabel} data-active={projectId ? "true" : undefined}>
																<Filter aria-hidden="true" className="size-icon-sm" />
															</Button>
														</DropdownMenuTrigger>
													</TooltipTrigger>
													<TooltipContent side="bottom">{filterLabel}</TooltipContent>
												</Tooltip>
												<DropdownMenuContent align="end" className="max-h-96 min-w-52 overflow-y-auto" data-browser-native-overlay="true">
													<DropdownMenuLabel>{t("automations.filter.project")}</DropdownMenuLabel>
													<DropdownMenuRadioGroup value={projectId} onValueChange={setProjectId}>
														<DropdownMenuRadioItem value="">{t("automations.filter.allProjects")}</DropdownMenuRadioItem>
														{projectOptions.map((option) => <DropdownMenuRadioItem key={option.id} value={option.id}><span className="truncate">{option.name}</span></DropdownMenuRadioItem>)}
													</DropdownMenuRadioGroup>
												</DropdownMenuContent>
											</DropdownMenu>
										) : null}
										<DropdownMenu>
											<Tooltip>
												<TooltipTrigger asChild>
													<DropdownMenuTrigger asChild>
														<Button type="button" size="icon-sm" variant="quiet" aria-label={t("automations.sort.label")}>
															<ArrowDownUp aria-hidden="true" className="size-icon-sm" />
														</Button>
													</DropdownMenuTrigger>
												</TooltipTrigger>
												<TooltipContent side="bottom">{t("automations.sort.label")}</TooltipContent>
											</Tooltip>
											<DropdownMenuContent align="end" className="min-w-44" data-browser-native-overlay="true">
												<DropdownMenuRadioGroup value={sort} onValueChange={(value) => setSort(value as SortOrder)}>
													{SORT_ORDERS.map((value) => <DropdownMenuRadioItem key={value} value={value}>{t(`automations.sort.${value}`)}</DropdownMenuRadioItem>)}
												</DropdownMenuRadioGroup>
											</DropdownMenuContent>
										</DropdownMenu>
										<NewAutomationMenu onCreate={openCreate} />
									</div>
								</div>
								{projectId ? (
									<div className="mt-2 flex flex-wrap items-center gap-1.5">
										<FilterChip
											label={t("automations.filter.project")}
											value={projectName(projectId)}
											removeLabel={t("automations.filter.remove", { filter: `${t("automations.filter.project")}: ${projectName(projectId)}` })}
											onRemove={() => setProjectId("")}
										/>
									</div>
								) : null}
							</div>
							{visibleAutomations.length > 0 ? (
								<div className="flex flex-col gap-0.5">
									{visibleAutomations.map((item) => (
										<AutomationRow key={item.id} item={item} projectName={projectName(item.projectId)} selected={item.id === selected?.id} onSelect={() => setSelectedId(item.id === selected?.id ? null : item.id)} />
									))}
								</div>
							) : (
								<div className="flex flex-col items-center gap-1 py-16 text-center">
									<p className="text-control font-medium text-foreground">{t("automations.noMatches")}</p>
									<Button variant="quiet" size="sm" onClick={clearFilters}>{t("automations.clearFilters")}</Button>
								</div>
							)}
						</section>
					) : null}
				</div>
				</ScrollArea.Viewport>
				<ScrollArea.Scrollbar orientation="vertical" className="z-10 flex w-2 touch-none select-none p-0.5">
					<ScrollArea.Thumb className="relative flex-1 rounded-full bg-[color-mix(in_srgb,var(--color-scrollbar)_42%,transparent)] hover:bg-[color-mix(in_srgb,var(--color-scrollbar)_56%,transparent)]" />
				</ScrollArea.Scrollbar>
			</ScrollArea.Root>
			{selected ? (
				<AutomationDetails key={selected.id} item={selected} projectName={projectName(selected.projectId)} busy={update.isPending}
					onClose={closeDetails} onEdit={() => setEditTarget(selected)} onDelete={() => setDeleteTarget(selected)} onToggle={(enabled) => toggle(selected, enabled)} />
			) : null}
			</main>
			<AutomationFormDialog initialTemplateId={starter} open={createOpen} workspaces={workspaces} harnesses={harnesses} busy={create.isPending} error={create.error?.message ?? null} onOpenChange={setCreateOpen} onSubmit={async (input) => { await create.mutateAsync(input as CreateAutomationInput); setCreateOpen(false); }} />
			<AutomationFormDialog open={Boolean(editTarget)} automation={editTarget ?? undefined} workspaces={workspaces} harnesses={harnesses} busy={update.isPending} error={update.error?.message ?? null} onOpenChange={(open) => { if (!open) setEditTarget(null); }} onSubmit={async (input) => { if (!editTarget) return; await update.mutateAsync({ id: editTarget.id, body: input }); setEditTarget(null); }} />
			<ConfirmDialog open={Boolean(deleteTarget)} title={t("automations.delete.title")} description={t("automations.delete.description", { name: deleteTarget?.displayName })} confirmLabel={t("automations.delete.confirm")} destructive busy={remove.isPending} error={remove.error?.message ?? null} onOpenChange={(open) => { if (!open) setDeleteTarget(null); }} onConfirm={() => { if (!deleteTarget) return; remove.mutate(deleteTarget.id, { onSuccess: () => setDeleteTarget(null) }); }} />
		</div>
	);
}

/** The page's one primary action: a blank automation or one of the starters. */
function NewAutomationMenu({ onCreate }: { onCreate: (template?: AutomationTemplateID) => void }) {
	const { t } = useTranslation();
	return (
		<DropdownMenu>
			<DropdownMenuTrigger asChild>
				<Button type="button" size="sm" variant="primary" className="ms-1">
					{t("automations.new")}
					<ChevronDown aria-hidden="true" className="size-icon-sm" />
				</Button>
			</DropdownMenuTrigger>
			<DropdownMenuContent align="end" className="min-w-52" data-browser-native-overlay="true">
				<DropdownMenuItem onSelect={() => onCreate()}>
					<Plus aria-hidden="true" className="size-icon-sm" />
					{t("automations.new.scratch")}
				</DropdownMenuItem>
				<DropdownMenuSeparator />
				<DropdownMenuLabel>{t("automations.templates.label")}</DropdownMenuLabel>
				{AUTOMATION_TEMPLATES.map((template) => {
					const Icon = TEMPLATE_ICONS[template.id];
					return (
						<DropdownMenuItem key={template.id} onSelect={() => onCreate(template.id)}>
							<Icon aria-hidden="true" className="size-icon-sm" />
							{t(`automations.template.${template.id}`)}
						</DropdownMenuItem>
					);
				})}
			</DropdownMenuContent>
		</DropdownMenu>
	);
}

function TemplateGrid({ onSelect }: { onSelect: (template: StarterTemplateID) => void }) {
	return (
		<div className="grid grid-cols-1 gap-3 @lg:grid-cols-2 @3xl:grid-cols-3">
			{AUTOMATION_TEMPLATES.map((template) => <TemplateTile key={template.id} id={template.id} rrule={template.rrule} onSelect={() => onSelect(template.id)} />)}
		</div>
	);
}

function TemplateTile({ id, rrule, onSelect }: { id: StarterTemplateID; rrule: string; onSelect: () => void }) {
	const { t } = useTranslation();
	const scheduleLabel = useScheduleLabel();
	const Icon = TEMPLATE_ICONS[id];
	return (
		<Pressable aria-label={t(`automations.template.${id}`)} onClick={onSelect}
			className="group flex min-w-0 flex-col items-start gap-4 rounded-lg bg-interactive-hover p-4 text-start hover:bg-interactive-active">
			<Icon aria-hidden="true" className="size-5 shrink-0 text-muted-foreground group-hover:text-foreground" />
			<span className="flex min-w-0 flex-col">
				<span className="text-sm font-medium text-foreground">{t(`automations.template.${id}`)}</span>
				<span className="mt-1 text-pretty text-xs leading-5 text-muted-foreground">{t(`automations.template.${id}.description`)}</span>
				<span className="mt-3 flex items-center gap-1.5 text-caption tabular-nums text-passive">
					<Clock aria-hidden="true" className="size-icon-xs shrink-0" />
					{scheduleLabel(rrule)}
				</span>
			</span>
		</Pressable>
	);
}

/** An automation's or run's state at a glance: failed, paused, has run, or not run yet. */
function StatusMark({ state }: { state: "failed" | "paused" | "ran" | "waiting" }) {
	if (state === "failed") return <CircleAlert aria-hidden="true" className="size-icon-sm shrink-0 text-destructive" />;
	if (state === "paused") return <Pause aria-hidden="true" className="size-icon-sm shrink-0 text-muted-foreground" />;
	return <StatusDot state={state} />;
}

/** A run's state as a dot: red when it failed, filled when it ran, hollow while it waits. */
function StatusDot({ state }: { state: "failed" | "ran" | "waiting" }) {
	return (
		<span aria-hidden="true" className="flex size-icon-sm shrink-0 items-center justify-center">
			<span className={cn("size-1.5 rounded-full", state === "failed" ? "bg-destructive" : state === "ran" ? "bg-muted-foreground" : "border border-muted-foreground")} />
		</span>
	);
}

function AutomationRow({ item, projectName, selected, onSelect }: { item: Automation; projectName: string; selected: boolean; onSelect: () => void }) {
	const { t, i18n } = useTranslation();
	const locale = i18n.resolvedLanguage;
	const scheduleLabel = useScheduleLabel();
	const failed = item.enabled && item.latestRun?.status === "failed";
	const failedAt = item.latestRun?.finishedAt ?? item.latestRun?.scheduledFor;
	return <article>
		<Pressable id={automationRowId(item.id)} aria-expanded={selected} aria-controls={selected ? AUTOMATION_DETAILS_ID : undefined} onClick={onSelect}
			className={cn("flex w-full min-w-0 items-center gap-3 rounded-md px-2 py-2.5 text-start", selected ? "bg-interactive-hover" : "hover:bg-interactive-hover")}>
			<StatusMark state={!item.enabled ? "paused" : failed ? "failed" : item.latestRun ? "ran" : "waiting"} />
			<div className="min-w-0 flex-1">
				<h2 className="truncate text-control font-medium leading-5 text-foreground">{item.displayName}</h2>
				<p className="flex min-w-0 items-center gap-1.5 text-caption leading-5 text-muted-foreground">
					<span className="truncate">{scheduleLabel(item.rrule)}</span>
					<span aria-hidden="true" className="text-passive">·</span>
					<span className="truncate">{projectName}</span>
				</p>
			</div>
			<span className={cn("sr-only shrink-0 text-caption tabular-nums @md:not-sr-only", failed ? "text-destructive" : item.enabled ? "text-foreground" : "text-muted-foreground")}>
				{!item.enabled
					? t("automations.paused")
					: failed && failedAt
						? t("automations.failedOn", { date: new Intl.DateTimeFormat(locale, { month: "short", day: "numeric" }).format(new Date(failedAt)) })
						: item.nextRunAt ? displayRelativeTime(new Date(item.nextRunAt), t, locale) : null}
			</span>
		</Pressable>
	</article>;
}

const AUTOMATION_DETAILS_ID = "automation-details";
const automationRowId = (id: string) => `automation-row-${id}`;
const DETAILS_WIDTH_VAR = "--automation-details-w";
const DETAILS_DEFAULT_WIDTH_PX = 400;
const DETAILS_MIN_WIDTH_PX = 340;
const DETAILS_MAX_WIDTH_PX = 640;
/** Room the list keeps beside the details: its whole toolbar, project filter included. The aside's max-w repeats it. */
const DETAILS_LIST_MIN_WIDTH_PX = 480;
/** Runs shown at first, and how many more each Show more adds. */
const RUNS_PAGE_SIZE = 5;

/** The selected automation beside the list: its actions, schedule, runs and prompt. */
function AutomationDetails({ item, projectName, busy, onClose, onEdit, onDelete, onToggle }: { item: Automation; projectName: string; busy: boolean; onClose: () => void; onEdit: () => void; onDelete: () => void; onToggle: (enabled: boolean) => Promise<void> }) {
	const runs = useAutomationRuns(item.id);
	const navigate = useNavigate();
	const { t, i18n } = useTranslation();
	const locale = i18n.resolvedLanguage;
	const scheduleLabel = useScheduleLabel();
	const asideRef = useRef<HTMLElement>(null);
	const closeRef = useRef<HTMLButtonElement>(null);
	// A narrow window hides the list, and the row that had focus with it, so focus moves into the panel.
	useEffect(() => {
		const list = asideRef.current?.previousElementSibling;
		if (list && window.getComputedStyle(list).display === "none") closeRef.current?.focus();
	}, []);
	const getResizeTargets = useCallback(() => [asideRef.current], []);
	// At most DETAILS_MAX_WIDTH_PX, and never so wide that the list loses its minimum.
	const maxWidth = useCallback(() => {
		const page = asideRef.current?.parentElement;
		if (!page || page.clientWidth <= 0) return DETAILS_MAX_WIDTH_PX;
		return Math.max(DETAILS_MIN_WIDTH_PX, Math.min(DETAILS_MAX_WIDTH_PX, page.clientWidth - DETAILS_LIST_MIN_WIDTH_PX));
	}, []);
	const { onPointerDown, onDoubleClick } = useResizable({
		cssVar: DETAILS_WIDTH_VAR,
		getCssTargets: getResizeTargets,
		storageKey: "ao.automations.detailsWidthPx",
		defaultWidth: DETAILS_DEFAULT_WIDTH_PX,
		min: DETAILS_MIN_WIDTH_PX,
		max: maxWidth,
		edge: "left",
		reclampOnWindowResize: true,
	});
	const [visibleRuns, setVisibleRuns] = useState(RUNS_PAGE_SIZE);
	const promptRef = useRef<HTMLParagraphElement>(null);
	const [promptOpen, setPromptOpen] = useState(false);
	const [promptClipped, setPromptClipped] = useState(false);
	// Offer the full prompt only when the four clamped lines cut it short.
	useLayoutEffect(() => {
		const prompt = promptRef.current;
		if (prompt && !promptOpen) setPromptClipped(prompt.scrollHeight > prompt.clientHeight);
	}, [item.prompt, promptOpen]);
	return (
		// Beside the list when there is room, at the width its handle sets; in its place in a narrow window, closed with its X.
		<aside ref={asideRef} id={AUTOMATION_DETAILS_ID} aria-label={item.displayName}
			className="relative flex min-h-0 flex-1 flex-col @3xl/automations:w-(--automation-details-w) @3xl/automations:max-w-[max(340px,calc(100%-480px))] @3xl/automations:flex-none @3xl/automations:border-s @3xl/automations:border-border">
			<div className="min-h-0 flex-1 overflow-y-auto">
			<div className="flex flex-col gap-6 p-4">
				<header className="flex flex-col gap-3">
					<div className="flex items-start gap-2">
						<div className="min-w-0 flex-1">
							<h2 className="truncate text-subtitle font-semibold leading-6 text-foreground">{item.displayName}</h2>
							<p className="truncate text-xs text-muted-foreground">{projectName} · {item.harness ? agentLabel(item.harness) : t("automations.projectDefault")}</p>
						</div>
						<CloseButton ref={closeRef} aria-label={t("common.close")} onClick={onClose} />
					</div>
					<div className="flex items-center gap-1">
						<Button type="button" size="sm" variant="secondary" aria-label={t("automations.edit.aria", { name: item.displayName })} onClick={onEdit}>
							<Pencil aria-hidden="true" className="size-icon-sm" />
							{t("automations.details.edit")}
						</Button>
						<Button type="button" size="sm" variant="quiet" aria-label={t("automations.delete.aria", { name: item.displayName })} onClick={onDelete}>
							<Trash2 aria-hidden="true" className="size-icon-sm" />
							{t("automations.details.delete")}
						</Button>
						<span aria-hidden="true" className="ms-auto text-xs text-muted-foreground">{t(item.enabled ? "automations.details.on" : "automations.details.off")}</span>
						<Switch checked={item.enabled} disabled={busy} aria-label={t(item.enabled ? "automations.disable" : "automations.enable", { name: item.displayName })} onCheckedChange={(checked) => void onToggle(checked)} />
					</div>
				</header>
				<section className="flex flex-col gap-1">
					<h3 className={FORM_CAPTION_CLASS}>{t("automations.field.schedule")}</h3>
					<p className="text-sm text-foreground">
						{scheduleLabel(item.rrule)}
						<span aria-hidden="true" className="text-passive"> · </span>
						<span className="text-muted-foreground">{item.timezone}</span>
					</p>
					<p className="text-xs tabular-nums text-muted-foreground">
						{item.enabled ? t("automations.nextRunAt", { time: item.nextRunAt ? displayRelativeTime(new Date(item.nextRunAt), t, locale) : "—" }) : t("automations.paused")}
					</p>
				</section>
				<section className="flex flex-col gap-1">
					<h3 className={FORM_CAPTION_CLASS}>{t("automations.runs.title")}</h3>
					{runs.isLoading ? (
						<p role="status" className="py-2 text-xs text-muted-foreground">{t("automations.runs.loading")}</p>
					) : runs.error ? (
						<p role="alert" className="py-2 text-xs text-destructive">{runs.error.message}</p>
					) : runs.data?.length ? (
						<>
							<ul className="divide-y divide-border">
								{runs.data.slice(0, visibleRuns).map((run) => {
									const failed = run.status === "failed";
									const duration = run.status === "completed" && run.startedAt && run.finishedAt ? formatDuration(Date.parse(run.finishedAt) - Date.parse(run.startedAt)) : null;
									const status = duration ?? t(`automations.runStatus.${run.status}`, { defaultValue: run.status });
									const statusClass = cn("shrink-0 text-xs tabular-nums", failed ? "text-destructive" : "text-muted-foreground");
									return (
										<li key={run.id} className="flex min-h-9 items-center gap-2.5 py-1.5">
											<StatusDot state={failed ? "failed" : run.status === "completed" ? "ran" : "waiting"} />
											<p className="min-w-0 flex-1 truncate text-sm tabular-nums text-foreground">{displayRelativeTime(new Date(run.scheduledFor), t, locale)}</p>
											{run.errorMessage ? (
												// The error stays one hover or focus away, so a long message never stretches the row.
												<Tooltip>
													<TooltipTrigger asChild>
														<button type="button" aria-label={`${status}: ${run.errorMessage}`} className={cn(GHOST_PILL_CLASS, statusClass, "-mx-1.5 inline-flex h-control-sm items-center gap-1 rounded-md px-1.5 outline-none")}>
															{status}
															<CircleAlert aria-hidden="true" className="size-icon-sm text-destructive" />
														</button>
													</TooltipTrigger>
													<TooltipContent side="left" className="max-w-72">{run.errorMessage}</TooltipContent>
												</Tooltip>
											) : (
												<span className={statusClass}>{status}</span>
											)}
											{run.sessionId ? (
												<Button type="button" size="icon-xs" variant="quiet" aria-label={t("automations.runs.openSession")} onClick={() => void navigate({ to: "/sessions/$sessionId", params: { sessionId: run.sessionId! } })}>
													<ArrowUpRight aria-hidden="true" className="size-icon-sm" />
												</Button>
											) : null}
										</li>
									);
								})}
							</ul>
							{runs.data.length > visibleRuns ? (
								<Button type="button" size="sm" variant="quiet" className="-ms-2.5 self-start" onClick={() => setVisibleRuns(visibleRuns + RUNS_PAGE_SIZE)}>
									{t("automations.runs.showMore")}
								</Button>
							) : null}
						</>
					) : (
						<p className="py-2 text-xs text-muted-foreground">{t("automations.runs.empty")}</p>
					)}
				</section>
				<section className="flex flex-col gap-1">
					<h3 className={FORM_CAPTION_CLASS}>{t("automations.field.prompt")}</h3>
					<p ref={promptRef} className={cn("whitespace-pre-wrap text-sm leading-6 text-foreground", !promptOpen && "line-clamp-4")}>{item.prompt}</p>
					{promptClipped ? (
						<Button type="button" size="sm" variant="quiet" className="-ms-2.5 self-start" onClick={() => setPromptOpen(!promptOpen)}>
							{t(promptOpen ? "automations.details.showLess" : "automations.details.showFullPrompt")}
						</Button>
					) : null}
				</section>
			</div>
			</div>
			{/* Outside the scroller so it spans the panel's full height; a narrow window has nothing beside it to resize against. */}
			<ResizeHandle side="left" edge={() => asideRef.current} className="hidden @3xl/automations:block" data-testid="automation-details-resize-handle" onPointerDown={onPointerDown} onDoubleClick={onDoubleClick} />
		</aside>
	);
}


type WorkspaceOption = { id: string; name: string; kind?: string; standaloneAgent?: boolean };
type AutomationFormSubmit = {
	templateId?: AutomationTemplateID;
	useCase?: AutomationUseCase;
	projectId?: string;
	displayName: string;
	prompt: string;
	kind?: "worker" | "orchestrator";
	harness?: string;
	model?: string;
	mode?: string;
	effort?: string;
	timezone?: string;
	rrule?: string;
};
type AutomationFormDialogProps = {
	open: boolean;
	initialTemplateId?: AutomationTemplateID;
	automation?: Automation;
	workspaces: WorkspaceOption[];
	harnesses: AgentReadinessSnapshot[];
	busy: boolean;
	error: string | null;
	onOpenChange: (open: boolean) => void;
	onSubmit: (input: AutomationFormSubmit) => Promise<void>;
};

type AutomationField = "projectId" | "name" | "prompt" | "time" | "hour" | "minute" | "monthDay";
type AutomationValidationErrors = Partial<Record<AutomationField, string>>;

const AUTOMATION_FIELD_IDS: Record<AutomationField, string> = {
	projectId: "automation-project",
	name: "automation-name",
	prompt: "automation-prompt",
	time: "automation-time",
	hour: "automation-hour",
	minute: "automation-minute",
	monthDay: "automation-month-day",
};

function AutomationFormDialog({
	open,
	initialTemplateId = "custom",
	automation,
	workspaces,
	harnesses,
	busy,
	error,
	onOpenChange,
	onSubmit,
}: AutomationFormDialogProps) {
	const { t, i18n } = useTranslation();
	const editing = Boolean(automation);
	const timezone = automation?.timezone || Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC";
	const [projectId, setProjectId] = useState("");
	const projectDefaults = useProjectWorkerDefaults(open ? daemonProjectId(automation?.projectId || projectId) : "");
	const projectDefaultWorker = projectDefaults.harness;
	const [name, setName] = useState("");
	const nameRef = useRef<HTMLInputElement>(null);
	const projectRef = useRef<HTMLButtonElement>(null);
	const [prompt, setPrompt] = useState("");
	const [draftTemplateId, setDraftTemplateId] = useState<AutomationTemplateID>(initialTemplateId);
	const templateId = automation?.templateId ?? draftTemplateId;
	const template = AUTOMATION_TEMPLATES.find((item) => item.id === templateId);
	const [harness, setHarness] = useState("");
	const [model, setModel] = useState("");
	const [mode, setMode] = useState("");
	const [effort, setEffort] = useState("");
	const [modelValid, setModelValid] = useState(true);
	const [schedule, setSchedule] = useState<ScheduleFields>(() => ({
		preset: "daily",
		time: nowLocalHHMM(),
		customFrequency: "monthly",
		monthDay: "1",
		weekday: "MO",
		hour: "9",
		minute: "0",
		legacyRaw: null,
	}));
	const [initialSchedule, setInitialSchedule] = useState<ScheduleFields | null>(null);
	const [validationErrors, setValidationErrors] = useState<AutomationValidationErrors>({});

	function patchSchedule(patch: Partial<ScheduleFields>) {
		setSchedule((current) => {
			const clearsLegacy = Object.keys(patch).some((key) => key !== "legacyRaw");
			return {
				...current,
				...patch,
				legacyRaw: patch.legacyRaw !== undefined ? patch.legacyRaw : clearsLegacy ? null : current.legacyRaw,
			};
		});
	}

	function applyTemplate(id: StarterTemplateID) {
		const starter = AUTOMATION_TEMPLATES.find((item) => item.id === id);
		if (!starter) return;
		setDraftTemplateId(id);
		setName(t(`automations.template.${id}`));
		setPrompt(starter.prompt);
		setSchedule(scheduleFieldsFromRRule(starter.rrule));
		setValidationErrors({});
	}

	useEffect(() => {
		if (!open) return;
		if (!automation) void captureRendererEvent("ao.automations.create_opened");
		setProjectId(automation?.projectId ?? "");
		setName(automation?.displayName ?? "");
		setPrompt(automation?.prompt ?? "");
		setHarness(automation?.harness ?? "");
		setModel(automation?.model ?? "");
		setMode(automation?.mode ?? "");
		setEffort(automation?.effort ?? "");
		setModelValid(true);
		const defaultTime = nowLocalHHMM();
		const [defaultHour, defaultMinute] = defaultTime.split(":");
		const nextSchedule = automation
			? scheduleFieldsFromRRule(automation.rrule)
			: {
					preset: "daily" as const,
					time: defaultTime,
					customFrequency: "daily" as const,
					monthDay: "1",
					weekday: "MO" as WeekdayCode,
					hour: defaultHour,
					minute: defaultMinute,
					legacyRaw: null,
				};
		setInitialSchedule(nextSchedule);
		setSchedule(nextSchedule);
		setValidationErrors({});
		setDraftTemplateId("custom");
		if (!automation && initialTemplateId !== "custom") applyTemplate(initialTemplateId);
	// Language changes must not discard edits to an open form.
	// eslint-disable-next-line react-hooks/exhaustive-deps
	}, [open, automation, initialTemplateId]);

	const nextRun = schedule.legacyRaw || (automation && automation.timezone !== Intl.DateTimeFormat().resolvedOptions().timeZone)
		? null
		: nextScheduledRun(schedule);
	// Re-render when the shown run passes or the day turns, so Next run and its
	// Today/Tomorrow label stay true for as long as the dialog is open.
	const [clockTick, setClockTick] = useState(0);
	const nextRunAt = nextRun?.getTime();
	useEffect(() => {
		if (!open || nextRunAt === undefined) return;
		const now = Date.now();
		const midnight = new Date(now).setHours(24, 0, 0, 0);
		const timer = window.setTimeout(() => setClockTick((tick) => tick + 1), Math.min(nextRunAt, midnight) - now + 1000);
		return () => window.clearTimeout(timer);
	}, [open, nextRunAt, clockTick]);
	// Automations run on this machine's daemon, which knows no Cloud project (it
	// answers "Unknown project"), and a Cloud standalone agent takes no session
	// but its own: neither is a project for an automation.
	const projectOptions = [
		{ value: STANDALONE_WORKSPACE_ID, label: t("standalone.workspaceName") },
		...workspaces
			.filter((item) => item.kind !== CLOUD_PROJECT_KIND && item.kind !== STANDALONE_PROJECT_KIND && !item.standaloneAgent)
			.map((item) => ({ value: item.id, label: item.name })),
	];
	if (automation && !projectOptions.some((option) => option.value === automation.projectId)) {
		projectOptions.unshift({ value: automation.projectId, label: automation.projectId });
	}
	// The ready agents, ranked as the composer offers them.
	const agentChoices = buildRankedAgentOptions({
		agents: harnesses,
		priorityRank: DEFAULT_AGENT_PRIORITY_RANK,
		fallbackAgents: [],
	}).filter(isReadyAgent).map(({ id, label }) => ({ id, label }));
	// Prefer an explicit choice, then the project's resolved worker, then the
	// first ready harness so Model isn't stuck on "Select agent" before a
	// project is picked.
	const selectedHarness = harness || projectDefaultWorker || agentChoices[0]?.id || "";
	const queryClient = useQueryClient();
	// Another agent, alone or with one of its models, is taken at once, as on the new chat page.
	const switchAgent = (next: PendingAgentSwitch | undefined) => {
		if (!next) return;
		setHarness(next.harness);
		setModel(""); setMode(""); setEffort(""); setModelValid(true);
		if (next.model) {
			// Some agents list launch modes in place of models.
			const catalog = queryClient.getQueryData(agentModelsQueryOptions(next.harness, daemonProjectId(projectId)).queryKey);
			if (catalog?.selectionMode === "mode") setMode(next.model);
			else setModel(next.model);
		}
		const nextEffort = switchEffort(next);
		if (nextEffort) setEffort(nextEffort);
	};
	const awaitingInheritedHarness = Boolean(automation && !automation.harness && !harness && !projectDefaults.loaded);
	// Match the daemon's base config plus overrides for a matching worker harness.
	const inheritedWorker = !projectDefaults.worker?.agent || projectDefaults.worker.agent === selectedHarness ? projectDefaults.worker?.agentConfig : undefined;
	const inheritedModel = inheritedWorker?.model || projectDefaults.agentConfig?.model || "";
	const inheritedMode = inheritedWorker?.mode || projectDefaults.agentConfig?.mode || "";
	const inheritedEffort = inheritedWorker?.effort || projectDefaults.agentConfig?.effort || "";
	const displayedModel = model || (!mode ? inheritedModel : "");
	const displayedMode = mode || (!model ? inheritedMode : "");
	const displayedEffort = effort || (!model && !mode ? inheritedEffort : "");
	const previousHarness = useRef<{ automation?: Automation; harness: string } | null>(null);
	useEffect(() => {
		if (!open || awaitingInheritedHarness) { previousHarness.current = null; return; }
		if (!previousHarness.current || previousHarness.current.automation !== automation) {
			// Opening an edit hydrates its saved harness in the preceding effect.
			previousHarness.current = { automation, harness: automation?.harness || selectedHarness };
			return;
		}
		if (previousHarness.current.harness !== selectedHarness) {
			setModel(""); setMode(""); setEffort(""); setModelValid(true);
			previousHarness.current.harness = selectedHarness;
		}
	}, [open, automation, selectedHarness, awaitingInheritedHarness]);

	function clearValidationError(field: AutomationField) {
		setValidationErrors((current) => {
			if (!current[field]) return current;
			const next = { ...current };
			delete next[field];
			return next;
		});
	}

	async function submit(event: FormEvent) {
		event.preventDefault();
		if (!modelValid || awaitingInheritedHarness) return;
		const nextErrors: AutomationValidationErrors = {};
		if (!editing && !projectId) nextErrors.projectId = t("automations.validation.project");
		if (!name.trim()) nextErrors.name = t("automations.validation.name");
		if (!prompt.trim()) nextErrors.prompt = t("automations.validation.prompt");
		else if (new TextEncoder().encode(prompt.trim()).length > 4096) nextErrors.prompt = t("automations.validation.promptBytes");
		if (schedule.preset !== "custom") {
			if (!parseTimeValue(schedule.time)) nextErrors.time = t("automations.validation.time");
		} else {
			if (!parseHourMinute(schedule.hour, schedule.minute)) {
				nextErrors.hour = t("automations.validation.hour");
				nextErrors.minute = t("automations.validation.minute");
			}
			if (schedule.customFrequency === "monthly" && !/^[1-9]$|^[12]\d$|^3[01]$/.test(schedule.monthDay.trim())) {
				nextErrors.monthDay = t("automations.validation.date");
			}
		}
		setValidationErrors(nextErrors);
		const firstInvalid = (["projectId", "name", "prompt", "time", "hour", "minute", "monthDay"] as const).find((field) => nextErrors[field]);
		if (firstInvalid) {
			if (firstInvalid === "projectId") projectRef.current?.focus();
			else document.getElementById(AUTOMATION_FIELD_IDS[firstInvalid])?.focus();
			return;
		}
		const nextRRule = buildRRuleFromSchedule(schedule);
		const scheduleChanged = !(editing && automation && initialSchedule && schedulesEqual(schedule, initialSchedule));
		const tuning = {
			...(!editing || model !== (automation?.model ?? "") ? { model } : {}),
			...(!editing || mode !== (automation?.mode ?? "") ? { mode } : {}),
			...(!editing || effort !== (automation?.effort ?? "") ? { effort } : {}),
		};
		const harnessChanged = !editing || harness !== (automation?.harness ?? "") || Object.keys(tuning).length > 0;
		const funnel = editing
			? null
			: {
					project_id: projectId,
					// Telemetry still files Weekdays under custom, where it lived before it was a preset.
					schedule_preset: schedule.preset === "weekdays" ? "custom" : schedule.preset,
					prompt_length_bucket: automationPromptLengthBucket(prompt.trim()),
				};
		try {
			await onSubmit({
				// Kind is not a form choice: automations are workers, and editing
				// leaves the stored kind untouched.
				...(editing ? {} : { projectId, timezone, templateId, useCase: template?.useCase ?? "unknown", kind: "worker" as const }),
				displayName: name,
				prompt,
				...tuning,
				// A new pin resets omitted backend fields, so persist the complete tuple.
				...(harnessChanged && selectedHarness ? { harness: selectedHarness, model, mode, effort } : {}),
				...(scheduleChanged ? { rrule: nextRRule } : {}),
			});
		} catch (error) {
			if (!funnel) throw error;
			// The dialog already renders the mutation's error, so letting this
			// reject only reported the same failure a second time as an
			// unhandled renderer exception.
			void captureRendererEvent("ao.renderer.automation_create_failed", funnel);
			return;
		}
		if (funnel) void captureRendererEvent("ao.renderer.automation_create_succeeded", funnel);
	}

	return (
		<Dialog open={open} onOpenChange={onOpenChange}>
			<DialogContent showCloseButton={false} className={cn(centeredOnboardingDialogClass, "h-[min(640px,calc(100svh-24px))] max-h-none w-[min(960px,calc(100vw-24px))] rounded-panel")}>
				<div className="flex shrink-0 items-center gap-2 border-b border-border py-2 ps-4 pe-2">
					<DialogTitle className="min-w-0 flex-1 truncate text-heading-sm font-normal text-muted-foreground">
						{t(editing ? "automations.edit" : "automations.new")}
					</DialogTitle>
					<DialogDescription className="sr-only">
						{t(editing ? "automations.edit.description" : "automations.create.description")}
					</DialogDescription>
					<DialogClose asChild>
						<CloseButton disabled={busy} aria-label={t("automations.create.close")} />
					</DialogClose>
				</div>
				<form className="flex min-h-0 flex-1 flex-col" noValidate onSubmit={(event) => void submit(event)}>
					<div className="grid min-h-0 flex-1 grid-cols-1 overflow-y-auto md:grid-cols-[minmax(0,1fr)_380px] md:overflow-hidden">
					<div className="flex min-h-0 flex-col gap-4 p-6 md:overflow-y-auto">
						{Object.keys(validationErrors).length > 0 ? (
							<div role="alert" className={cn(onboardingAlertErrorClass, "flex items-start gap-2")}>
								<TriangleAlert className="mt-0.5 size-4 shrink-0" aria-hidden="true" />
								<span>{t("automations.validation.summary")}</span>
							</div>
						) : null}
						<div className="flex flex-col gap-1">
							{/* The name reads as the dialog's heading; the pencil puts the caret in it. */}
							<div className="flex min-w-0 items-center gap-1">
								<input ref={nameRef} id={AUTOMATION_FIELD_IDS.name} required maxLength={120} value={name} placeholder={t("automations.field.name")} aria-label={t("automations.field.name")} aria-invalid={Boolean(validationErrors.name) || undefined} aria-describedby={validationErrors.name ? `${AUTOMATION_FIELD_IDS.name}-error` : undefined} onChange={(event) => { setName(event.target.value); if (event.target.value.trim()) clearValidationError("name"); }}
									className="field-sizing-content -ms-1.5 min-w-[4ch] max-w-full rounded-md bg-transparent px-1.5 py-0.5 text-heading font-semibold leading-tight text-foreground outline-none placeholder:text-passive focus:bg-input/50" />
								<Button type="button" size="icon-sm" variant="quiet" tabIndex={-1} aria-label={t("shell.rename")} onClick={() => { nameRef.current?.focus(); nameRef.current?.select(); }}>
									<Pencil aria-hidden="true" className="size-icon-sm" />
								</Button>
							</div>
							{validationErrors.name ? <p id={`${AUTOMATION_FIELD_IDS.name}-error`} className={onboardingFieldErrorClass}>{validationErrors.name}</p> : null}
						</div>
						<div className="flex min-h-48 flex-1 flex-col gap-2 rounded-lg border border-border bg-input/50 px-4 py-3 focus-within:border-ring has-aria-invalid:border-destructive">
							<label htmlFor={AUTOMATION_FIELD_IDS.prompt} className={FORM_CAPTION_CLASS}>{t("automations.field.prompt")}</label>
							<textarea
								id={AUTOMATION_FIELD_IDS.prompt}
								required
								maxLength={4096}
								value={prompt}
								aria-invalid={Boolean(validationErrors.prompt) || undefined}
								aria-describedby={validationErrors.prompt ? `${AUTOMATION_FIELD_IDS.prompt}-error` : undefined}
								onChange={(event) => { setPrompt(event.target.value); if (event.target.value.trim()) clearValidationError("prompt"); }}
								className="min-h-0 w-full flex-1 resize-none bg-transparent text-[13px] leading-5 outline-none"
							/>
						</div>
						{validationErrors.prompt ? <p id={`${AUTOMATION_FIELD_IDS.prompt}-error`} className={onboardingFieldErrorClass}>{validationErrors.prompt}</p> : null}
					</div>
					<div className="flex min-h-0 flex-col gap-6 border-t border-border p-4 md:overflow-y-auto md:border-t-0 md:border-s">
						<section className="flex flex-col gap-2" aria-labelledby="automation-run-in">
						<h3 id="automation-run-in" className={FORM_CAPTION_CLASS}>{t("automations.section.runIn")}</h3>
						{/* The composer's project picker, then its one agent, model and effort picker. */}
						<div className="-ms-3 flex min-w-0 items-center gap-1">
							<SettingsOptionMenu
								triggerRef={projectRef}
								aria-invalid={Boolean(validationErrors.projectId)}
								aria-describedby={validationErrors.projectId ? `${AUTOMATION_FIELD_IDS.projectId}-error` : undefined}
								aria-label={`${t("automations.field.project")}: ${projectOptions.find((option) => option.value === projectId)?.label ?? t("automations.projectPlaceholder")}`}
								value={projectId}
								options={projectOptions}
								placeholder={t("automations.projectPlaceholder")}
								disabled={editing}
								onChange={(value) => { setProjectId(value); setModel(""); setMode(""); setEffort(""); setModelValid(true); clearValidationError("projectId"); }}
								menuAlign="start"
								menuClassName="w-48 max-h-72"
								menuItemClassName="gap-2! [&_svg]:text-current!"
								triggerVariant="ghost"
								triggerClassName={cn(TRIGGER_CLASS, "w-fit min-w-0")}
								showCaret={false}
								renderTrigger={(selected, placeholder) => (
									<span className="inline-flex min-w-0 items-center gap-1.5">
										<ProjectMark projectId={selected?.value} className="size-3.5 text-muted-foreground" />
										<span className="min-w-0 truncate">{selected?.label ?? placeholder}</span>
									</span>
								)}
								renderMenuItem={(option) => (
									<>
										<ProjectMark projectId={option.value} className="size-icon-sm" />
										<span className="min-w-0 truncate">{option.label}</span>
									</>
								)}
							/>
							{open ? (
								<div className="flex shrink-0 items-center" inert={busy || awaitingInheritedHarness} aria-busy={awaitingInheritedHarness}>
									<AgentModelField
										key={selectedHarness + ":" + projectId}
										role="worker"
										variant="task"
										agentId={selectedHarness}
										projectId={daemonProjectId(projectId)}
										model={displayedModel}
										mode={displayedMode}
										effort={displayedEffort}
										onModelChange={setModel}
										onModeChange={setMode}
										onEffortChange={setEffort}
										onValidityChange={setModelValid}
										agentSwitch={{ projectId: daemonProjectId(projectId), agents: agentChoices, disabled: busy, onPendingChange: switchAgent }}
									/>
								</div>
							) : null}
						</div>
						{validationErrors.projectId ? <p id={`${AUTOMATION_FIELD_IDS.projectId}-error`} className={onboardingFieldErrorClass}>{validationErrors.projectId}</p> : null}
						</section>
						<section className="flex flex-col gap-2" aria-labelledby="automation-schedule">
							<h3 id="automation-schedule" className={FORM_CAPTION_CLASS}>{t("automations.field.schedule")}</h3>
							<Field label={t("automations.field.schedule")} hideLabel>
								<AutomationSelect
									icon={Repeat}
									label={t("automations.field.schedule")}
									value={schedule.preset}
									onValueChange={(value) => {
										patchSchedule({ preset: value as ScheduleFields["preset"] });
										clearValidationError("time");
										clearValidationError("hour");
										clearValidationError("minute");
										clearValidationError("monthDay");
									}}
									options={[
										{ value: "daily", label: t("automations.schedule.daily") },
										{ value: "weekdays", label: t("automations.customFrequency.weekdays") },
										{ value: "weekly", label: t("automations.schedule.weekly") },
										{ value: "custom", label: t("automations.schedule.custom") },
									]}
								/>
							</Field>
							{schedule.preset === "weekly" ? (
								<Field label={t("automations.field.weekday")} hideLabel>
									<AutomationSelect
										icon={CalendarClock}
										label={t("automations.field.weekday")}
										value={schedule.weekday}
										onValueChange={(value) => patchSchedule({ weekday: value as WeekdayCode })}
										options={WEEKDAY_CODES.map((code) => ({
											value: code,
											label: t(`automations.weekday.${code}`),
										}))}
									/>
								</Field>
							) : null}
							{schedule.preset !== "custom" ? (
								<Field label={t("automations.field.localTime")} id={AUTOMATION_FIELD_IDS.time} error={validationErrors.time} hideLabel>
									<div className="relative">
									<Clock aria-hidden="true" className="pointer-events-none absolute start-3 top-1/2 size-icon-sm -translate-y-1/2 text-muted-foreground" />
									<Input
										id={AUTOMATION_FIELD_IDS.time}
										type="text"
										inputMode="numeric"
										autoComplete="off"
										spellCheck={false}
										required
										placeholder="09:00"
										value={schedule.time}
										aria-invalid={Boolean(validationErrors.time) || undefined}
										aria-describedby={validationErrors.time ? `${AUTOMATION_FIELD_IDS.time}-error` : undefined}
										className="ps-9 tabular-nums"
										onChange={(event) => {
											const next = formatTimeDraft(event.target.value);
											patchSchedule({ time: next });
											if (parseTimeValue(next)) clearValidationError("time");
										}}
									/>
									</div>
								</Field>
							) : null}
						{schedule.preset === "custom" ? (
							<div className="grid grid-cols-2 gap-3">
								<div className="col-span-2"><Field label={t("automations.field.frequency")}>
									<AutomationSelect
										label={t("automations.field.frequency")}
										value={schedule.customFrequency}
										onValueChange={(value) => {
											patchSchedule({ customFrequency: value as ScheduleFields["customFrequency"] });
											clearValidationError("monthDay");
										}}
										options={[
											{ value: "daily", label: t("automations.customFrequency.daily") },
											{ value: "weekdays", label: t("automations.customFrequency.weekdays") },
											{ value: "weekly", label: t("automations.customFrequency.weekly") },
											{ value: "monthly", label: t("automations.customFrequency.monthly") },
										]}
									/>
								</Field></div>
								{schedule.customFrequency === "monthly" ? (
									<div className="col-span-2"><Field label={t("automations.field.date")} id={AUTOMATION_FIELD_IDS.monthDay} error={validationErrors.monthDay}>
										<Input
											id={AUTOMATION_FIELD_IDS.monthDay}
											inputMode="numeric"
											autoComplete="off"
											required
											value={schedule.monthDay}
											aria-invalid={Boolean(validationErrors.monthDay) || undefined}
											aria-describedby={validationErrors.monthDay ? `${AUTOMATION_FIELD_IDS.monthDay}-error` : undefined}
											className="tabular-nums"
											onChange={(event) => {
												const next = clampMonthDayDraft(event.target.value);
												patchSchedule({ monthDay: next });
												if (/^[1-9]$|^[12]\d$|^3[01]$/.test(next)) clearValidationError("monthDay");
											}}
										/>
									</Field></div>
								) : schedule.customFrequency === "weekly" ? (
									<div className="col-span-2"><Field label={t("automations.field.weekday")}>
										<AutomationSelect
											label={t("automations.field.weekday")}
											value={schedule.weekday}
											onValueChange={(value) => patchSchedule({ weekday: value as WeekdayCode })}
											options={WEEKDAY_CODES.map((code) => ({
												value: code,
												label: t(`automations.weekday.${code}`),
											}))}
										/>
									</Field></div>
								) : null}
								<Field label={t("automations.field.hour")} id={AUTOMATION_FIELD_IDS.hour} error={validationErrors.hour}>
									<Input
										id={AUTOMATION_FIELD_IDS.hour}
										inputMode="numeric"
										autoComplete="off"
										required
										placeholder="09"
										value={schedule.hour}
										aria-invalid={Boolean(validationErrors.hour) || undefined}
										className="tabular-nums"
										onChange={(event) => {
											const next = clampHourDraft(event.target.value);
											patchSchedule({ hour: next });
											if (parseHourMinute(next, schedule.minute)) {
												clearValidationError("hour");
												clearValidationError("minute");
											}
										}}
									/>
								</Field>
								<Field label={t("automations.field.minute")} id={AUTOMATION_FIELD_IDS.minute} error={validationErrors.minute}>
									<Input
										id={AUTOMATION_FIELD_IDS.minute}
										inputMode="numeric"
										autoComplete="off"
										required
										placeholder="00"
										value={schedule.minute}
										aria-invalid={Boolean(validationErrors.minute) || undefined}
										className="tabular-nums"
										onChange={(event) => {
											const next = clampMinuteDraft(event.target.value);
											patchSchedule({ minute: next });
											if (parseHourMinute(schedule.hour, next)) {
												clearValidationError("hour");
												clearValidationError("minute");
											}
										}}
									/>
								</Field>
							</div>
						) : null}
						<p className={onboardingFieldHintClass}>{t("automations.timezone", { timezone })}</p>
						</section>
						{error ? <p role="alert" className={onboardingFieldErrorClass}>{error}</p> : null}
						{nextRun ? (
							<div className="mt-auto flex flex-col gap-1 rounded-lg border border-border px-4 py-3">
								<span className="text-xs text-muted-foreground">{t("automations.nextRun")}</span>
								<span className="text-sm text-foreground tabular-nums">{displayRelativeTime(nextRun, t, i18n.resolvedLanguage)}</span>
							</div>
						) : null}
					</div>
					</div>

					<div className={cn(onboardingFooterActionsEndClass, "shrink-0 border-t border-border px-4 py-3")}>
						<Button type="button" variant="secondary" disabled={busy} onClick={() => onOpenChange(false)}>
							{t("automations.cancel")}
						</Button>
						<Button type="submit" variant="primary" disabled={busy || !modelValid || awaitingInheritedHarness}>
							{busy ? t(editing ? "automations.saving" : "automations.creating") : t(editing ? "automations.save" : "automations.create")}
						</Button>
					</div>
				</form>
			</DialogContent>
		</Dialog>
	);
}

/** A project's mark in the picker: the Scratchpad's note, as on the new chat page, or a folder. */
function ProjectMark({ projectId, className }: { projectId?: string; className: string }) {
	const Icon = projectId === STANDALONE_WORKSPACE_ID ? StickyNote : Folder;
	return <Icon aria-hidden="true" className={cn("shrink-0", className)} />;
}

function AutomationSelect({
	label,
	value,
	onValueChange,
	options,
	icon: Icon,
}: {
	icon?: AppIcon;
	label: string;
	value: string;
	onValueChange: (value: string) => void;
	options: Array<{ value: string; label: string }>;
}) {
	return (
		<Select value={value} onValueChange={onValueChange}>
			<SelectTrigger size="sm" className="w-full text-control" aria-label={label}>
				<span className="flex min-w-0 items-center gap-2">
					{Icon ? <Icon aria-hidden="true" className="size-icon-sm shrink-0 text-muted-foreground" /> : null}
					<SelectValue />
				</span>
			</SelectTrigger>
			<SelectContent position="popper" side="bottom" align="start" sideOffset={4} className="max-h-64">
				{options.map((option) => (
					<SelectItem key={option.value} value={option.value}>
						{option.label}
					</SelectItem>
				))}
			</SelectContent>
		</Select>
	);
}

function Field({ label, id, error, hideLabel, children }: { label: string; id?: string; error?: string; hideLabel?: boolean; children: React.ReactNode }) {
	const labelClass = hideLabel ? "sr-only" : onboardingFormLabelClass;
	return (
		<div className="flex flex-col gap-2">
			{id ? <label htmlFor={id} className={labelClass}>{label}</label> : <span className={labelClass}>{label}</span>}
			{children}
			{error ? <p id={`${id}-error`} className={onboardingFieldErrorClass}>{error}</p> : null}
		</div>
	);
}
