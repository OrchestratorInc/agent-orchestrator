import { useQuery, useQueryClient } from "@tanstack/react-query";
import { ExternalLink, LoaderCircle, RefreshCw } from "@/components/app-icons";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import {
	agentAccountConnectorsQueryKey,
	agentAccountLabel,
	fetchAgentAccountConnectors,
	oneRowPerLogin,
	useAgentAccounts,
	type AgentAccountConnector,
} from "../../../hooks/useAgentAccounts";
import { aoBridge } from "../../../lib/bridge";
import type { HostId } from "../../../lib/hosts";
import { Button } from "../../ui/button";
import { SettingsOptionMenu } from "../SettingsOptionMenu";
import { HarnessDetailGroup, HarnessDetailRow, HarnessStateText, HarnessTimestamp, hasValidTimestamp, type HarnessTone } from "./harness-ui";

/** Where a claude.ai account adds connectors and signs in to them. */
export const CLAUDE_CONNECTORS_URL = "https://claude.ai/settings/connectors";

const connectorTone: Record<AgentAccountConnector["status"], HarnessTone> = {
	connected: "ok",
	needs_auth: "off",
	failed: "error",
	unknown: "off",
};

/**
 * The claude.ai connectors (Gmail, Calendar, Drive, ...) each Claude Code
 * account's sessions load. Connectors belong to the claude.ai account, so the
 * list is per Orchestrator account; it is read with that account's own login,
 * which takes seconds, and the daemon reuses a reading for ten minutes.
 */
export function HarnessConnectorsTab({ hostId, onOpenAccount }: { hostId?: HostId; onOpenAccount: () => void }) {
	const { t } = useTranslation();
	const queryClient = useQueryClient();
	const accountsQuery = useAgentAccounts(true, hostId);
	const accounts = oneRowPerLogin((accountsQuery.data ?? []).filter((account) => account.provider === "claude"));
	const [pickedId, setPickedId] = useState<string>();
	const account = accounts.find((candidate) => candidate.id === pickedId)
		?? accounts.find((candidate) => candidate.default)
		?? accounts[0];
	const accountId = account?.id;
	const queryKey = agentAccountConnectorsQueryKey(accountId ?? "", hostId);
	const connectors = useQuery({
		queryKey,
		queryFn: () => fetchAgentAccountConnectors(accountId!, false, hostId),
		enabled: Boolean(accountId),
		staleTime: 60_000,
	});
	const [checking, setChecking] = useState(false);
	const [checkError, setCheckError] = useState<string>();
	const checkAgain = async () => {
		if (!accountId) return;
		setChecking(true);
		setCheckError(undefined);
		try {
			queryClient.setQueryData(queryKey, await fetchAgentAccountConnectors(accountId, true, hostId));
		} catch (error) {
			setCheckError(error instanceof Error ? error.message : String(error));
		} finally {
			setChecking(false);
		}
	};
	const manage = () => void aoBridge.app.openExternal(CLAUDE_CONNECTORS_URL);

	if (accountsQuery.isPending) return <ConnectorsLoading label={t("settings.harness.connectors.loadingAccounts")} />;
	if (accountsQuery.error && !accountsQuery.data) {
		return <HarnessDetailGroup title={t("settings.harness.connectors.title")}>
			<HarnessDetailRow label={t("settings.harness.connectors.accountsFailed")} description={accountsQuery.error.message}>
				<Button type="button" size="sm" variant="secondary" onClick={() => void accountsQuery.refetch()}>
					{t("settings.harness.retry")}
				</Button>
			</HarnessDetailRow>
		</HarnessDetailGroup>;
	}
	if (!account) {
		return <p className="text-sm text-settings-muted">{t("settings.harness.connectors.noAccount")}</p>;
	}

	const reading = connectors.data;
	const busy = checking || connectors.isFetching;
	const picker = accounts.length > 1 ? <SettingsOptionMenu
		aria-label={t("settings.harness.connectors.account")}
		value={account.id}
		options={accounts.map((candidate) => ({ value: candidate.id, label: agentAccountLabel(candidate, accounts, t) }))}
		onChange={(id) => { setPickedId(id); setCheckError(undefined); }}
		disabled={checking}
		menuAlign="end"
	/> : null;

	return <div className="flex flex-col gap-6">
		<HarnessDetailGroup title={t("settings.harness.connectors.title")} action={picker ?? undefined}>
			{connectors.isPending ? <HarnessDetailRow label={<ConnectorsLoading label={t("settings.harness.connectors.checking")} />} />
				: reading?.unavailable ? <HarnessDetailRow
					label={t(`settings.harness.connectors.unavailable.${reading.unavailable}.title`)}
					description={t(`settings.harness.connectors.unavailable.${reading.unavailable}.description`)}
				>
					{reading.unavailable === "unsupported" ? null : <Button type="button" size="sm" variant="secondary" onClick={onOpenAccount}>
						{t("settings.harness.connectors.unavailable.action")}
					</Button>}
				</HarnessDetailRow>
				: connectors.error ? <HarnessDetailRow label={t("settings.harness.connectors.loadFailed")} description={connectors.error.message} />
				: reading?.error && reading.connectors.length === 0 ? <HarnessDetailRow label={t("settings.harness.connectors.checkFailed")} description={reading.error} />
				: reading && reading.connectors.length === 0 ? <HarnessDetailRow
					label={t("settings.harness.connectors.empty")}
					description={t("settings.harness.connectors.emptyDescription")}
				/>
				: reading?.connectors.map((connector) => <HarnessDetailRow key={connector.name} label={connector.name}>
					{connector.status === "needs_auth" ? <Button type="button" size="sm" variant="ghost" className="h-6 px-1.5 text-xs" onClick={manage}>
						{t("settings.harness.connectors.signIn")}<ExternalLink aria-hidden="true" />
					</Button> : null}
					<HarnessStateText tone={connectorTone[connector.status]}>{t(`settings.harness.connectors.status.${connector.status}`)}</HarnessStateText>
				</HarnessDetailRow>)}
		</HarnessDetailGroup>
		{(reading || connectors.error) && !reading?.unavailable ? <div className="flex flex-wrap items-center gap-2">
			<Button type="button" size="sm" variant="secondary" disabled={busy} onClick={() => void checkAgain()}>
				{busy ? <LoaderCircle className="animate-spin" aria-hidden="true" /> : <RefreshCw aria-hidden="true" />}
				{t(busy ? "settings.harness.connectors.checking" : "settings.harness.health.checkAgain")}
			</Button>
			<Button type="button" size="sm" variant="ghost" onClick={manage}>
				{t("settings.harness.connectors.manage")}<ExternalLink aria-hidden="true" />
			</Button>
			{reading && hasValidTimestamp(reading.checkedAt) ? <span className="ml-auto text-xs text-settings-muted">
				{t("settings.harness.connectors.checkedAt")} <HarnessTimestamp value={reading.checkedAt} />
			</span> : null}
		</div> : null}
		{checkError ? <p role="alert" className="text-xs text-error">{checkError}</p> : null}
	</div>;
}

function ConnectorsLoading({ label }: { label: string }) {
	return <span className="inline-flex items-center gap-2 text-sm text-settings-muted">
		<LoaderCircle className="size-3.5 animate-spin" aria-hidden="true" />{label}
	</span>;
}
