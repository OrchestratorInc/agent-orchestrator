import { useState, type ReactNode } from "react";
import { useNavigate } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";
import { AlertCircle, Check, ChevronDown, ChevronRight, ExternalLink } from "lucide-react";
import type { ProviderAccount } from "../../hooks/useProviderAccounts";
import { useSessionUsageSummaries } from "../../hooks/useSessionUsageSummaries";
import { useWorkspaceQuery } from "../../hooks/useWorkspaceQuery";
import { aoBridge } from "../../lib/bridge";
import { formatCostNanos } from "../../lib/format-cost";
import { cn } from "../../lib/utils";
import { useUiStore } from "../../stores/ui-store";
import { STANDALONE_WORKSPACE_ID } from "../../types/workspace";
import { AccountMenu, AccountMenuItems } from "../AccountMenu";
import { Button } from "../ui/button";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "../ui/dropdown-menu";
import { Switch } from "../ui/switch";
import { Fact, formatCount, IconAction, Row, Rows } from "./ProviderAccountDetail";
import type { AccountsPage } from "./ProviderAccountsSection";

type Usage = NonNullable<ProviderAccount["usage"]>;
// Where each provider shows the plan, its usage and its billing.
const PLAN_PAGES = { claude: "https://claude.ai/settings/usage", codex: "https://chatgpt.com/codex/settings/usage" } as const;
const FAILURES = { limit: "providerAccounts.failureLimit", "sign-in": "providerAccounts.failureSignIn", server: "providerAccounts.failureServer", other: "providerAccounts.failureOther" } as const;
const WARN_AT = [0, 5, 10, 20, 30];
const SHOWN = 5;

export function ManagePlan({ account, page }: { account: ProviderAccount; page: AccountsPage }) {
	const { t } = useTranslation();
	return (
		<Button type="button" variant="ghost" size="sm" className="gap-1.5 text-muted-foreground" onClick={() => void page.run(() => aoBridge.app.openExternal(PLAN_PAGES[account.provider]))}>
			{t("providerAccounts.managePlan")}<ExternalLink aria-hidden="true" className="size-3.5" />
		</Button>
	);
}

// How the account's recent requests went, as the helper saw them.
export function AccountHealth({ health, language }: { health: Usage["health"]; language: string }) {
	const { t } = useTranslation();
	const failure = health?.lastFailure;
	if (!failure && !health?.firstWordMs) return null;
	const kinds = (["limit", "signIn", "server", "other"] as const).filter((kind) => health?.failures?.[kind]);
	return (
		<Rows title={t("providerAccounts.healthHeading")}>
			{failure ? (
				<Row icon={<AlertCircle aria-hidden="true" className="size-4 shrink-0 text-warning" />} title={t("providerAccounts.lastFailure")} hint={`${t(FAILURES[failure.kind])} · ${new Intl.DateTimeFormat(language, { dateStyle: "medium", timeStyle: "short" }).format(new Date(failure.at))}`}>
					<span className="flex gap-2.5 text-[13px] tabular-nums text-muted-foreground">
						{kinds.map((kind) => <span key={kind}><span className="font-medium text-foreground">{health!.failures![kind]}</span> {t(FAILURES[kind === "signIn" ? "sign-in" : kind]).toLowerCase()}</span>)}
					</span>
				</Row>
			) : null}
			{health?.firstWordMs ? (
				<Row title={t("providerAccounts.firstWord")} hint={t("providerAccounts.firstWordHint")}>
					<span className="text-sm tabular-nums text-foreground">{t("providerAccounts.seconds", { value: new Intl.NumberFormat(language, { maximumFractionDigits: 1 }).format(health.firstWordMs / 1000) })}</span>
				</Row>
			) : null}
		</Rows>
	);
}

function Choice({ label, children }: { label: string; children: ReactNode }) {
	return (
		<DropdownMenu>
			<DropdownMenuTrigger asChild>
				<Button type="button" variant="secondary" className="max-w-56 gap-1.5 font-normal"><span className="min-w-0 truncate">{label}</span><ChevronDown aria-hidden="true" className="size-3.5 shrink-0 text-muted-foreground" /></Button>
			</DropdownMenuTrigger>
			<DropdownMenuContent align="end" className="min-w-52 text-xs">{children}</DropdownMenuContent>
		</DropdownMenu>
	);
}
const Tick = ({ on }: { on: boolean }) => <Check aria-hidden="true" className={on ? "size-3.5" : "invisible size-3.5"} />;

