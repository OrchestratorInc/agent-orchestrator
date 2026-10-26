import { TriangleAlert } from "lucide-react";
import { useMemo } from "react";
import { useTranslation } from "react-i18next";
import { agentAccountName, agentAccountProviderNames, type AgentAccount, type AgentAccountProvider } from "../../hooks/useAgentAccounts";
import { formatCountdown, poolLimits, type LimitSegment, type PooledWindow } from "../../lib/usage-limits";
import { formatRelativeTime } from "../../lib/usage-format";
import { cn } from "../../lib/utils";
import { useUiStore } from "../../stores/ui-store";
import { AgentAvatar } from "../AgentAvatar";
import { Button } from "../ui/button";
import { Tooltip, TooltipContent, TooltipTrigger } from "../ui/tooltip";

const PROVIDER_HARNESS: Record<AgentAccountProvider, string> = { codex: "codex", claude: "claude-code" };

/** What is left reads in the same tones as the account settings: low is a warning, none is an error. */
function remainingTone(remaining: number): string {
	return remaining <= 0 ? "bg-error" : remaining <= 25 ? "bg-warning" : "bg-foreground/80";
}

/** Subscription windows for every signed-in account, pooled per provider. */
export function UsageLimits({ accounts, now }: { accounts: readonly AgentAccount[]; now: Date }) {
	const { t, i18n } = useTranslation();
	const providers = useMemo(() => poolLimits(accounts), [accounts]);
	const openGlobalSettings = useUiStore((state) => state.openGlobalSettings);

	if (providers.length === 0) {
		return (
			<div className="flex flex-col items-center gap-2 py-16 text-center">
				<p className="text-control font-medium text-foreground">{t("usage.limits.empty")}</p>
				<p className="max-w-sm text-xs text-muted-foreground">{t("usage.limits.emptyBody")}</p>
				<Button variant="ghost" size="sm" onClick={() => openGlobalSettings("harness")}>{t("usage.limits.openSettings")}</Button>
			</div>
		);
	}

	return (
		<div className="flex flex-col gap-10">
			{providers.map((entry) => {
				const issues = entry.accounts.filter((account) => !account.signedIn || account.problem);
				return (
					<section key={entry.provider} aria-labelledby={`usage-limits-${entry.provider}`} className="flex flex-col gap-4">
						<div className="flex flex-wrap items-center gap-x-3 gap-y-1">
							<h2 id={`usage-limits-${entry.provider}`} className="inline-flex items-center gap-2 text-control font-medium text-foreground">
								<AgentAvatar provider={PROVIDER_HARNESS[entry.provider]} decorative className="size-4" />
								{agentAccountProviderNames[entry.provider]}
							</h2>
							<span className="text-xs text-muted-foreground">{t("usage.limits.accounts", { count: entry.accounts.length })}</span>
							{entry.resetCredits > 0 ? (
								<span className="text-xs tabular-nums text-muted-foreground">{t("usage.limits.resetCredits", { value: entry.resetCredits })}</span>
							) : null}
							{entry.checkedAt ? (
								<span className="ms-auto text-xs text-muted-foreground">{t("usage.limits.checked", { value: formatRelativeTime(entry.checkedAt, now, i18n.language) })}</span>
							) : null}
						</div>
						{entry.windows.length > 0 ? (
							<div className="grid grid-cols-[minmax(7rem,auto)_5.5rem_minmax(0,1fr)_6rem] items-center gap-x-4 gap-y-3">
								{entry.windows.map((window) => (
									<LimitWindowRow key={window.id} window={window} accounts={entry.accounts} now={now} />
								))}
							</div>
						) : (
							<p className="text-xs text-muted-foreground">{t("usage.limits.noWindows")}</p>
						)}
						{issues.length > 0 ? (
							<ul className="flex flex-col gap-1">
								{issues.map((account) => (
									<li key={account.id} className="inline-flex items-center gap-1.5 text-xs text-muted-foreground">
										<TriangleAlert aria-hidden="true" className="size-icon-sm text-warning" />
										<span className="text-foreground">{agentAccountName(account, t)}</span>
										<span>{account.signedIn ? account.problem : t("usage.limits.signedOut")}</span>
									</li>
								))}
							</ul>
						) : null}
					</section>
				);
			})}
		</div>
	);
}

function LimitWindowRow({ window, accounts, now }: { window: PooledWindow; accounts: readonly AgentAccount[]; now: Date }) {
	const { t, i18n } = useTranslation();
	const remaining = Math.round(window.remainingPercent);
	const countdown = formatCountdown(window.nextResetAt, now);
	const percent = new Intl.NumberFormat(i18n.language, { style: "percent", maximumFractionDigits: 0 }).format(remaining / 100);
	return (
		<>
			<span className="truncate text-control text-foreground">{window.label}</span>
			<span className="text-end">
				<span className="text-heading-sm tabular-nums text-foreground">{percent}</span>
				<span className="ms-1 text-xs text-muted-foreground">{t("usage.limits.left")}</span>
			</span>
			<div
				className="flex h-2.5 min-w-0 gap-1"
				role="img"
				aria-label={t("usage.limits.barLabel", { window: window.label, value: percent })}
			>
				{window.segments.map((segment) => (
					<LimitSegmentBar
						key={segment.accountId}
						segment={segment}
						name={accountName(accounts, segment.accountId, t)}
						now={now}
						single={window.segments.length === 1}
					/>
				))}
			</div>
			<span className="text-end text-xs tabular-nums text-muted-foreground">
				{countdown ? t("usage.limits.resetsIn", { value: countdown }) : ""}
			</span>
		</>
	);
}

function accountName(accounts: readonly AgentAccount[], accountId: string, t: Parameters<typeof agentAccountName>[1]): string {
	const account = accounts.find((candidate) => candidate.id === accountId);
	return account ? agentAccountName(account, t) : accountId;
}

function LimitSegmentBar({ segment, name, now, single }: { segment: LimitSegment; name: string; now: Date; single: boolean }) {
	const { t, i18n } = useTranslation();
	const countdown = formatCountdown(segment.resetsAt, now);
	const reported = segment.remainingPercent !== null;
	const percent = reported
		? new Intl.NumberFormat(i18n.language, { style: "percent", maximumFractionDigits: 0 }).format((segment.remainingPercent ?? 0) / 100)
		: null;
	return (
		<Tooltip>
			<TooltipTrigger asChild>
				<div
					tabIndex={single ? -1 : 0}
					className={cn(
						"relative h-full min-w-0 flex-1 overflow-hidden rounded-full bg-muted outline-none focus-visible:ring-2 focus-visible:ring-ring",
						!reported && "bg-[repeating-linear-gradient(135deg,var(--color-muted)_0_4px,transparent_4px_8px)]",
					)}
				>
					{reported ? (
						<div
							className={cn("h-full rounded-full", remainingTone(segment.remainingPercent ?? 0))}
							style={{ width: `${segment.remainingPercent}%` }}
						/>
					) : null}
				</div>
			</TooltipTrigger>
			<TooltipContent side="top" className="flex flex-col gap-0.5 tabular-nums">
				<span className="font-medium">{name}</span>
				<span>{reported ? t("usage.limits.segmentLeft", { value: percent }) : t("usage.limits.notReported")}</span>
				{countdown ? <span>{t("usage.limits.resetsIn", { value: countdown })}</span> : null}
			</TooltipContent>
		</Tooltip>
	);
}
