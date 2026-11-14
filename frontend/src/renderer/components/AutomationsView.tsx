import { useEffect, useMemo, useRef, useState, type FormEvent } from "react";
import { useNavigate } from "@tanstack/react-router";
import { ScrollArea } from "radix-ui";
import { useTranslation } from "react-i18next";
import { ArrowDownUp, Bell, BellRing, CalendarClock, ChevronDown, ChevronRight, Clock, Clock3, Cloud, History, GitPullRequest, ListChecks, FileText, Filter, Pencil, Play, Plus, Trash2, TriangleAlert } from "@/components/app-icons";
import { Button } from "./ui/button";
import { CloseButton } from "./ui/close-button";
import { AnimatePresence, motion, useReducedMotion } from "motion/react";
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
import { AgentModelField } from "./settings/ProjectAgentRoleControls";
import { RequiredAgentField } from "./CreateProjectAgentSheet";
import { useAgentReadinessQuery, type AgentReadinessSnapshot } from "../hooks/useAgentReadinessQuery";
import { useProjectWorkerDefaults } from "../hooks/useProjectDefaultWorker";
import { useWorkspaceQuery } from "../hooks/useWorkspaceQuery";
import {
	buildRankedAgentOptions,
	DEFAULT_AGENT_PRIORITY_RANK,
	isReadyAgent,
} from "../lib/agent-select-options";
import {
	buildRRuleFromSchedule,
	clampMonthDayDraft,
	describeSchedule,
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
	useAutomations,
	useCreateAutomation,
	useDeleteAutomation,
	useUpdateAutomation,
	type Automation,
	type CreateAutomationInput,
} from "../hooks/useAutomations";
import {
	CLOUD_AUTOMATION_HARNESSES,
	cloudAutomationErrorMessage,
	cloudRunErrorMessage,
	useCloudAutomationRuns,
	useCloudAutomations,
	useCreateCloudAutomation,
	useDeleteCloudAutomation,
	useRunCloudAutomationNow,
	useSubscribeCloudAutomation,
	useUpdateCloudAutomation,
	type CloudAutomation,
} from "../hooks/useCloudAutomations";
import { useCloudOffering } from "../hooks/useCloudOffering";
import { useProviderConnections } from "../hooks/useProviderConnections";
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
const TEMPLATE_ICONS = { daily_pr_review_v1: GitPullRequest, daily_issue_triage_v1: ListChecks, weekly_release_notes_v1: FileText };

function displayTime(value?: string, locale?: string) {
	if (!value) return "—";
	return new Intl.DateTimeFormat(locale, { dateStyle: "medium", timeStyle: "short" }).format(new Date(value));
}

function timestamp(value?: string) {
	return value ? Date.parse(value) || 0 : 0;
}

/** One row of the list: an automation of this Mac's daemon, or one that runs in AO Cloud. */
type ListedAutomation = {
	key: string;
	displayName: string;
	enabled: boolean;
	nextRunAt?: string;
	createdAt: string;
	projectId: string;
	local?: Automation;
	cloud?: CloudAutomation;
};

function listLocal(item: Automation): ListedAutomation {
	return { key: `local:${item.id}`, displayName: item.displayName, enabled: item.enabled, nextRunAt: item.nextRunAt, createdAt: item.createdAt, projectId: item.projectId, local: item };
}

function listCloud(item: CloudAutomation): ListedAutomation {
	return { key: `cloud:${item.id}`, displayName: item.displayName, enabled: item.enabled, nextRunAt: item.nextRunAt, createdAt: item.createdAt, projectId: item.projectId, cloud: item };
}

