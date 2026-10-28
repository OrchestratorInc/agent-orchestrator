import { TriangleAlert } from "lucide-react";
import { useMemo } from "react";
import { useTranslation } from "react-i18next";
import { agentAccountName, agentAccountProviderNames, type AgentAccount, type AgentAccountLimitWindow, type AgentAccountProvider } from "../../hooks/useAgentAccounts";
import { agentTone } from "../../lib/agent-tone";
import { formatCountdown, groupAccounts, remainingPercent } from "../../lib/usage-limits";
import { formatRelativeTime } from "../../lib/usage-format";
import { cn } from "../../lib/utils";
import { useUiStore } from "../../stores/ui-store";
import { AgentAvatar } from "../AgentAvatar";
import { formatPlanName } from "../settings/CodexAccountDetails";
import { Button } from "../ui/button";

const PROVIDER_HARNESS: Record<AgentAccountProvider, string> = { codex: "codex", claude: "claude-code" };

/** Low reads as a warning and none as an error, as in the account settings; otherwise the agent's colour. */
function remainingTone(remaining: number): string | null {
	return remaining <= 0 ? "bg-error" : remaining <= 25 ? "bg-warning" : null;
}

/** Subscription windows for every signed-in account, one block per account under its provider. */
export function UsageLimits({ accounts, now }: { accounts: readonly AgentAccount[]; now: Date }) {
	const { t, i18n } = useTranslation();
	const providers = useMemo(() => groupAccounts(accounts), [accounts]);
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
			{providers.map((entry) => (
				<section key={entry.provider} aria-labelledby={`usage-limits-${entry.provider}`} className="flex flex-col gap-4">
					<div className="flex flex-wrap items-center gap-x-3 gap-y-1">
						<h2 id={`usage-limits-${entry.provider}`} className="inline-flex items-center gap-2 text-control font-medium text-foreground">
							<AgentAvatar provider={PROVIDER_HARNESS[entry.provider]} decorative className="size-4" />
							{agentAccountProviderNames[entry.provider]}
						</h2>
						<span className="text-xs text-muted-foreground">{t("usage.limits.accounts", { count: entry.accounts.length })}</span>
						{entry.checkedAt ? (
							<span className="ms-auto text-xs text-muted-foreground">{t("usage.limits.checked", { value: formatRelativeTime(entry.checkedAt, now, i18n.language) })}</span>
						) : null}
					</div>
					{/* One grid for every account, so bars and figures line up down the provider. */}
					<div className="grid grid-cols-[minmax(7rem,auto)_5.5rem_minmax(0,1fr)_auto] items-center gap-x-4 gap-y-3">
						{entry.accounts.map((account, index) => (
							<AccountLimitRows key={account.id} account={account} harness={PROVIDER_HARNESS[entry.provider]} first={index === 0} now={now} />
						))}
					</div>
				</section>
			))}
		</div>
	);
}

function AccountLimitRows({ account, harness, first, now }: { account: AgentAccount; harness: string; first: boolean; now: Date }) {
	const { t } = useTranslation();
	const windows = account.limits?.windows ?? [];
	const plan = formatPlanName(account.plan);
	const resetCredits = account.limits?.resetCredits ?? 0;
	const meta = [
		plan ? t("settings.codexAccounts.planLabel", { name: plan }) : null,
		account.default ? t("settings.agentAccounts.default") : null,
		resetCredits > 0 ? t("usage.limits.resetCredits", { value: resetCredits }) : null,
	].filter(Boolean).join(" · ");
	const issue = account.signedIn ? account.problem : t("usage.limits.signedOut");
	return (
		<>
			<div className={cn("col-span-full flex min-w-0 flex-wrap items-baseline gap-x-2 gap-y-0.5", !first && "mt-4")} data-usage-account={account.id}>
				<span className="truncate text-control font-medium text-foreground">{agentAccountName(account, t)}</span>
				{meta ? <span className="text-xs text-muted-foreground">{meta}</span> : null}
				{issue ? (
					<span className="inline-flex items-center gap-1 text-xs text-muted-foreground">
						<TriangleAlert aria-hidden="true" className="size-icon-sm self-center text-warning" />
						{issue}
					</span>
				) : null}
			</div>
			{windows.map((window) => <LimitWindowRow key={window.id} window={window} harness={harness} now={now} />)}
			{windows.length === 0 && !issue ? <p className="col-span-full text-xs text-muted-foreground">{t("usage.limits.noWindows")}</p> : null}
		</>
	);
}

function LimitWindowRow({ window, harness, now }: { window: AgentAccountLimitWindow; harness: string; now: Date }) {
	const { t, i18n } = useTranslation();
	const remaining = Math.round(remainingPercent(window.usedPercent));
	const tone = remainingTone(remaining);
	const countdown = formatCountdown(window.resetsAt, now);
	const percent = new Intl.NumberFormat(i18n.language, { style: "percent", maximumFractionDigits: 0 }).format(remaining / 100);
	return (
		<>
			<span className="truncate text-control text-foreground">{window.label}</span>
			<span className="text-end">
				<span className="text-heading-sm tabular-nums text-foreground">{percent}</span>
				<span className="ms-1 text-xs text-muted-foreground">{t("usage.limits.left")}</span>
			</span>
			<div
				role="progressbar"
				aria-label={t("usage.limits.barLabel", { window: window.label, value: percent })}
				aria-valuemin={0}
				aria-valuemax={100}
				aria-valuenow={remaining}
				className="h-2.5 min-w-0 overflow-hidden rounded-full bg-muted"
			>
				<div className={cn("h-full rounded-full", tone)} style={{ width: `${remaining}%`, backgroundColor: tone ? undefined : agentTone(harness) }} />
			</div>
			<span className="whitespace-nowrap text-end text-xs tabular-nums text-muted-foreground">
				{countdown ? t("usage.limits.resetsIn", { value: countdown }) : ""}
			</span>
		</>
	);
}