// What the account does without being asked: take new sessions, hand its sessions on at a limit, warn before one.
export function AccountRules({ account, page, others }: { account: ProviderAccount; page: AccountsPage; others: ProviderAccount[] }) {
	const { t } = useTranslation();
	const set = (change: { reserved?: boolean; onLimit?: string; warnAt?: number }) => void page.act(account.id, { action: "settings", ...change });
	const next = others.find((other) => other.id === account.onLimit);
	const warn = (percent: number) => (percent ? t("providerAccounts.warnAtPercent", { percent }) : t("providerAccounts.never"));
	return (
		<>
			<Row title={t("providerAccounts.useForNew")} hint={t(account.primary ? "providerAccounts.useForNewDefault" : "providerAccounts.useForNewHint")}>
				<Switch aria-label={t("providerAccounts.useForNew")} checked={!account.reserved} disabled={page.pending || account.primary} onCheckedChange={(on) => set({ reserved: !on })} />
			</Row>
			{others.length ? (
				<Row title={t("providerAccounts.onLimit")} hint={t("providerAccounts.onLimitHint")}>
					<Choice label={next ? t("providerAccounts.switchTo", { name: next.displayName }) : t("providerAccounts.doNothing")}>
						<DropdownMenuItem onSelect={() => set({ onLimit: "" })}><Tick on={!next} />{t("providerAccounts.doNothing")}</DropdownMenuItem>
						<AccountMenuItems accounts={others} selectedId={next?.id ?? ""} markDefault={false} onSelect={(other) => set({ onLimit: other.id })} />
					</Choice>
				</Row>
			) : null}
			<Row title={t("providerAccounts.warnWhenLow")}>
				<Choice label={warn(account.warnAt ?? 0)}>
					{WARN_AT.map((percent) => <DropdownMenuItem key={percent} onSelect={() => set({ warnAt: percent })}><Tick on={(account.warnAt ?? 0) === percent} />{warn(percent)}</DropdownMenuItem>)}
				</Choice>
			</Row>
		</>
	);
}

// The sessions on the account by name, busiest today first, each one a click from being opened or moved.
export function AccountSessions({ account, page, others, language }: { account: ProviderAccount; page: AccountsPage; others: ProviderAccount[]; language: string }) {
	const { t } = useTranslation();
	const navigate = useNavigate();
	const closeSettings = useUiStore((state) => state.closeSettings);
	const [all, setAll] = useState(false);
	const workspaces = useWorkspaceQuery({ includeCloud: false }).data ?? [];
	const today = account.usage?.activity?.sessions ?? {};
	const rows = account.sessions.flatMap((id) => {
		const workspace = workspaces.find((candidate) => candidate.sessions.some((session) => session.id === id));
		const session = workspace?.sessions.find((candidate) => candidate.id === id);
		return workspace && session ? [{ id, workspace, session, tokens: today[id] ?? 0 }] : [];
	}).sort((first, second) => second.tokens - first.tokens);
	if (!rows.length) return null;
	return (
		<Rows title={t("providerAccounts.sessionsOnAccount", { count: rows.length })} data-testid="provider-account-sessions">
			{(all ? rows : rows.slice(0, SHOWN)).map(({ id, workspace, session, tokens }) => (
				<div key={id} className="flex min-h-11 items-center gap-3 py-1.5 pl-4 pr-2 text-[13px]">
					<span aria-hidden="true" className={cn("size-1.5 shrink-0 rounded-full", session.status === "working" ? "bg-status-working" : "bg-muted-foreground/50")} />
					<span className="min-w-0 flex-1 truncate text-foreground" title={session.title}>{session.title || id}</span>
					{tokens ? <span className="shrink-0 tabular-nums text-muted-foreground">{t("providerAccounts.tokensTodayShort", { tokens: formatCount(tokens, language) })}</span> : null}
					{others.length ? (
						<AccountMenu accounts={others} onSelect={(target) => void page.act(target.id, { action: "assign-session", sessionId: id }, t("providerAccounts.sessionMoved", { name: target.displayName }))}>
							<IconAction name={t("providerAccounts.moveSession")} icon={ChevronDown} disabled={page.pending} />
						</AccountMenu>
					) : null}
					<IconAction name={t("providerAccounts.openSession")} icon={ChevronRight} onClick={() => {
						closeSettings();
						if (workspace.id === STANDALONE_WORKSPACE_ID) void navigate({ to: "/sessions/$sessionId", params: { sessionId: id } });
						else void navigate({ to: "/projects/$projectId/sessions/$sessionId", params: { projectId: workspace.id, sessionId: id } });
					}} />
				</div>
			))}
			{rows.length > SHOWN && !all ? (
				<button type="button" className="flex min-h-10 w-full items-center px-4 text-left text-[13px] text-muted-foreground outline-none hover:text-foreground focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring" onClick={() => setAll(true)}>{t("providerAccounts.showMoreSessions", { count: rows.length - SHOWN })}</button>
			) : null}
		</Rows>
	);
}