/** Orders the list: soonest next run first with paused ones last, by name, or newest first. */
function sortAutomations(items: ListedAutomation[], order: SortOrder) {
	const byName = (left: ListedAutomation, right: ListedAutomation) => left.displayName.localeCompare(right.displayName);
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
	const cloud = useCloudAutomations();
	const chatHarnesses = useCloudOffering().chatHarnesses;
	const providerConnections = useProviderConnections().data;
	const connectedCloudHarnesses = useMemo(
		() => providerConnections?.filter((connection) => connection.validationState === "valid").map((connection) => connection.provider),
		[providerConnections],
	);
	const createCloud = useCreateCloudAutomation();
	const updateCloud = useUpdateCloudAutomation();
	const removeCloud = useDeleteCloudAutomation();
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
	const [cloudEditTarget, setCloudEditTarget] = useState<CloudAutomation | null>(null);
	const [deleteTarget, setDeleteTarget] = useState<ListedAutomation | null>(null);
	const [expanded, setExpanded] = useState<string | null>(null);
	const [actionError, setActionError] = useState<string | null>(null);
	const cloudItems = cloud.available ? cloud.data : undefined;
	const automations = useMemo(() => [...(query.data ?? []).map(listLocal), ...(cloudItems ?? []).map(listCloud)], [query.data, cloudItems]);
	const projectName = (id: string) => workspaces.find((workspace) => workspace.id === id)?.name ?? id;
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
			<ScrollArea.Root asChild type="hover" scrollHideDelay={500}>
			<main className="relative min-h-0 flex-1 overflow-hidden">
				<ScrollArea.Viewport className="h-full w-full overscroll-contain focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring/60" tabIndex={0}>
				<div className="@container pt-4 pr-4 pb-4 pl-(--space-4_5)">
					{actionError ? <p role="alert" className="mb-3 rounded-md bg-destructive/10 px-3 py-2 text-xs text-destructive">{actionError}</p> : null}
					{query.isLoading ? <p role="status" className="px-3 py-8 text-center text-sm text-muted-foreground">{t("automations.loading")}</p> : null}
					{query.error ? <p role="alert" className="text-sm text-destructive">{query.error.message}</p> : null}
					{cloud.error ? <p role="alert" className="text-sm text-destructive">{cloudAutomationErrorMessage(cloud.error, t)}</p> : null}
					{!query.isLoading && !query.error ? (
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
						<section aria-label={t("automations.title")} className="mt-6 border-t border-border pt-3">
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
								<div className="divide-y divide-border">
									{visibleAutomations.map((entry) => entry.cloud ? (
										<CloudAutomationRow key={entry.key} item={entry.cloud} projectName={projectName(entry.projectId)} busy={updateCloud.isPending} expanded={expanded === entry.key} onExpand={() => setExpanded(expanded === entry.key ? null : entry.key)} onEdit={() => setCloudEditTarget(entry.cloud ?? null)} onDelete={() => setDeleteTarget(entry)} onError={setActionError} onToggle={async (enabled) => { setActionError(null); try { await updateCloud.mutateAsync({ id: entry.cloud!.id, body: { enabled } }); } catch (error) { setActionError(cloudAutomationErrorMessage(error, t) ?? t("automations.updateError")); } }} />
									) : entry.local ? (
										<AutomationRow key={entry.key} item={entry.local} projectName={projectName(entry.projectId)} busy={update.isPending} expanded={expanded === entry.key} onExpand={() => setExpanded(expanded === entry.key ? null : entry.key)} onEdit={() => setEditTarget(entry.local ?? null)} onDelete={() => setDeleteTarget(entry)} onToggle={async (enabled) => { setActionError(null); try { await update.mutateAsync({ id: entry.local!.id, body: { enabled } }); } catch (error) { setActionError(error instanceof Error ? error.message : t("automations.updateError")); } }} />
									) : null)}
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
			</main>
			</ScrollArea.Root>
			<AutomationFormDialog initialTemplateId={starter} open={createOpen} workspaces={workspaces} harnesses={harnesses} cloudOrgId={cloud.available ? cloud.orgId : undefined} cloudHarnesses={chatHarnesses} connectedCloudHarnesses={connectedCloudHarnesses} busy={create.isPending || createCloud.isPending} error={create.error?.message ?? cloudAutomationErrorMessage(createCloud.error, t)} onOpenChange={(open) => { setCreateOpen(open); if (!open) createCloud.reset(); }} onSubmit={async ({ runsOn, ...input }) => {
				if (runsOn === "cloud") {
					// A cloud run is unattended: trusted mode, the default time limit.
					await createCloud.mutateAsync({ projectId: input.projectId!, displayName: input.displayName, prompt: input.prompt, harness: input.harness as CloudAutomation["harness"], timezone: input.timezone!, rrule: input.rrule, mode: "trusted", maxRuntimeMinutes: 60, enabled: true });
				} else {
					await create.mutateAsync(input as CreateAutomationInput);
				}
				setCreateOpen(false);
			}} />
			<AutomationFormDialog open={Boolean(editTarget)} automation={editTarget ?? undefined} workspaces={workspaces} harnesses={harnesses} busy={update.isPending} error={update.error?.message ?? null} onOpenChange={(open) => { if (!open) setEditTarget(null); }} onSubmit={async ({ runsOn: _runsOn, ...input }) => { if (!editTarget) return; await update.mutateAsync({ id: editTarget.id, body: input }); setEditTarget(null); }} />
			<AutomationFormDialog open={Boolean(cloudEditTarget)} cloudAutomation={cloudEditTarget ?? undefined} cloudOrgId={cloud.orgId} cloudHarnesses={chatHarnesses} connectedCloudHarnesses={connectedCloudHarnesses} workspaces={workspaces} harnesses={harnesses} busy={updateCloud.isPending} error={cloudAutomationErrorMessage(updateCloud.error, t)} onOpenChange={(open) => { if (!open) { setCloudEditTarget(null); updateCloud.reset(); } }} onSubmit={async (input) => {
				if (!cloudEditTarget) return;
				await updateCloud.mutateAsync({ id: cloudEditTarget.id, body: { displayName: input.displayName, prompt: input.prompt, ...(input.harness ? { harness: input.harness as CloudAutomation["harness"] } : {}), ...(input.rrule ? { rrule: input.rrule } : {}) } });
				setCloudEditTarget(null);
			}} />
			<ConfirmDialog open={Boolean(deleteTarget)} title={t("automations.delete.title")} description={t("automations.delete.description", { name: deleteTarget?.displayName })} confirmLabel={t("automations.delete.confirm")} destructive busy={remove.isPending || removeCloud.isPending} error={deleteTarget?.cloud ? cloudAutomationErrorMessage(removeCloud.error, t) : remove.error?.message ?? null} onOpenChange={(open) => { if (!open) { setDeleteTarget(null); removeCloud.reset(); } }} onConfirm={() => {
				if (!deleteTarget) return;
				if (deleteTarget.cloud) removeCloud.mutate(deleteTarget.cloud.id, { onSuccess: () => setDeleteTarget(null) });
				else if (deleteTarget.local) remove.mutate(deleteTarget.local.id, { onSuccess: () => setDeleteTarget(null) });
			}} />
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

function AutomationRow({ item, projectName, busy, expanded, onExpand, onEdit, onDelete, onToggle }: { item: Automation; projectName: string; busy: boolean; expanded: boolean; onExpand: () => void; onEdit: () => void; onDelete: () => void; onToggle: (enabled: boolean) => Promise<void> }) {
	const runs = useAutomationRuns(expanded ? item.id : null);
	const navigate = useNavigate();
	const { t, i18n } = useTranslation();
	const reduceMotion = useReducedMotion();
	const scheduleLabel = useScheduleLabel();
	const historyId = `automation-history-${item.id}`;
	return <article className="group">
		<div className={cn("flex items-center gap-3 rounded-md px-2 py-2.5", expanded ? "bg-interactive-hover" : "hover:bg-interactive-hover")}>
			<Pressable className="flex min-w-0 flex-1 items-center gap-2.5 rounded-sm" aria-label={t(expanded ? "automations.runs.hide" : "automations.runs.show", { name: item.displayName })} aria-expanded={expanded} aria-controls={historyId} onClick={onExpand}>
				<ChevronRight aria-hidden="true" className={cn("size-3.5 shrink-0 text-muted-foreground transition-transform duration-150 motion-reduce:transition-none", expanded && "rotate-90")} />
				<div className="min-w-0">
					<h2 className="truncate text-control font-medium leading-5 text-foreground">{item.displayName}</h2>
					<p className="flex min-w-0 items-center gap-1.5 text-caption leading-5 text-muted-foreground">
						<Clock aria-hidden="true" className="size-icon-xs shrink-0 text-passive" />
						<span className="truncate">{scheduleLabel(item.rrule)}</span>
						<span aria-hidden="true" className="text-passive">·</span>
						<span className="truncate">{projectName}</span>
					</p>
				</div>
			</Pressable>
			<div className="hidden shrink-0 text-end text-caption leading-5 @lg:block">
				<p className={cn("tabular-nums", item.enabled ? "text-foreground" : "text-muted-foreground")}>
					{item.enabled ? t("automations.nextRunAt", { time: displayTime(item.nextRunAt, i18n.resolvedLanguage) }) : t("automations.paused")}
				</p>
				{item.latestRun ? <p className="text-muted-foreground">{t("automations.lastRun", { status: t(`automations.runStatus.${item.latestRun.status}`, { defaultValue: item.latestRun.status }) })}</p> : null}
			</div>
			<div className="flex shrink-0 items-center gap-1">
				<Button variant="quiet" size="icon-sm" className="opacity-0 group-hover:opacity-100 group-focus-within:opacity-100" aria-label={t("automations.edit.aria", { name: item.displayName })} onClick={onEdit}><Pencil className="size-3.5" /></Button>
				<Button variant="destructive-ghost" size="icon-sm" className="opacity-0 group-hover:opacity-100 group-focus-within:opacity-100" aria-label={t("automations.delete.aria", { name: item.displayName })} onClick={onDelete}><Trash2 className="size-3.5" /></Button>
				<Switch checked={item.enabled} disabled={busy} aria-label={t(item.enabled ? "automations.disable" : "automations.enable", { name: item.displayName })} onCheckedChange={(checked) => void onToggle(checked)} />
			</div>
		</div>
		{item.latestRun?.errorMessage ? <p role="alert" className="mx-2 mb-2 rounded-md bg-destructive/10 px-3 py-2 text-xs text-destructive">{item.latestRun.errorMessage}</p> : null}
		<AnimatePresence initial={false}>
			{expanded ? <motion.div id={historyId} initial={{ height: 0, opacity: 0 }} animate={{ height: "auto", opacity: 1 }} exit={{ height: 0, opacity: 0 }} transition={{ duration: reduceMotion ? 0 : 0.18, ease: "easeOut" }} className="overflow-hidden">
				<div className="px-2 pt-3 pb-4 pl-8">
					<div className="mb-4 flex flex-wrap items-center gap-x-4 gap-y-1 text-caption text-muted-foreground">
						<span className="@lg:hidden tabular-nums">{item.enabled ? t("automations.nextRunAt", { time: displayTime(item.nextRunAt, i18n.resolvedLanguage) }) : t("automations.paused")}</span>
						<span>{t("automations.timezone", { timezone: item.timezone })}</span>
						<span>{t("automations.agent")}: <span className="text-foreground">{item.harness || t("automations.projectDefault")}</span></span>
					</div>
					<p className="mb-1 text-caption font-medium text-muted-foreground">{t("automations.field.prompt")}</p>
					<p className="mb-4 whitespace-pre-wrap text-xs leading-5 text-foreground">{item.prompt}</p>
					<h3 className="mb-2 flex items-center gap-1.5 text-xs font-medium"><History className="size-3.5 text-muted-foreground" aria-hidden="true" />{t("automations.runs.title")}</h3>
					{runs.isLoading ? <p role="status" className="text-xs text-muted-foreground">{t("automations.runs.loading")}</p> : runs.error ? <p role="alert" className="text-xs text-destructive">{runs.error.message}</p> : runs.data?.length ? <div className="divide-y divide-border">{runs.data.map((run) => <div key={run.id} className="flex items-center justify-between gap-3 py-2 text-xs"><div><span className="font-medium">{t(`automations.runStatus.${run.status}`, { defaultValue: run.status })}</span><span className="ml-3 text-caption tabular-nums text-muted-foreground">{displayTime(run.scheduledFor, i18n.resolvedLanguage)}</span>{run.errorMessage ? <p className="mt-1 text-xs text-destructive">{run.errorMessage}</p> : null}</div>{run.sessionId ? <Button variant="secondary" size="sm" onClick={() => void navigate({ to: "/sessions/$sessionId", params: { sessionId: run.sessionId! } })}>{t("automations.runs.openSession")}</Button> : null}</div>)}</div> : <p className="flex items-center gap-2 py-3 text-xs text-muted-foreground"><Clock3 className="size-3.5" aria-hidden="true" />{t("automations.runs.empty")}</p>}
				</div>
			</motion.div> : null}
		</AnimatePresence>
	</article>;
}


/**
 * An automation that runs in AO Cloud: the whole organization sees it and may
 * run it now; only the person it runs as, its creator and admins change it.
 */
function CloudAutomationRow({ item, projectName, busy, expanded, onExpand, onEdit, onDelete, onToggle, onError }: { item: CloudAutomation; projectName: string; busy: boolean; expanded: boolean; onExpand: () => void; onEdit: () => void; onDelete: () => void; onToggle: (enabled: boolean) => Promise<void>; onError: (message: string | null) => void }) {
	const runs = useCloudAutomationRuns(expanded ? item.id : null);
	const runNow = useRunCloudAutomationNow();
	const subscribe = useSubscribeCloudAutomation();
	const navigate = useNavigate();
	const { t, i18n } = useTranslation();
	const reduceMotion = useReducedMotion();
	const scheduleLabel = useScheduleLabel();
	const historyId = `automation-history-cloud-${item.id}`;
	const runAs = item.runAs.displayName || item.runAs.email || t("automations.cloud.runAsUnknown");
	const latestError = item.latestRun ? cloudRunErrorMessage(item.latestRun, t) : undefined;
	const running = item.latestRun ? ["pending", "starting", "running"].includes(item.latestRun.status) : false;
	async function act(action: () => Promise<unknown>) {
		onError(null);
		try { await action(); } catch (error) { onError(cloudAutomationErrorMessage(error, t)); }
	}
	const subscribeLabel = t(item.subscribed ? "automations.cloud.unsubscribe" : "automations.cloud.subscribe", { name: item.displayName });
	return <article className="group">
		<div className={cn("flex items-center gap-3 rounded-md px-2 py-2.5", expanded ? "bg-interactive-hover" : "hover:bg-interactive-hover")}>
			<Pressable className="flex min-w-0 flex-1 items-center gap-2.5 rounded-sm" aria-label={t(expanded ? "automations.runs.hide" : "automations.runs.show", { name: item.displayName })} aria-expanded={expanded} aria-controls={historyId} onClick={onExpand}>
				<ChevronRight aria-hidden="true" className={cn("size-3.5 shrink-0 text-muted-foreground transition-transform duration-150 motion-reduce:transition-none", expanded && "rotate-90")} />
				<div className="min-w-0">
					<h2 className="flex min-w-0 items-center gap-1.5 text-control font-medium leading-5 text-foreground">
						<span className="truncate">{item.displayName}</span>
						<span className="flex shrink-0 items-center gap-1 text-caption font-normal text-muted-foreground"><Cloud aria-hidden="true" className="size-icon-xs" />{t("automations.cloud.badge")}</span>
					</h2>
					<p className="flex min-w-0 items-center gap-1.5 text-caption leading-5 text-muted-foreground">
						<Clock aria-hidden="true" className="size-icon-xs shrink-0 text-passive" />
						<span className="truncate">{scheduleLabel(item.rrule)}</span>
						<span aria-hidden="true" className="text-passive">·</span>
						<span className="truncate">{projectName}</span>
						<span aria-hidden="true" className="text-passive">·</span>
						<span className="truncate">{t("automations.cloud.runsAs", { name: runAs })}</span>
					</p>
				</div>
			</Pressable>
			<div className="hidden shrink-0 text-end text-caption leading-5 @lg:block">
				<p className={cn("tabular-nums", item.enabled ? "text-foreground" : "text-muted-foreground")}>
					{item.enabled ? t("automations.nextRunAt", { time: displayTime(item.nextRunAt, i18n.resolvedLanguage) }) : item.disabledReason || t("automations.paused")}
				</p>
				{item.latestRun ? <p className="text-muted-foreground">{t("automations.lastRun", { status: t(`automations.runStatus.${item.latestRun.status}`, { defaultValue: item.latestRun.status }) })}</p> : null}
			</div>
			<div className="flex shrink-0 items-center gap-1">
				<Tooltip>
					<TooltipTrigger asChild>
						<Button variant="quiet" size="icon-sm" aria-label={t("automations.cloud.runNow.aria", { name: item.displayName })} disabled={running || runNow.isPending} onClick={() => void act(() => runNow.mutateAsync(item.id))}><Play className="size-3.5" /></Button>
					</TooltipTrigger>
					<TooltipContent side="bottom">{t(running ? "automations.cloud.runNow.running" : "automations.cloud.runNow")}</TooltipContent>
				</Tooltip>
				<Tooltip>
					<TooltipTrigger asChild>
						<Button variant="quiet" size="icon-sm" aria-label={subscribeLabel} aria-pressed={item.subscribed} disabled={subscribe.isPending} onClick={() => void act(() => subscribe.mutateAsync({ id: item.id, subscribed: !item.subscribed }))}>{item.subscribed ? <BellRing className="size-3.5" /> : <Bell className="size-3.5" />}</Button>
					</TooltipTrigger>
					<TooltipContent side="bottom">{t(item.subscribed ? "automations.cloud.unsubscribe.tooltip" : "automations.cloud.subscribe.tooltip")}</TooltipContent>
				</Tooltip>
				{item.canEdit ? <>
					<Button variant="quiet" size="icon-sm" className="opacity-0 group-hover:opacity-100 group-focus-within:opacity-100" aria-label={t("automations.edit.aria", { name: item.displayName })} onClick={onEdit}><Pencil className="size-3.5" /></Button>
					<Button variant="destructive-ghost" size="icon-sm" className="opacity-0 group-hover:opacity-100 group-focus-within:opacity-100" aria-label={t("automations.delete.aria", { name: item.displayName })} onClick={onDelete}><Trash2 className="size-3.5" /></Button>
				</> : null}
				<Switch checked={item.enabled} disabled={busy || !item.canEdit} aria-label={t(item.enabled ? "automations.disable" : "automations.enable", { name: item.displayName })} onCheckedChange={(checked) => void onToggle(checked)} />
			</div>
		</div>
		{latestError ? <p role="alert" className="mx-2 mb-2 rounded-md bg-destructive/10 px-3 py-2 text-xs text-destructive">{latestError}</p> : null}
		<AnimatePresence initial={false}>
			{expanded ? <motion.div id={historyId} initial={{ height: 0, opacity: 0 }} animate={{ height: "auto", opacity: 1 }} exit={{ height: 0, opacity: 0 }} transition={{ duration: reduceMotion ? 0 : 0.18, ease: "easeOut" }} className="overflow-hidden">
				<div className="px-2 pt-3 pb-4 pl-8">
					<div className="mb-4 flex flex-wrap items-center gap-x-4 gap-y-1 text-caption text-muted-foreground">
						<span className="@lg:hidden tabular-nums">{item.enabled ? t("automations.nextRunAt", { time: displayTime(item.nextRunAt, i18n.resolvedLanguage) }) : t("automations.paused")}</span>
						<span>{t("automations.timezone", { timezone: item.timezone })}</span>
						<span>{t("automations.agent")}: <span className="text-foreground">{item.harness}</span></span>
						<span>{t("automations.cloud.branch")}: <span className="font-mono text-foreground">{item.branch}</span></span>
					</div>
					<p className="mb-1 text-caption font-medium text-muted-foreground">{t("automations.field.prompt")}</p>
					<p className="mb-4 whitespace-pre-wrap text-xs leading-5 text-foreground">{item.prompt}</p>
					<h3 className="mb-2 flex items-center gap-1.5 text-xs font-medium"><History className="size-3.5 text-muted-foreground" aria-hidden="true" />{t("automations.runs.title")}</h3>
					{runs.isLoading ? <p role="status" className="text-xs text-muted-foreground">{t("automations.runs.loading")}</p> : runs.error ? <p role="alert" className="text-xs text-destructive">{cloudAutomationErrorMessage(runs.error, t)}</p> : runs.data?.length ? <div className="divide-y divide-border">{runs.data.map((run) => {
						const runError = cloudRunErrorMessage(run, t);
						return <div key={run.id} className="flex items-center justify-between gap-3 py-2 text-xs"><div><span className="font-medium">{t(`automations.runStatus.${run.status}`, { defaultValue: run.status })}</span><span className="ml-3 text-caption tabular-nums text-muted-foreground">{displayTime(run.scheduledFor, i18n.resolvedLanguage)}</span>{run.trigger === "manual" ? <span className="ml-3 text-caption text-muted-foreground">{t("automations.cloud.manual")}</span> : null}{runError ? <p className="mt-1 text-xs text-destructive">{runError}</p> : null}</div>{run.sessionId ? <Button variant="secondary" size="sm" onClick={() => void navigate({ to: "/sessions/$sessionId", params: { sessionId: run.sessionId! } })}>{t("automations.runs.openSession")}</Button> : null}</div>;
					})}</div> : <p className="flex items-center gap-2 py-3 text-xs text-muted-foreground"><Clock3 className="size-3.5" aria-hidden="true" />{t("automations.runs.empty")}</p>}
				</div>
			</motion.div> : null}
		</AnimatePresence>
	</article>;
}

type WorkspaceOption = { id: string; name: string; standaloneAgent?: boolean; kind?: string; cloudOrgId?: string; shared?: unknown };
type RunsOn = "local" | "cloud";
type AutomationFormSubmit = {
	/** Where the automation runs: this Mac's daemon, or AO Cloud. */
	runsOn: RunsOn;
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
	/** A cloud automation being edited. */
	cloudAutomation?: CloudAutomation;
	/** The organization whose cloud projects may run automations; unset when AO Cloud automations are not offered. */
	cloudOrgId?: string;
	/** Harnesses AO Cloud runs in Chat (`/me` chatHarnesses). */
	cloudHarnesses?: string[];
	/** Harnesses the user has a valid AO Cloud login for; unset while loading. */
	connectedCloudHarnesses?: string[];
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
	// Daily and weekly times are picked with the hour and minute fields.
	time: "automation-hour",
	hour: "automation-hour",
	minute: "automation-minute",
	monthDay: "automation-month-day",
};

function AutomationFormDialog({
	open,
	initialTemplateId = "custom",
	automation,
	cloudAutomation,
	cloudOrgId,
	cloudHarnesses,
	connectedCloudHarnesses,
	workspaces,
	harnesses,
	busy,
	error,
	onOpenChange,
	onSubmit,
}: AutomationFormDialogProps) {
	const { t } = useTranslation();
	const edited = automation ?? cloudAutomation;
	const editing = Boolean(edited);
	const timezone = edited?.timezone || Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC";
	const [runsOn, setRunsOn] = useState<RunsOn>("local");
	const isCloud = runsOn === "cloud";
	const [projectId, setProjectId] = useState("");
	const projectDefaults = useProjectWorkerDefaults(open && !isCloud ? (automation?.projectId || projectId) : "");
	const projectDefaultWorker = projectDefaults.harness;
	const [name, setName] = useState("");
	const [prompt, setPrompt] = useState("");
	const templateId = automation?.templateId ?? (cloudAutomation ? "custom" : initialTemplateId);
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

	useEffect(() => {
		if (!open) return;
		if (!edited) void captureRendererEvent("ao.automations.create_opened");
		setRunsOn(cloudAutomation ? "cloud" : "local");
		setProjectId(edited?.projectId ?? "");
		setName(edited?.displayName ?? "");
		setPrompt(edited?.prompt ?? "");
		setHarness(edited?.harness ?? "");
		setModel(automation?.model ?? "");
		setMode(automation?.mode ?? "");
		setEffort(automation?.effort ?? "");
		setModelValid(true);
		const defaultTime = nowLocalHHMM();
		const [defaultHour, defaultMinute] = defaultTime.split(":");
		const nextSchedule = edited
			? scheduleFieldsFromRRule(edited.rrule)
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
		const starter = !edited ? AUTOMATION_TEMPLATES.find((item) => item.id === initialTemplateId) : undefined;
		if (starter) {
			setName(t(`automations.template.${starter.id}`));
			setPrompt(starter.prompt);
		}
		setInitialSchedule(nextSchedule);
		setSchedule(starter ? scheduleFieldsFromRRule(starter.rrule) : nextSchedule);
		setValidationErrors({});
	// Language changes must not discard edits to an open form.
	// eslint-disable-next-line react-hooks/exhaustive-deps
	}, [open, automation, cloudAutomation, initialTemplateId]);

	// A Cloud standalone agent takes no session but its own, so it is no project for an automation.
	// With AO Cloud automations offered, This Mac lists local projects and AO Cloud the organization's own cloud projects.
	const projectOptions = workspaces
		.filter((item) => !item.standaloneAgent && (isCloud
			? item.kind === "cloud" && item.cloudOrgId === cloudOrgId && !item.shared
			: !cloudOrgId || item.kind !== "cloud"))
		.map((item) => ({ value: item.id, label: item.name }));
	if (edited && !projectOptions.some((option) => option.value === edited.projectId)) {
		projectOptions.unshift({ value: edited.projectId, label: edited.projectId });
	}
	const cloudHarnessOptions = CLOUD_AUTOMATION_HARNESSES
		.filter((id) => !cloudHarnesses?.length || cloudHarnesses.includes(id))
		// A run uses the login of the person it runs as, so offer only harnesses logged in to AO Cloud.
		.filter((id) => !connectedCloudHarnesses || connectedCloudHarnesses.includes(id) || id === cloudAutomation?.harness)
		.map((id) => ({ value: id, label: harnesses.find((agent) => agent.id === id)?.label ?? id }));
	const cloudHarness = cloudHarnessOptions.some((option) => option.value === harness) ? harness : cloudHarnessOptions[0]?.value ?? "";
	// Prefer an explicit choice, then the project's resolved worker, then the
	// first ready harness so Model isn't stuck on "Select agent" before a
	// project is picked.
	const fallbackHarness =
		buildRankedAgentOptions({
			agents: harnesses,
			priorityRank: DEFAULT_AGENT_PRIORITY_RANK,
			fallbackAgents: [],
		}).find(isReadyAgent)?.id ?? "";
	const selectedHarness = harness || projectDefaultWorker || fallbackHarness;
	const awaitingInheritedHarness = Boolean(!isCloud && automation && !automation.harness && !harness && !projectDefaults.loaded);
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
		if (!isCloud && (!modelValid || awaitingInheritedHarness)) return;
		const nextErrors: AutomationValidationErrors = {};
		if (!editing && !projectId) nextErrors.projectId = t("automations.validation.project");
		if (!name.trim()) nextErrors.name = t("automations.validation.name");
		if (!prompt.trim()) nextErrors.prompt = t("automations.validation.prompt");
		else if (new TextEncoder().encode(prompt.trim()).length > 4096) nextErrors.prompt = t("automations.validation.promptBytes");
		if (schedule.preset === "daily" || schedule.preset === "weekly") {
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
			document.getElementById(AUTOMATION_FIELD_IDS[firstInvalid])?.focus();
			return;
		}
		const nextRRule = buildRRuleFromSchedule(schedule);
		const scheduleChanged = !(editing && initialSchedule && schedulesEqual(schedule, initialSchedule));
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
					schedule_preset: schedule.preset,
					prompt_length_bucket: automationPromptLengthBucket(prompt.trim()),
				};
		try {
			if (isCloud) {
				// A cloud automation runs the harness's default model in trusted mode.
				await onSubmit({
					runsOn: "cloud",
					...(editing ? {} : { projectId, timezone }),
					displayName: name,
					prompt,
					...(!editing || cloudHarness !== (cloudAutomation?.harness ?? "") ? { harness: cloudHarness } : {}),
					...(scheduleChanged ? { rrule: nextRRule } : {}),
				});
				if (funnel) void captureRendererEvent("ao.renderer.automation_create_succeeded", funnel);
				return;
			}
			await onSubmit({
				runsOn: "local",
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
			<DialogContent showCloseButton={false} className={centeredOnboardingDialogClass}>
				<DialogClose asChild>
					<CloseButton floating disabled={busy} aria-label={t("automations.create.close")} />
				</DialogClose>

				<DialogTitle className="settings-dialog-title px-4 pr-12 pt-3">
					{t(editing ? "automations.edit" : "automations.create")}
				</DialogTitle>
				<DialogDescription className="px-4 pr-12 pt-1 text-[13px] leading-5 text-muted-foreground">
					{t(editing ? "automations.edit.description" : "automations.create.description")}
				</DialogDescription>
				<form className="flex min-h-0 flex-1 flex-col" noValidate onSubmit={(event) => void submit(event)}>
					<div className="flex min-h-0 flex-1 flex-col gap-4 overflow-y-auto px-4 pb-1 pt-4">
						{Object.keys(validationErrors).length > 0 ? (
							<div role="alert" className={cn(onboardingAlertErrorClass, "flex items-start gap-2")}>
								<TriangleAlert className="mt-0.5 size-4 shrink-0" aria-hidden="true" />
								<span>{t("automations.validation.summary")}</span>
							</div>
						) : null}
						<Field label={t("automations.field.name")} id={AUTOMATION_FIELD_IDS.name} error={validationErrors.name}>
							<Input id={AUTOMATION_FIELD_IDS.name} required maxLength={120} value={name} aria-invalid={Boolean(validationErrors.name) || undefined} aria-describedby={validationErrors.name ? `${AUTOMATION_FIELD_IDS.name}-error` : undefined} onChange={(event) => { setName(event.target.value); if (event.target.value.trim()) clearValidationError("name"); }} />
						</Field>
						{cloudOrgId && !editing ? (
							<Field label={t("automations.field.runsOn")}>
								<AutomationSelect
									label={t("automations.field.runsOn")}
									value={runsOn}
									disabled={busy}
									onValueChange={(value) => { setRunsOn(value as RunsOn); setProjectId(""); setHarness(""); setModel(""); setMode(""); setEffort(""); setModelValid(true); clearValidationError("projectId"); }}
									options={[
										{ value: "local", label: t("automations.runsOn.local") },
										{ value: "cloud", label: t("automations.runsOn.cloud") },
									]}
								/>
								{isCloud ? <p className={onboardingFieldHintClass}>{t("automations.runsOn.cloudHint")}</p> : null}
							</Field>
						) : null}
						<Field label={t("automations.field.project")} id={AUTOMATION_FIELD_IDS.projectId} error={validationErrors.projectId}>
							<AutomationSelect
								id={AUTOMATION_FIELD_IDS.projectId}
								label={t("automations.field.project")}
								placeholder={t("automations.projectPlaceholder")}
								required
								disabled={editing}
								invalid={Boolean(validationErrors.projectId)}
								describedBy={validationErrors.projectId ? `${AUTOMATION_FIELD_IDS.projectId}-error` : undefined}
								value={projectId}
								onValueChange={(value) => { setProjectId(value); setModel(""); setMode(""); setEffort(""); setModelValid(true); clearValidationError("projectId"); }}
								options={projectOptions}
							/>
						</Field>

						<Field label={t("automations.field.prompt")} id={AUTOMATION_FIELD_IDS.prompt} error={validationErrors.prompt}>
							<textarea
								id={AUTOMATION_FIELD_IDS.prompt}
								required
								maxLength={4096}
								value={prompt}
								aria-invalid={Boolean(validationErrors.prompt) || undefined}
								aria-describedby={validationErrors.prompt ? `${AUTOMATION_FIELD_IDS.prompt}-error` : undefined}
								onChange={(event) => { setPrompt(event.target.value); if (event.target.value.trim()) clearValidationError("prompt"); }}
								className="min-h-28 w-full resize-y rounded-md border border-transparent bg-input/50 px-3 py-2 text-[13px] leading-5 outline-none focus-visible:border-ring focus-visible:ring-2 focus-visible:ring-ring/30 aria-invalid:border-destructive"
							/>
						</Field>
						<div className="grid grid-cols-2 gap-3">
							{isCloud ? (
								<Field label={t("automations.agent")} id="automation-agent">
									<AutomationSelect
										id="automation-agent"
										label={t("automations.agent")}
										value={cloudHarness}
										disabled={busy}
										onValueChange={setHarness}
										options={cloudHarnessOptions}
										placeholder={t("automations.cloud.noConnectedAgent")}
									/>
									{cloudHarnessOptions.length === 0 ? <p className={onboardingFieldHintClass}>{t("automations.cloud.connectAgentHint")}</p> : null}
								</Field>
							) : <RequiredAgentField
								id="automation-agent"
								label={t("automations.agent")}
								labelClassName={onboardingFormLabelClass}
								placeholder={t("automations.agentPlaceholder")}
								value={selectedHarness}
								agents={harnesses}
								disabled={busy}
								onChange={(value) => {
									setHarness(value);
									setModel(""); setMode(""); setEffort(""); setModelValid(true);
								}}
							/>}
							{open && !isCloud ? (
								<div className="flex min-w-0 flex-col justify-end gap-2" inert={busy || awaitingInheritedHarness} aria-busy={awaitingInheritedHarness}>
									<AgentModelField
										key={selectedHarness + ":" + projectId}
										role="worker"
										variant="task"
										agentId={selectedHarness}
										projectId={projectId}
										model={displayedModel}
										mode={displayedMode}
										effort={displayedEffort}
										onModelChange={setModel}
										onModeChange={setMode}
										onEffortChange={setEffort}
										onValidityChange={setModelValid}
									/>
								</div>
							) : null}
							<Field label={t("automations.field.schedule")}>
								<AutomationSelect
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
										{ value: "weekly", label: t("automations.schedule.weekly") },
										{ value: "custom", label: t("automations.schedule.custom") },
									]}
								/>
							</Field>
							{schedule.preset === "weekly" ? (
								<Field label={t("automations.field.weekday")}>
									<AutomationSelect
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
							{schedule.preset === "daily" || schedule.preset === "weekly" ? (
								<TimePickerFields
									hour={schedule.time.split(":")[0] ?? ""}
									minute={schedule.time.split(":")[1] ?? ""}
									hourError={validationErrors.time}
									onChange={(hour, minute) => { patchSchedule({ time: `${hour}:${minute}` }); clearValidationError("time"); }}
								/>
							) : null}
						</div>
						{schedule.preset === "custom" ? (
							<div className="grid grid-cols-2 gap-3">
								<Field label={t("automations.field.frequency")}>
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
								</Field>
								{schedule.customFrequency === "monthly" ? (
									<Field label={t("automations.field.date")} id={AUTOMATION_FIELD_IDS.monthDay} error={validationErrors.monthDay}>
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
									</Field>
								) : schedule.customFrequency === "weekly" ? (
									<Field label={t("automations.field.weekday")}>
										<AutomationSelect
											label={t("automations.field.weekday")}
											value={schedule.weekday}
											onValueChange={(value) => patchSchedule({ weekday: value as WeekdayCode })}
											options={WEEKDAY_CODES.map((code) => ({
												value: code,
												label: t(`automations.weekday.${code}`),
											}))}
										/>
									</Field>
								) : (
									<div aria-hidden="true" />
								)}
								<TimePickerFields
									hour={schedule.hour}
									minute={schedule.minute}
									hourError={validationErrors.hour}
									minuteError={validationErrors.minute}
									onChange={(hour, minute) => { patchSchedule({ hour, minute }); clearValidationError("hour"); clearValidationError("minute"); }}
								/>
							</div>
						) : null}
						<p className={onboardingFieldHintClass}>{t("automations.timezone", { timezone })}</p>
						{templateId !== "custom" ? <p className="text-xs leading-5 text-muted-foreground">{t("automations.templates.setup")}</p> : null}
						{error ? <p role="alert" className={onboardingFieldErrorClass}>{error}</p> : null}
					</div>

					<div className={cn(onboardingFooterActionsEndClass, "px-4 pb-4")}>
						<Button type="button" variant="secondary" disabled={busy} onClick={() => onOpenChange(false)}>
							{t("automations.cancel")}
						</Button>
						<Button type="submit" variant="primary" disabled={busy || (!isCloud && (!modelValid || awaitingInheritedHarness))}>
							{busy ? t(editing ? "automations.saving" : "automations.creating") : t(editing ? "automations.save" : "automations.create")}
						</Button>
					</div>
				</form>
			</DialogContent>
		</Dialog>
	);
}

const HOUR_OPTIONS = Array.from({ length: 24 }, (_, value) => String(value).padStart(2, "0")).map((value) => ({ value, label: value }));
const MINUTE_OPTIONS = Array.from({ length: 60 }, (_, value) => String(value).padStart(2, "0")).map((value) => ({ value, label: value }));

/** Hour (00-23) and minute (00-59) pickers; values are two-digit strings. */
function TimePickerFields({ hour, minute, hourError, minuteError, onChange }: { hour: string; minute: string; hourError?: string; minuteError?: string; onChange: (hour: string, minute: string) => void }) {
	const { t } = useTranslation();
	const pad = (value: string) => (/^\d{1,2}$/.test(value.trim()) ? value.trim().padStart(2, "0") : "");
	const hh = pad(hour);
	const mm = pad(minute);
	return (
		<>
			<Field label={t("automations.field.hour")} id={AUTOMATION_FIELD_IDS.hour} error={hourError}>
				<AutomationSelect id={AUTOMATION_FIELD_IDS.hour} label={t("automations.field.hour")} placeholder="09" value={hh} invalid={Boolean(hourError)} onValueChange={(value) => onChange(value, mm || "00")} options={HOUR_OPTIONS} />
			</Field>
			<Field label={t("automations.field.minute")} id={AUTOMATION_FIELD_IDS.minute} error={minuteError}>
				<AutomationSelect id={AUTOMATION_FIELD_IDS.minute} label={t("automations.field.minute")} placeholder="00" value={mm} invalid={Boolean(minuteError)} onValueChange={(value) => onChange(hh || "00", value)} options={MINUTE_OPTIONS} />
			</Field>
		</>
	);
}

function AutomationSelect({
	id,
	label,
	value,
	onValueChange,
	options,
	placeholder,
	required,
	disabled,
	invalid,
	describedBy,
}: {
	id?: string;
	label: string;
	value: string;
	onValueChange: (value: string) => void;
	options: Array<{ value: string; label: string; disabled?: boolean }>;
	placeholder?: string;
	required?: boolean;
	disabled?: boolean;
	invalid?: boolean;
	describedBy?: string;
}) {
	return (
		<Select value={value} onValueChange={onValueChange} required={required} disabled={disabled}>
			<SelectTrigger id={id} size="sm" className="w-full text-control" aria-label={label} aria-invalid={invalid || undefined} aria-describedby={describedBy}>
				<SelectValue placeholder={placeholder} />
			</SelectTrigger>
			<SelectContent position="popper" side="bottom" align="start" sideOffset={4} className="max-h-64">
				{options.map((option) => (
					<SelectItem key={option.value} value={option.value} disabled={option.disabled}>
						{option.label}
					</SelectItem>
				))}
			</SelectContent>
		</Select>
	);
}

function Field({ label, id, error, children }: { label: string; id?: string; error?: string; children: React.ReactNode }) {
	return (
		<div className="flex flex-col gap-2">
			{id ? <label htmlFor={id} className={onboardingFormLabelClass}>{label}</label> : <span className={onboardingFormLabelClass}>{label}</span>}
			{children}
			{error ? <p id={`${id}-error`} className={onboardingFieldErrorClass}>{error}</p> : null}
		</div>
	);
}
