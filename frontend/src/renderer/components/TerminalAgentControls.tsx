import { MoreHorizontal } from "lucide-react";
import { useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { useProviderSwitchCommand } from "../hooks/useConversation";
import { agentLabel } from "../lib/agent-options";
import { apiErrorMessage } from "../lib/api-client";
import { useUiStore } from "../stores/ui-store";
import type { WorkspaceSession } from "../types/workspace";
import { AgentModelPicker, DismissButton, switchEffort, type PendingAgentSwitch } from "./chat/AgentSwitchPicker";
import { ProviderMark } from "./chat/ProviderMark";
import { RoundedInterfaceIcon } from "./SessionInterfaceSwitch";
import { TopbarButton } from "./TopbarButton";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "./ui/dropdown-menu";

const NO_CHOICES = { models: [], modelLabel: "", efforts: [] };

/**
 * A terminal session tab's menu: the agent picker and the switch to chat. A picked agent
 * adds Switch and Cancel, since a terminal has no next message to wait on.
 */
export function TerminalSessionMenu({
	session,
	hostId,
	blocked,
	switchAction,
	switchControl,
}: {
	session: WorkspaceSession;
	hostId?: string;
	/** The agent is working, so it cannot be replaced right now. */
	blocked: boolean;
	switchAction?: { disabled: boolean; reason?: string; run: () => void };
	/** Shown in place of the menu while a switch to chat is running. */
	switchControl?: ReactNode;
}) {
	const { t } = useTranslation();
	const [open, setOpen] = useState(false);
	const [pending, setPending] = useState<PendingAgentSwitch>();
	const providerSwitch = useProviderSwitchCommand(session.id, hostId);
	const showGlobalToast = useUiStore((state) => state.showGlobalToast);
	// Agent switching ships behind Developer Mode for its first week.
	const switching = useUiStore((state) => state.developerMode);
	const agentSwitch = { projectId: session.workspaceId, hostId, pending, onPendingChange: setPending };
	if (!switchAction && (switchControl || !switching)) return switchControl ? <div className="inline-flex shrink-0 items-center">{switchControl}</div> : null;
	const switchNow = () => {
		if (!pending) return;
		const { harness: targetHarness, model, fastMode, ultracode, autoCompactWindow } = pending;
		void providerSwitch.mutateAsync({ targetHarness, model, effort: switchEffort(pending), fastMode, ultracode, autoCompactWindow })
			.then(() => { setPending(undefined); setOpen(false); })
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
				<button
					type="button"
					disabled={blocked || providerSwitch.isPending}
					title={blocked ? t("agentSwitch.busy") : t("agentSwitch.switchTerminalTo", { target })}
					onClick={switchNow}
					className="shrink-0 rounded-md bg-primary px-2 py-px text-[11px] font-medium text-primary-foreground hover:bg-primary/90 disabled:cursor-not-allowed disabled:opacity-40"
				>
					{providerSwitch.isPending ? t("agentSwitch.switchingNow") : t("agentSwitch.switchAction")}
				</button>
				<DismissButton label={t("agentSwitch.cancel")} disabled={providerSwitch.isPending} onClick={() => setPending(undefined)} />
			</div>
		</div>
	) : null;
	return (
		<DropdownMenu open={open} onOpenChange={setOpen}>
			<DropdownMenuTrigger asChild>
				<TopbarButton
					aria-label={t("session.actionsMenu")}
					className="size-7 !bg-transparent text-muted-foreground hover:!bg-transparent active:!bg-transparent focus:!bg-transparent data-[state=open]:!bg-transparent hover:text-foreground"
					data-session-actions-trigger
					title={t("session.actionsMenu")}
					type="button"
					variant="icon"
				>
					<MoreHorizontal aria-hidden="true" className="size-icon-md" />
				</TopbarButton>
			</DropdownMenuTrigger>
			<DropdownMenuContent align="start" className="min-w-48">
				{switching ? <AgentModelPicker terminal currentHarness={session.provider} current={NO_CHOICES} agentSwitch={agentSwitch} pendingFooter={footer} /> : null}
				{switchAction ? (
					<DropdownMenuItem disabled={switchAction.disabled} title={switchAction.reason} onSelect={switchAction.run}>
						<RoundedInterfaceIcon target="chat" className="size-icon-lg" />
						{t("agentSwitch.switchToChat")}
					</DropdownMenuItem>
				) : null}
			</DropdownMenuContent>
		</DropdownMenu>
	);
}
