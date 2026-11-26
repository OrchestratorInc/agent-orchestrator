import { useState } from "react";
import { useTranslation } from "react-i18next";
import { useRefreshAfterEndpointChange, type AgentEndpointStatus } from "../../../hooks/useAgentEndpoint";
import { agentLabel, type AgentId } from "../../../lib/agent-options";
import { Button } from "../../ui/button";
import { AgentEndpointDialog, endpointHost } from "../AgentEndpointDialog";
import { HarnessDetailGroup, HarnessDetailRow, HarnessStatusDot } from "./harness-ui";

/** Lets a harness that supports it send requests to the user's own API
	* endpoint and key instead of its native sign-in. */
export function HarnessCustomEndpointGroup({ agentId, endpoint, hostId, disabled }: {
	agentId: AgentId;
	endpoint: AgentEndpointStatus;
	hostId?: string;
	disabled?: boolean;
}) {
	const { t } = useTranslation();
	const [open, setOpen] = useState(false);
	const refresh = useRefreshAfterEndpointChange(hostId);
	// A saved endpoint that cannot be used stops launches, so it reads as a
	// problem the user can fix with Edit or Remove, never as "Not set".
	const unusable = Boolean(endpoint.error);
	const saved = endpoint.configured || unusable;
	return <HarnessDetailGroup title={t("settings.harness.endpoint.title")}>
		<HarnessDetailRow
			label={unusable
				? <span className="inline-flex items-center gap-2"><HarnessStatusDot tone="error" />{t("settings.harness.endpoint.unusable")}</span>
				: endpoint.configured
					? <span className="inline-flex items-center gap-2"><HarnessStatusDot tone="ok" />{t("settings.harness.endpoint.active", { host: endpointHost(endpoint.baseUrl) })}</span>
					: t("settings.harness.endpoint.notSet")}
			description={t(unusable ? "settings.harness.endpoint.unusableSummary" : "settings.harness.endpoint.summary")}
		>
			<Button type="button" size="default" variant="secondary" className="min-w-20" disabled={disabled} onClick={() => setOpen(true)}>
				{t(saved ? "settings.harness.endpoint.edit" : "settings.harness.endpoint.open")}
			</Button>
		</HarnessDetailRow>
		<AgentEndpointDialog
			agentId={agentId}
			agentName={agentLabel(agentId)}
			endpoint={endpoint}
			hostId={hostId}
			open={open}
			onOpenChange={setOpen}
			onChanged={() => refresh(agentId)}
		/>
	</HarnessDetailGroup>;
}
