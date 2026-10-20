import { useState } from "react";
import { useTranslation } from "react-i18next";
import { formatBytes } from "../lib/format-bytes";
import {
	activityStateLabelKey,
	isStoppableOwnedTree,
	type HostMemory,
	type ProcessInventory,
	type ProcessKillTarget,
	type ProcessTreeRow,
} from "../hooks/useProcessInventoryQuery";
import { Badge } from "./ui/badge";
import { Popover, PopoverContent, PopoverTrigger } from "./ui/popover";
import { Tooltip, TooltipContent, TooltipTrigger } from "./ui/tooltip";

type MemoryPopoverProps = {
	host: HostMemory;
	trees: ProcessTreeRow[];
	totals: ProcessInventory["totals"];
	/** sessionId whose kill is currently in flight, if any. */
	pendingKillSessionId?: string;
	/** sessionId whose stop is currently in flight, if any. */
	pendingStopSessionId?: string;
	killError: string | null;
	stopError: string | null;
	onKillTrees: (targets: ProcessKillTarget[]) => void;
	onStopSessions: (sessionIds: string[]) => void;
	onBatchKill: () => void;
};

// Session ids are "<projectId>-<per-project counter>" (domain.CreateSession);
// stripping the trailing counter groups a project's workers together. Good
// enough for display grouping — the kill/stop targets stay the exact trees.
function projectGroupOf(sessionId: string): string {
	const match = sessionId.match(/^(.*)-\d+$/);
	return match ? match[1] : sessionId;
}

function toKillTarget(tree: ProcessTreeRow): ProcessKillTarget {
	return { sessionId: tree.sessionId, rootPid: tree.rootPid, rootLstart: tree.rootLstart };
}

