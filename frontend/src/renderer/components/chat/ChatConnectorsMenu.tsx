import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { Plug, Settings2 } from "@/components/app-icons";
import { cn } from "@/lib/utils";
import { connectorsApi, sessionConnectorsQueryKey, type SessionConnector } from "../../hooks/useConnectors";
import { useUiStore } from "../../stores/ui-store";
import type { McpServer } from "../../types/conversation";
import { Button } from "../ui/button";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger } from "../ui/dropdown-menu";
import { CHAT_MENU_CLASS, TRIGGER_CLASS } from "./TurnSettingsBar";

type RowState = "off" | "needsSignIn" | "failed" | "starting" | "loaded" | "added";

/** What a chat's connector is doing: Orchestrator knows the sign-in, the agent reports whether it loaded the server. */
export function connectorRowState(connector: Pick<SessionConnector, "enabled" | "status">, reported?: Pick<McpServer, "status">): RowState {
	if (!connector.enabled) return "off";
	if (connector.status === "needs_signin") return "needsSignIn";
	if (reported?.status === "failed" || reported?.status === "cancelled") return "failed";
	if (reported?.status === "starting") return "starting";
	return reported?.status === "ready" ? "loaded" : "added";
}

const stateKeys = {
	off: "settings.connectors.status.off",
	needsSignIn: "settings.connectors.status.needsSignIn",
	failed: "chat.connectors.failed",
	starting: "chat.connectors.starting",
	loaded: "chat.connectors.loaded",
	added: "chat.connectors.added",
} as const;

/**
 * The connectors this chat started with: their state, and how many calls the
 * chat made to each server URL connector. Calls to a local command do not pass
 * through the daemon, so that row names its kind and shows no count. Nothing
 * shows for a chat without connectors.
 */
export function ChatConnectorsMenu({ sessionId, servers }: { sessionId: string; servers: readonly McpServer[] }) {
	const { t } = useTranslation();
	const [open, setOpen] = useState(false);
	const query = useQuery({
		queryKey: sessionConnectorsQueryKey(sessionId),
		queryFn: () => connectorsApi.forSession(sessionId),
		refetchInterval: open ? 3000 : false,
		staleTime: 30_000,
	});
	const connectors = query.data ?? [];
	if (connectors.length === 0) return null;
	const reported = new Map(servers.map((server) => [server.name, server]));
	const rows = connectors.map((connector) => ({ connector, report: reported.get(connector.key), state: connectorRowState(connector, reported.get(connector.key)) }));
	const attention = rows.some((row) => row.state === "needsSignIn" || row.state === "failed");
	const label = t("chat.connectors.chip", { count: connectors.length });

	return (
		<DropdownMenu open={open} onOpenChange={setOpen}>
			<DropdownMenuTrigger asChild>
				<Button type="button" variant="ghost" size="sm" className={cn(TRIGGER_CLASS, "px-2")} aria-label={label} title={label} data-chat-connectors>
					<Plug className={cn("size-3.5", attention && "text-warning")} aria-hidden="true" />
					<span className="tabular-nums">{connectors.length}</span>
				</Button>
			</DropdownMenuTrigger>
			<DropdownMenuContent align="start" className={cn(CHAT_MENU_CLASS, "min-w-64 max-w-80")}>
				<div role="list" aria-label={label} className="flex flex-col">
					{rows.map(({ connector, report, state }) => (
						<div key={connector.key} role="listitem" className="flex items-start justify-between gap-3 px-2 py-1.5">
							<span className="min-w-0">
								<span className="block truncate text-foreground">{connector.name}</span>
								<span className="block truncate text-muted-foreground">
									{connector.transport === "stdio"
										? t("settings.connectors.dialog.tabCommand")
										: connector.calls > 0
											? `${t("chat.connectors.calls", { count: connector.calls })}${connector.lastTool ? ` · ${connector.lastTool}` : ""}`
											: t("chat.connectors.noCalls")}
								</span>
							</span>
							<span
								className={cn("shrink-0", state === "failed" ? "text-error" : state === "needsSignIn" ? "text-warning" : "text-muted-foreground")}
								title={state === "failed" ? report?.error : undefined}
							>
								{t(stateKeys[state])}
							</span>
						</div>
					))}
				</div>
				<DropdownMenuSeparator />
				<DropdownMenuItem onSelect={() => useUiStore.getState().openGlobalSettings("connectors", { preserveProject: true })}>
					<Settings2 aria-hidden="true" />
					{t("chat.connectors.manage")}
				</DropdownMenuItem>
			</DropdownMenuContent>
		</DropdownMenu>
	);
}
