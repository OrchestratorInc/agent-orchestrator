import { Globe, KeyRound, Trash2 } from "@/components/app-icons";
import { useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { useConnectedHosts } from "../../hooks/useHostConnection";
import { useWorkspaceQuery } from "../../hooks/useWorkspaceQuery";
import { labelForHost } from "../../lib/host-clients";
import {
	forgetApprovalSite,
	setApprovalMode,
	useApprovalAudit,
	useApprovalPolicy,
	type ApprovalAuditEntry,
	type ApprovalMode,
	type ApprovalPolicy,
} from "../../lib/approvals";
import { formatRelativeTime } from "../../lib/usage-format";
import { Button } from "../ui/button";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "../ui/select";
import { SettingsRow } from "./SettingsRow";
import { SettingsSection } from "./SettingsSection";

const MODES: ApprovalMode[] = ["off", "risky", "every-submit"];
/** Recent decisions shown before "Show all". */
const RECENT = 5;

/**
 * Settings → Browser: when agents ask before acting, the sites the user
 * always allows, and the recent decisions, as three sections.
 */
export function BrowserApprovalsSection() {
	const { t, i18n } = useTranslation();
	const queryClient = useQueryClient();
	const policy = useApprovalPolicy();
	const audit = useApprovalAudit();
	const workspaces = useWorkspaceQuery();
	// An "Always allow" given on a remote host's prompt is kept by that host.
	const hosts = useConnectedHosts();
	const [showAll, setShowAll] = useState(false);
	const [saveError, setSaveError] = useState<string>();
	const run = (work: Promise<void>) =>
		work.then(
			() => setSaveError(undefined),
			(reason: unknown) => setSaveError(reason instanceof Error ? reason.message : String(reason)),
		);
	const category = (value: string) => t(`approvals.category.${value}` as "approvals.category.payment", { defaultValue: value });
	const decision = (entry: ApprovalAuditEntry) =>
		t(`approvals.decision.${entry.decision}` as "approvals.decision.deny", { defaultValue: entry.decision });
	const sessionTitle = (sessionId: string) => {
		for (const workspace of workspaces.data ?? []) {
			const session = workspace.sessions.find((candidate) => candidate.id === sessionId);
			if (session && session.title !== sessionId) return session.title;
		}
		return undefined;
	};
	const loadError = policy.error ?? audit.error;
	const entries = audit.data ?? [];
	const shown = showAll ? entries : entries.slice(0, RECENT);
	const now = new Date();
	const locale = i18n.resolvedLanguage ?? i18n.language;

	return (
		<>
			<SettingsSection title={t("settings.approvals.title")} sectionId="browserApprovals" grouped>
				<SettingsRow
					label={t("settings.approvals.mode")}
					description={
						<>
							{t("settings.approvals.modeDescription")}
							{saveError ? <span className="mt-1 block text-error" role="alert">{saveError}</span> : null}
							{loadError ? (
								<span className="mt-1 block text-error" role="alert">
									{t("settings.approvals.loadFailed", { message: loadError.message })}
								</span>
							) : null}
						</>
					}
				>
					<Select
						value={policy.data?.defaultMode}
						onValueChange={(mode) => void run(setApprovalMode("", mode as ApprovalMode, queryClient).then(() => undefined))}
						disabled={!policy.data}
					>
						<SelectTrigger size="sm" className="w-48 text-control" aria-label={t("settings.approvals.mode")}>
							<SelectValue />
						</SelectTrigger>
						<SelectContent position="popper" side="bottom" align="end" sideOffset={4}>
							{MODES.map((mode) => (
								<SelectItem key={mode} value={mode}>
									{t(`approvals.mode.${mode}`)}
								</SelectItem>
							))}
						</SelectContent>
					</Select>
				</SettingsRow>
			</SettingsSection>

			<SettingsSection title={t("settings.approvals.sites")} sectionId="browserApprovalSites" grouped>
				{policy.data && policy.data.sites.length === 0 && hosts.length === 0 ? (
					<SettingsRow label={<span className="text-settings-muted">{t("settings.approvals.sitesEmpty")}</span>} />
				) : null}
				{policy.data?.sites.map((rule) => (
					<SiteRuleRow
						key={`${rule.site}|${rule.category}`}
						rule={rule}
						category={category(rule.category)}
						onForget={() => void run(forgetApprovalSite(rule.site, rule.category, queryClient))}
					/>
				))}
				{hosts.map((hostId) => (
					<HostSiteRules key={hostId} hostId={hostId} category={category} run={run} />
				))}
			</SettingsSection>

			<SettingsSection
				title={t("settings.approvals.log")}
				sectionId="browserApprovalLog"
				grouped
				action={
					entries.length > RECENT && !showAll ? (
						<Button type="button" variant="ghost" size="xs" onClick={() => setShowAll(true)}>
							{t("settings.approvals.showAll")}
						</Button>
					) : undefined
				}
			>
				{audit.data && entries.length === 0 ? (
					<SettingsRow label={<span className="text-settings-muted">{t("settings.approvals.logEmpty")}</span>} />
				) : null}
				{shown.map((entry) => {
					const title = sessionTitle(entry.sessionId);
					return (
						<SettingsRow
							key={entry.id}
							data-testid="approval-audit-entry"
							label={<span className="block truncate" title={entry.action}>{entry.action}</span>}
							description={
								<>
									{title ? <>{title} · </> : null}
									{entry.sessionId} · <span className="tabular-nums" title={new Date(entry.time).toLocaleString(locale)}>{formatRelativeTime(entry.time, now, locale)}</span>
								</>
							}
						>
							<span className="text-control text-settings-muted">{decision(entry)}</span>
						</SettingsRow>
					);
				})}
			</SettingsSection>
		</>
	);
}

type SiteRule = ApprovalPolicy["sites"][number];

/** One remembered site, with a button that forgets it. */
function SiteRuleRow({ rule, category, host, onForget }: { rule: SiteRule; category: string; host?: string; onForget: () => void }) {
	const { t } = useTranslation();
	return (
		<SettingsRow
			className="group"
			icon={rule.category === "credential" ? KeyRound : Globe}
			label={
				<span className="flex min-w-0 items-baseline gap-2">
					<span className="truncate font-mono">{rule.site}</span>
					{host ? <span className="shrink-0 text-caption text-settings-muted">{host}</span> : null}
				</span>
			}
		>
			<div className="flex items-center">
				<span className="truncate text-control text-settings-muted">{category}</span>
				{/* Takes no room until the row is hovered or the button focused,
				    so the label lines up with the decisions' below. */}
				<Button
					type="button"
					variant="destructive-ghost"
					size="icon-xs"
					aria-label={`${t("settings.approvals.forget")} ${rule.site}`}
					className="ml-0 max-w-0 overflow-hidden opacity-0 group-hover:ml-2 group-hover:max-w-8 group-hover:opacity-100 focus-visible:ml-2 focus-visible:max-w-8 focus-visible:opacity-100"
					onClick={onForget}
				>
					<Trash2 aria-hidden="true" />
				</Button>
			</div>
		</SettingsRow>
	);
}

/** A connected remote host's remembered sites, read and forgotten on that host. */
function HostSiteRules({
	hostId,
	category,
	run,
}: {
	hostId: string;
	category: (value: string) => string;
	run: (work: Promise<void>) => void;
}) {
	const queryClient = useQueryClient();
	const policy = useApprovalPolicy(undefined, hostId);
	const host = labelForHost(hostId) ?? hostId;
	return (
		<>
			{policy.data?.sites.map((rule) => (
				<SiteRuleRow
					key={`${hostId}|${rule.site}|${rule.category}`}
					rule={rule}
					category={category(rule.category)}
					host={host}
					onForget={() => run(forgetApprovalSite(rule.site, rule.category, queryClient, hostId))}
				/>
			))}
		</>
	);
}