// The status bar's RAM/Swap segment: a hover tooltip names the AO footprint
// (basic stats), a click opens the extended popover — full host memory
// breakdown plus every AO process tree grouped by project. Orphaned trees
// carry kill buttons (processes/kill), idle owned sessions carry Stop buttons
// (sessions/{id}/kill — the board's Kill; the session stays resumable).
// Radix tooltip + popover both asChild onto one button (the NotificationCenter
// composition); the tooltip suppresses itself while the popover is open.
export function MemoryPopover({
	host,
	trees,
	totals,
	pendingKillSessionId,
	pendingStopSessionId,
	killError,
	stopError,
	onKillTrees,
	onStopSessions,
	onBatchKill,
}: MemoryPopoverProps) {
	const { t } = useTranslation();
	const [tooltipOpen, setTooltipOpen] = useState(false);
	const [popoverOpen, setPopoverOpen] = useState(false);
	const orphans = trees.filter((tree) => tree.state === "orphan");
	const pressureFreePercent = host.pressureFreePercent;
	// darwin-only kinds arrive as optional in the generated schema (omitempty).
	const wiredBytes = host.wiredBytes ?? 0;
	const appBytes = host.appBytes ?? 0;
	const compressedBytes = host.compressedBytes ?? 0;

	const projectGroups = new Map<string, ProcessTreeRow[]>();
	for (const tree of trees) {
		const group = projectGroupOf(tree.sessionId);
		const list = projectGroups.get(group);
		if (list) {
			list.push(tree);
		} else {
			projectGroups.set(group, [tree]);
		}
	}
	const sortedGroups = [...projectGroups.entries()]
		.map(([project, groupTrees]) => ({
			project,
			groupTrees: [...groupTrees].sort((a, b) => b.rssBytes - a.rssBytes),
			rssBytes: groupTrees.reduce((sum, tree) => sum + tree.rssBytes, 0),
		}))
		.sort((a, b) => b.rssBytes - a.rssBytes);

	return (
		<Popover open={popoverOpen} onOpenChange={setPopoverOpen}>
			<Tooltip open={tooltipOpen && !popoverOpen} onOpenChange={setTooltipOpen}>
				<TooltipTrigger asChild>
					<PopoverTrigger asChild>
						<button
							type="button"
							data-testid="status-bar-memory"
							className="flex items-center gap-2 rounded-md px-1 py-0.5 text-caption text-muted-foreground hover:bg-interactive-hover hover:text-foreground"
						>
							<span>
								{t("statusBar.aoShort")}{" "}
								<span className="tabular-nums text-foreground">
									{formatBytes(
										totals.daemonRssBytes + totals.tmuxRssBytes + totals.sessionsRssBytes + totals.orphansRssBytes,
									)}
								</span>
							</span>
							<span aria-hidden="true">·</span>
							<span>
								{t("statusBar.ramLabel")}{" "}
								<span className="tabular-nums">
									{formatBytes(host.usedBytes)} / {formatBytes(host.totalBytes)}
								</span>
							</span>
							{host.swapTotalBytes > 0 && (
								<span>
									{t("statusBar.swapLabel")}{" "}
									<span className="tabular-nums">
										{formatBytes(host.swapUsedBytes)} / {formatBytes(host.swapTotalBytes)}
									</span>
								</span>
							)}
						</button>
					</PopoverTrigger>
				</TooltipTrigger>
				<TooltipContent side="top">
					<p>{t("statusBar.tooltipDaemon", { rss: formatBytes(totals.daemonRssBytes) })}</p>
					<p>{t("statusBar.tooltipSessions", { count: totals.sessionsCount, rss: formatBytes(totals.sessionsRssBytes) })}</p>
					{totals.orphansCount > 0 && (
						<p>{t("statusBar.tooltipOrphans", { count: totals.orphansCount, rss: formatBytes(totals.orphansRssBytes) })}</p>
					)}
				</TooltipContent>
			</Tooltip>
			<PopoverContent side="top" align="end" className="w-80 space-y-3 p-3 text-caption">
				<div>
					<p className="font-medium text-foreground">{t("statusBar.hostMemoryTitle")}</p>
					<div className="mt-1 grid grid-cols-[auto_1fr] gap-x-3 gap-y-0.5">
						<span>{t("statusBar.ramLabel")}</span>
						<span className="tabular-nums">
							{formatBytes(host.usedBytes)} / {formatBytes(host.totalBytes)}
						</span>
						{wiredBytes > 0 && (
							<>
								<span>{t("statusBar.kindWired")}</span>
								<span className="tabular-nums">{formatBytes(wiredBytes)}</span>
							</>
						)}
						{appBytes > 0 && (
							<>
								<span>{t("statusBar.kindApp")}</span>
								<span className="tabular-nums">{formatBytes(appBytes)}</span>
							</>
						)}
						{compressedBytes > 0 && (
							<>
								<span>{t("statusBar.kindCompressed")}</span>
								<span className="tabular-nums">{formatBytes(compressedBytes)}</span>
							</>
						)}
						<span>{t("statusBar.kindCached")}</span>
						<span className="tabular-nums">{formatBytes(host.cachedBytes)}</span>
						<span>{t("statusBar.kindFree")}</span>
						<span className="tabular-nums">{formatBytes(host.freeBytes)}</span>
						<span>{t("statusBar.swapLabel")}</span>
						<span className="tabular-nums">
							{formatBytes(host.swapUsedBytes)} / {formatBytes(host.swapTotalBytes)}
						</span>
					</div>
					{host.swapMaxBytes > host.swapTotalBytes && (
						<p className="mt-1 text-muted-foreground">
							{t("statusBar.swapCeiling", { limit: formatBytes(host.swapMaxBytes - host.swapTotalBytes) })}
						</p>
					)}
					{pressureFreePercent != null && (
						<p className="text-muted-foreground">{t("statusBar.tooltipPressure", { percent: pressureFreePercent })}</p>
					)}
				</div>
				<div>
					<div className="flex items-center justify-between gap-2">
						<p className="font-medium text-foreground">{t("statusBar.treesTitle")}</p>
						{orphans.length > 1 && (
							<button
								type="button"
								data-testid="status-bar-kill-all"
								className="text-caption text-destructive hover:underline disabled:pointer-events-none disabled:opacity-50"
								onClick={onBatchKill}
							>
								{t("statusBar.killAllOrphans")}
							</button>
						)}
					</div>
					{killError && (
						<p role="alert" className="mt-1 text-error">
							{killError}
						</p>
					)}
					{stopError && (
						<p role="alert" className="mt-1 text-error">
							{stopError}
						</p>
					)}
					<div className="mt-1 max-h-64 space-y-2 overflow-y-auto">
						{sortedGroups.map(({ project, groupTrees, rssBytes }) => {
							const groupOrphans = groupTrees.filter((tree) => tree.state === "orphan");
							const groupIdle = groupTrees.filter(isStoppableOwnedTree);
							return (
								<div key={project}>
									<div className="flex items-center justify-between gap-2">
										<span className="truncate font-medium text-foreground">
											{project} <span className="tabular-nums text-muted-foreground">{formatBytes(rssBytes)}</span>
										</span>
										{groupOrphans.length > 0 && (
											<button
												type="button"
												aria-label={t("statusBar.killGroupAria", { project, count: groupOrphans.length })}
												disabled={pendingKillSessionId !== undefined || pendingStopSessionId !== undefined}
												className="shrink-0 text-caption text-destructive hover:underline disabled:pointer-events-none disabled:opacity-50"
												onClick={() => onKillTrees(groupOrphans.map(toKillTarget))}
											>
												{t("statusBar.killGroup", { count: groupOrphans.length })}
											</button>
										)}
										{groupIdle.length > 0 && (
											<button
												type="button"
												aria-label={t("statusBar.stopGroupAria", { project, count: groupIdle.length })}
												disabled={pendingStopSessionId !== undefined || pendingKillSessionId !== undefined}
												className="shrink-0 text-caption text-destructive hover:underline disabled:pointer-events-none disabled:opacity-50"
												onClick={() => onStopSessions(groupIdle.map((tree) => tree.sessionId))}
											>
												{t("statusBar.stopGroup", { count: groupIdle.length })}
											</button>
										)}
									</div>
									<ul className="space-y-0.5">
										{groupTrees.map((tree) => {
											const activityKey = activityStateLabelKey(tree.activityState);
											return (
												<li key={`${tree.sessionId}-${tree.rootPid}`} className="flex items-center justify-between gap-2">
												<span className="truncate tabular-nums">
													{t("statusBar.treeRow", { session: tree.sessionId, pid: tree.rootPid, rss: formatBytes(tree.rssBytes) })}
												</span>
												{tree.state === "orphan" ? (
													<button
														type="button"
														aria-label={t("statusBar.killTreeAria", { session: tree.sessionId })}
														disabled={pendingKillSessionId === tree.sessionId}
														className="shrink-0 text-caption text-destructive hover:underline disabled:pointer-events-none disabled:opacity-50"
														onClick={() => onKillTrees([toKillTarget(tree)])}
													>
														{pendingKillSessionId === tree.sessionId ? "…" : t("statusBar.killTree")}
													</button>
												) : isStoppableOwnedTree(tree) ? (
													<button
														type="button"
														aria-label={t("statusBar.stopTreeAria", { session: tree.sessionId })}
														disabled={pendingStopSessionId === tree.sessionId}
														className="shrink-0 text-caption text-destructive hover:underline disabled:pointer-events-none disabled:opacity-50"
														onClick={() => onStopSessions([tree.sessionId])}
													>
														{pendingStopSessionId === tree.sessionId ? "…" : t("statusBar.stopTree")}
													</button>
												) : tree.state === "owned" ? (
													<Badge variant="neutral" className="shrink-0">
														{activityKey ? (
															<>
																{t(activityKey)}
																{!tree.attached && ` · ${t("statusBar.stateAdopted")}`}
															</>
														) : tree.attached ? (
															t("statusBar.stateOwned")
														) : (
															`${t("statusBar.stateOwned")} · ${t("statusBar.stateAdopted")}`
														)}
													</Badge>
												) : (
													<Badge variant="outline" className="shrink-0">
														{t("statusBar.stateForeign")}
													</Badge>
												)}
											</li>
											);
										})}
									</ul>
								</div>
							);
						})}
						{trees.length === 0 && <p className="text-muted-foreground">{t("statusBar.noTrees")}</p>}
					</div>
				</div>
			</PopoverContent>
		</Popover>
	);
}
