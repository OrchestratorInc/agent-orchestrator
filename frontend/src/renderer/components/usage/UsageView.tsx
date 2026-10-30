import { ChevronLeft, ChevronRight, Gauge, RefreshCw } from "lucide-react";
import { ScrollArea } from "radix-ui";
import { useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { useAgentAccountActions, useAgentAccounts } from "../../hooks/useAgentAccounts";
import { useUsageLimitWindows, useUsageReport } from "../../hooks/useUsageReport";
import { useWorkspaceQuery } from "../../hooks/useWorkspaceQuery";
import { hidesShellTopbar } from "../../lib/platform";
import { formatRange } from "../../lib/usage-format";
import type { UsageUnit } from "../../lib/usage-report";
import { cn } from "../../lib/utils";
import { CLOUD_PROJECT_KIND, STANDALONE_WORKSPACE_ID } from "../../types/workspace";
import { HARNESS_TAB_CLASS, HARNESS_TABS_CLASS } from "../settings/harness/harness-ui";
import { SettingsOptionMenu } from "../settings/SettingsOptionMenu";
import { topbarProjectLabelClass } from "../TopbarButton";
import { Badge } from "../ui/badge";
import { Button } from "../ui/button";
import { Tabs, TabsList, TabsTrigger } from "../ui/tabs";
import { Tooltip, TooltipContent, TooltipTrigger } from "../ui/tooltip";
import { UsageLimits } from "./UsageLimits";
import { UsageSpend } from "./UsageSpend";
import { UsageWindows } from "./UsageWindows";

type UsageTab = "spend" | "limits" | "windows";
const ALL_PROJECTS = "all";
const PERIODS = [
	{ days: 1, label: "usage.period.day" },
	{ days: 7, label: "usage.period.week" },
	{ days: 30, label: "usage.period.month" },
	{ days: 90, label: "usage.period.quarter" },
] as const;

/** Re-renders on an interval so reset countdowns stay current. */
function useNow(intervalMs: number): Date {
	const [now, setNow] = useState(() => new Date());
	useEffect(() => {
		const id = window.setInterval(() => setNow(new Date()), intervalMs);
		return () => window.clearInterval(id);
	}, [intervalMs]);
	return now;
}

/** Estimated cost, token use and subscription limits for AO's agent sessions. */
export function UsageView() {
	const { t, i18n } = useTranslation();
	const showPageTitle = hidesShellTopbar();
	const [tab, setTab] = useState<UsageTab>("spend");
	const [unit, setUnit] = useState<UsageUnit>("cost");
	const [days, setDays] = useState(7);
	// Whole periods stepped back from now; 0 is the current one.
	const [offset, setOffset] = useState(0);
	const [projectFilter, setProjectFilter] = useState(ALL_PROJECTS);
	const [refreshingLimits, setRefreshingLimits] = useState(false);
	const now = useNow(30_000);

	const workspaces = useWorkspaceQuery({ includeCloud: false }).data;
	const projects = useMemo(
		() => (workspaces ?? []).filter((workspace) => !workspace.hostId && workspace.kind !== CLOUD_PROJECT_KIND),
		[workspaces],
	);
	const projectNames = useMemo(
		() => new Map(projects.map((project) => [project.id, project.id === STANDALONE_WORKSPACE_ID ? t("standalone.workspaceName") : project.name])),
		[projects, t],
	);
	const report = useUsageReport(days, offset, projectFilter === ALL_PROJECTS ? undefined : projectFilter);
	const accounts = useAgentAccounts(tab === "limits");
	const limitWindows = useUsageLimitWindows(7, tab === "windows");
	const accountActions = useAgentAccountActions();
	const isLimits = tab === "limits";
	const isWindows = tab === "windows";

	const refresh = async () => {
		if (isWindows) {
			await limitWindows.refetch();
			return;
		}
		if (!isLimits) {
			await report.refetch();
			return;
		}
		setRefreshingLimits(true);
		try {
			await accountActions.refresh();
		} finally {
			setRefreshingLimits(false);
		}
	};
	const refreshing = isWindows ? limitWindows.isFetching : isLimits ? refreshingLimits || accounts.isFetching : report.isFetching;
	const range = report.data ? formatRange(report.data.since, report.data.until, i18n.language) : null;

	return (
		<div className="flex min-h-0 flex-1 flex-col bg-background">
			<header className="workspace-topbar-container center-panel-titlebar flex h-toolbar shrink-0 items-center justify-between gap-3 border-b border-border-strong pr-[calc((var(--size-toolbar)-var(--size-topbar-control))/2)]">
				<h1 className={showPageTitle ? cn(topbarProjectLabelClass, "inline-flex items-center gap-1.5") : "sr-only"}>
					{showPageTitle ? <Gauge className="size-icon-md" aria-hidden="true" /> : null}
					{t("usage.title")}
				</h1>
				<div className="workspace-topbar-actions flex min-w-0 shrink-0 items-center gap-2">
					{tab === "spend" && projects.length > 0 ? (
						<SettingsOptionMenu
							aria-label={t("usage.projectFilter")}
							value={projectFilter}
							options={[{ value: ALL_PROJECTS, label: t("usage.allProjects") }, ...projects.map((project) => ({ value: project.id, label: projectNames.get(project.id) ?? project.name }))]}
							onChange={setProjectFilter}
							menuAlign="end"
							menuClassName="max-h-72"
							triggerClassName="h-(--size-control-md)! text-xs!"
						/>
					) : null}
					{tab === "spend" ? (
						<div className="flex items-center gap-0.5">
							<Button variant="ghost" size="icon" aria-label={t("usage.period.previous")} onClick={() => setOffset((value) => value + 1)} className="size-8">
								<ChevronLeft aria-hidden="true" className="size-icon-sm" />
							</Button>
							<SettingsOptionMenu
								aria-label={t("usage.period")}
								value={String(days)}
								options={PERIODS.map((period) => ({ value: String(period.days), label: t(period.label) }))}
								onChange={(value) => {
									setDays(Number(value));
									setOffset(0);
								}}
								menuAlign="end"
								triggerClassName="h-(--size-control-md)! text-xs!"
								renderTrigger={(selected) => (
									<span className="min-w-0 truncate">
										{/* An earlier period is named by its dates alone: "Last 7 days" would no longer be true. */}
										{offset === 0 || !range ? selected?.label : null}
										{range ? <span className="text-muted-foreground">{offset === 0 ? ` · ${range}` : range}</span> : null}
									</span>
								)}
							/>
							<Button variant="ghost" size="icon" aria-label={t("usage.period.next")} disabled={offset === 0} onClick={() => setOffset((value) => Math.max(0, value - 1))} className="size-8">
								<ChevronRight aria-hidden="true" className="size-icon-sm" />
							</Button>
						</div>
					) : null}
					<Tooltip>
						<TooltipTrigger asChild>
							<Button variant="ghost" size="icon" aria-label={t("usage.refresh")} disabled={refreshing} onClick={() => void refresh()} className="size-8">
								<RefreshCw aria-hidden="true" className={cn("size-icon-sm", refreshing && "animate-spin")} />
							</Button>
						</TooltipTrigger>
						<TooltipContent>{t("usage.refresh")}</TooltipContent>
					</Tooltip>
				</div>
			</header>
			{/* The page tabs stay put while the view under them scrolls. */}
			<div className="shrink-0">
				<div className="mx-auto w-full max-w-6xl px-6">
					<Tabs value={tab} onValueChange={(value) => setTab(value as UsageTab)}>
						<TabsList aria-label={t("usage.views")} className={HARNESS_TABS_CLASS}>
							<TabsTrigger value="spend" className={HARNESS_TAB_CLASS}>{t("usage.view.spend")}</TabsTrigger>
							<TabsTrigger value="limits" className={HARNESS_TAB_CLASS}>{t("usage.view.limits")}</TabsTrigger>
							<TabsTrigger value="windows" className={cn(HARNESS_TAB_CLASS, "gap-1.5")}>
								{t("usage.view.windows")}
								<Badge className="h-4 px-1.5 text-2xs font-normal">{t("usage.windows.experimental")}</Badge>
							</TabsTrigger>
						</TabsList>
					</Tabs>
				</div>
			</div>
			<ScrollArea.Root asChild type="hover" scrollHideDelay={500}>
				<main className="relative min-h-0 flex-1 overflow-hidden">
					<ScrollArea.Viewport className="h-full w-full overscroll-contain focus-visible:outline-none">
						<div className="mx-auto w-full max-w-6xl px-6 py-6">
							{isWindows ? (
								limitWindows.isLoading ? (
									<p role="status" className="py-16 text-center text-sm text-muted-foreground">{t("usage.loading")}</p>
								) : limitWindows.error ? (
									<p role="alert" className="py-16 text-center text-sm text-destructive">{limitWindows.error.message}</p>
								) : limitWindows.data ? (
									<UsageWindows history={limitWindows.data} now={now} />
								) : null
							) : isLimits ? (
								accounts.isLoading ? (
									<p role="status" className="py-16 text-center text-sm text-muted-foreground">{t("usage.loading")}</p>
								) : accounts.error ? (
									<p role="alert" className="py-16 text-center text-sm text-destructive">{accounts.error.message}</p>
								) : (
									<UsageLimits accounts={accounts.data ?? []} now={now} />
								)
							) : report.isLoading ? (
								<p role="status" className="py-16 text-center text-sm text-muted-foreground">{t("usage.loading")}</p>
							) : report.error ? (
								<p role="alert" className="py-16 text-center text-sm text-destructive">{report.error.message}</p>
							) : !report.data || report.data.rows.length === 0 ? (
								<div className="flex flex-col items-center gap-1 py-16 text-center">
									<p className="text-control font-medium text-foreground">{t("usage.empty.title")}</p>
									<p className="max-w-sm text-xs text-muted-foreground">{t("usage.empty.body")}</p>
								</div>
							) : (
								<div className={cn("transition-opacity", report.isPlaceholderData && "opacity-60")}>
									<UsageSpend report={report.data} unit={unit} onUnitChange={setUnit} projectNames={projectNames} />
								</div>
							)}
						</div>
					</ScrollArea.Viewport>
					<ScrollArea.Scrollbar orientation="vertical" className="z-10 flex w-2 touch-none select-none p-0.5">
						<ScrollArea.Thumb className="relative flex-1 rounded-full bg-[color-mix(in_srgb,var(--color-scrollbar)_42%,transparent)] hover:bg-[color-mix(in_srgb,var(--color-scrollbar)_56%,transparent)]" />
					</ScrollArea.Scrollbar>
				</main>
			</ScrollArea.Root>
		</div>
	);
}
