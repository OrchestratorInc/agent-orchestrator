import { useState } from "react";
import { useTranslation } from "react-i18next";
import { useProviderSwitchCommand } from "../hooks/useConversation";
import { agentLabel } from "../lib/agent-options";
import { apiErrorMessage } from "../lib/api-client";
import { useUiStore } from "../stores/ui-store";
import type { WorkspaceSession } from "../types/workspace";
import { AgentModelPicker, DismissButton, switchEffort, type PendingAgentSwitch } from "./chat/AgentSwitchPicker";
import { ProviderMark } from "./chat/ProviderMark";
import { useCloseMenu } from "./menu-model";
import { Button } from "./ui/button";

const NO_CHOICES = { models: [], modelLabel: "", efforts: [] };

/**
 * A terminal session tab's agent row, in either of the tab's menus. A picked
 * agent adds Switch and Cancel, since a terminal has no next message to wait
 * on; a successful switch closes the menu. A chat picks its agent in the composer.
 */
export function TerminalAgentPickerRow({
	session,
	hostId,
	blocked,
}: {
	session: WorkspaceSession;
	hostId?: string;
	/** The agent is working, so it cannot be replaced right now. */
	blocked: boolean;
}) {
	const { t } = useTranslation();
	const closeMenu = useCloseMenu();
	const [pending, setPending] = useState<PendingAgentSwitch>();
	const providerSwitch = useProviderSwitchCommand(session.id, hostId);
	const showGlobalToast = useUiStore((state) => state.showGlobalToast);
	const agentSwitch = { projectId: session.workspaceId, hostId, pending, onPendingChange: setPending };
	const switchNow = () => {
		if (!pending) return;
		const { harness: targetHarness, model, fastMode, ultracode, autoCompactWindow } = pending;
		void providerSwitch.mutateAsync({ targetHarness, model, effort: switchEffort(pending), fastMode, ultracode, autoCompactWindow })
			.then(() => { setPending(undefined); closeMenu(); })
			.catch((error) => showGlobalToast(t("agentSwitch.switchFailed"), apiErrorMessage(error), "error"));
	};
	const target = pending ? [agentLabel(pending.harness), pending.modelLabel].filter(Boolean).join(" · ") : "";
	// One bordered bar under the picked agent's settings: what it switches to, Switch, cancel.
	const footer = pending ? (
		<div className="mt-1 box-border flex h-10 shrink-0 items-center border-t border-border px-1.5 pt-1">
			<div role="status" className="flex h-7 min-w-0 flex-1 items-center gap-1.5 rounded-lg border border-border bg-interactive-active/50 pl-2 pr-1 text-xs text-foreground">
				<ProviderMark provider={pending.harness} className="size-3.5" />
				{/* The mark names the agent, so the model alone fits beside Switch. */}
				<span className="min-w-0 flex-1 truncate" title={target}>{pending.modelLabel ?? agentLabel(pending.harness)}</span>
				<Button
					type="button"
					disabled={blocked || providerSwitch.isPending}
					title={blocked ? t("agentSwitch.busy") : t("agentSwitch.switchTerminalTo", { target })}
					onClick={switchNow}
					className="disabled:pointer-events-auto disabled:cursor-not-allowed"
					size="xs"
					variant="primary"
				>
					{providerSwitch.isPending ? t("agentSwitch.switchingNow") : t("agentSwitch.switchAction")}
				</Button>
				<DismissButton label={t("agentSwitch.cancel")} disabled={providerSwitch.isPending} onClick={() => setPending(undefined)} />
			</div>
		</div>
	) : null;
	return <AgentModelPicker terminal currentHarness={session.provider} current={NO_CHOICES} agentSwitch={agentSwitch} pendingFooter={footer} />;
}
