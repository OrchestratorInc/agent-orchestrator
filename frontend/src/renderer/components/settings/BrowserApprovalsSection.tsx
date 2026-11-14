import { useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import {
	forgetApprovalSite,
	setApprovalMode,
	useApprovalAudit,
	useApprovalPolicy,
	type ApprovalAuditEntry,
	type ApprovalMode,
} from "../../lib/approvals";
import { Button } from "../ui/button";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "../ui/select";
import { SettingsRow } from "./SettingsRow";
import { SettingsSection } from "./SettingsSection";

const MODES: ApprovalMode[] = ["off", "risky", "every-submit"];

function formatTime(iso: string): string {
	const date = new Date(iso);
	return Number.isNaN(date.getTime()) ? iso : date.toLocaleString(undefined, { dateStyle: "medium", timeStyle: "short" });
}

/**
 * Settings → Browser → Approvals: when agents ask before acting, the sites
 * the user always allows, and the log of every gated action.
 */
export function BrowserApprovalsSection() {
	const { t } = useTranslation();
	const queryClient = useQueryClient();
	const policy = useApprovalPolicy();
	const audit = useApprovalAudit();
	const [saveError, setSaveError] = useState<string>();
	const run = (work: Promise<void>) =>
		work.then(
			() => setSaveError(undefined),
			(reason: unknown) => setSaveError(reason instanceof Error ? reason.message : String(reason)),
		);
	const category = (value: string) => t(`approvals.category.${value}` as "approvals.category.payment", { defaultValue: value });
	const decision = (entry: ApprovalAuditEntry) =>
		t(`approvals.decision.${entry.decision}` as "approvals.decision.deny", { defaultValue: entry.decision });
	const loadError = policy.error ?? audit.error;

	return (
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

			<SettingsRow
				label={t("settings.approvals.sites")}
				description={policy.data && policy.data.sites.length === 0 ? t("settings.approvals.sitesEmpty") : undefined}
			/>
			{policy.data?.sites.map((rule) => (
				<SettingsRow
					key={`${rule.site}|${rule.category}`}
					label={<span className="font-mono">{rule.site}</span>}
					description={category(rule.category)}
				>
					<Button
						type="button"
						variant="destructive-ghost"
						size="xs"
						onClick={() => void run(forgetApprovalSite(rule.site, rule.category, queryClient))}
					>
						{t("settings.approvals.forget")}
					</Button>
				</SettingsRow>
			))}

			<SettingsRow
				label={t("settings.approvals.log")}
				description={audit.data && audit.data.length === 0 ? t("settings.approvals.logEmpty") : undefined}
			/>
			{audit.data && audit.data.length > 0 ? (
				<div className="overflow-x-auto px-(--size-settings-row-padding) pb-2" data-testid="approval-audit-log">
					<table className="w-full text-left text-control">
						<tbody>
							{audit.data.map((entry) => (
								<tr key={entry.id} className="border-t border-border first:border-t-0 align-top">
									<td className="whitespace-nowrap py-1.5 pr-3 tabular-nums text-settings-muted">{formatTime(entry.time)}</td>
									<td className="py-1.5 pr-3 font-mono text-settings-muted">{entry.site}</td>
									<td className="py-1.5 pr-3 font-mono text-settings-muted">{entry.sessionId}</td>
									<td className="py-1.5 pr-3 text-settings-label">
										{entry.action}
										<span className="block text-settings-muted">{category(entry.category)}</span>
									</td>
									<td className="whitespace-nowrap py-1.5 text-settings-label">{decision(entry)}</td>
								</tr>
							))}
						</tbody>
					</table>
				</div>
			) : null}
		</SettingsSection>
	);
}