// What passed through AO on this account: a bar a day, then the totals.
export function AccountThroughput({ account, language }: { account: ProviderAccount; language: string }) {
	const { t } = useTranslation();
	const activity = account.usage?.activity;
	const costs = useSessionUsageSummaries().data;
	const nanos = account.sessions.reduce((sum, id) => sum + (costs?.get(id)?.estimatedCost?.totalNanos ?? 0), 0);
	const days = activity?.days ?? [];
	const peak = Math.max(1, ...days.map((day) => day.tokens));
	const day = (date: string) => new Intl.DateTimeFormat(language, { month: "short", day: "numeric" }).format(new Date(`${date}T12:00:00`));
	return (
		<>
			{days.some((entry) => entry.tokens) ? (
				<div className="px-4 pb-2.5 pt-3.5" role="img" aria-label={t("providerAccounts.tokensPerDay")}>
					<div className="flex h-14 items-end gap-1">
						{days.map((entry, index) => <span key={entry.date} title={`${day(entry.date)} · ${formatCount(entry.tokens, language)}`} className={cn("min-h-0.5 flex-1 rounded-sm", index === days.length - 1 ? "bg-foreground" : "bg-muted-foreground/55")} style={{ height: `${(entry.tokens / peak) * 100}%` }} />)}
					</div>
					<div className="mt-1.5 flex justify-between text-2xs text-passive"><span>{day(days[0]!.date)}</span><span>{t("providerAccounts.tokensPerDay")}</span><span>{t("providerAccounts.today")}</span></div>
				</div>
			) : null}
			{activity ? (
				<>
					<Fact label={t("providerAccounts.throughToday")}>{formatCount(activity.today, language)}</Fact>
					<Fact label={t("providerAccounts.throughWeek")}>{formatCount(activity.week, language)}</Fact>
					{activity.since ? <Fact label={t("providerAccounts.throughSince", { date: day(activity.since) })}>{formatCount(activity.total, language)}</Fact> : null}
				</>
			) : null}
			{nanos > 0 ? <Fact label={<span title={t("providerAccounts.costHint")}>{t("providerAccounts.sessionsCost")}</span>}>{formatCostNanos(nanos)}</Fact> : null}
		</>
	);
}

export function AccountModelMix({ activity }: { activity: Usage["activity"] }) {
	const { t } = useTranslation();
	const models = activity?.models ?? [];
	const total = models.reduce((sum, model) => sum + model.tokens, 0);
	if (!total) return null;
	return (
		<Rows title={t("providerAccounts.byModel")}>
			<div className="grid gap-2 px-4 py-3">
				{models.map((model) => {
					const percent = Math.round((model.tokens / total) * 100);
					return (
						<div key={model.model} className="grid grid-cols-[minmax(0,1fr)_72px_36px] items-center gap-2.5 text-[13px]">
							<span className="truncate text-foreground" title={model.model}>{model.model}</span>
							<span className="h-1.5 overflow-hidden rounded-full bg-muted"><span className="block h-full rounded-full bg-muted-foreground" style={{ width: `${percent}%` }} /></span>
							<span className="text-right tabular-nums text-muted-foreground">{percent}%</span>
						</div>
					);
				})}
			</div>
		</Rows>
	);
}
