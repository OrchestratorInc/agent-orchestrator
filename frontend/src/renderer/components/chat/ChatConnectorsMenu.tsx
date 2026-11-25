import { useQuery } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { Plug, Settings2 } from "@/components/app-icons";
import { cn } from "@/lib/utils";
import { connectorsApi, sessionConnectorsQueryKey, type SessionConnector } from "../../hooks/useConnectors";
import { ConnectorSignInDialog } from "../settings/ConnectorSignInDialog";
import { useUiStore } from "../../stores/ui-store";
import type { McpServer } from "../../types/conversation";
import { Button } from "../ui/button";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuLabel, DropdownMenuSeparator, DropdownMenuTrigger } from "../ui/dropdown-menu";
import { CHAT_MENU_CLASS, TRIGGER_CLASS } from "./TurnSettingsBar";

type RowState = "off" | "needsSignIn" | "failed" | "starting" | "loaded" | "added";

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

const ORCHESTRATOR_SERVERS: ReadonlySet<string> = new Set(["ao"]);

function GroupLabel({ text, count }: { text: string; count: number }) {
	return (
		<DropdownMenuLabel className="flex items-center justify-between gap-3">
			<span className="min-w-0 truncate">{text}</span>
			<span className="tabular-nums">{count}</span>
		</DropdownMenuLabel>
	);
}

function RowStateLabel({ state, error }: { state: RowState; error?: string }) {
	const { t } = useTranslation();
	return (
		<span
			className={cn("shrink-0", state === "failed" ? "text-error" : state === "needsSignIn" ? "text-warning" : "text-muted-foreground")}
			title={state === "failed" ? error : undefined}
		>
			{t(stateKeys[state])}
		</span>
	);
}

export function ChatConnectorsMenu({ sessionId, hostId, agentName, servers }: { sessionId: string; hostId?: string; agentName: string; servers: readonly McpServer[] }) {
	const { t } = useTranslation();
	const [open, setOpen] = useState(false);
	const [signingIn, setSigningIn] = useState<SessionConnector | null>(null);
	const query = useQuery({
		queryKey: sessionConnectorsQueryKey(sessionId, hostId),
		queryFn: () => connectorsApi.forSession(sessionId, hostId),
		refetchInterval: open ? 3000 : false,
		staleTime: 30_000,
	});
	const serverCount = servers.length;
	const { refetch } = query;
	useEffect(() => {
		if (serverCount > 0) void refetch({ cancelRefetch: false });
	}, [serverCount, refetch]);
	const connectors = query.data ?? [];
	const keys = new Set(connectors.map((connector) => connector.key));
	const own = servers.filter((server) => !keys.has(server.name) && !ORCHESTRATOR_SERVERS.has(server.name) && server.status !== "disabled");
	const total = connectors.length + own.length;
	if (total === 0) return null;
	const reported = new Map(servers.map((server) => [server.name, server]));
	const rows = connectors.map((connector) => ({ connector, report: reported.get(connector.key), state: connectorRowState(connector, reported.get(connector.key)) }));
	const attention = rows.some((row) => row.state === "needsSignIn" || row.state === "failed");
	const label = t("chat.connectors.chip", { count: total });
	const fromOrchestrator = t("chat.connectors.fromOrchestrator");
	const fromAgent = t("chat.connectors.fromAgent", { agent: agentName });

	return (
		<>
		<DropdownMenu open={open} onOpenChange={setOpen}>
			<DropdownMenuTrigger asChild>
				<Button type="button" variant="ghost" size="sm" className={cn(TRIGGER_CLASS, "px-2")} aria-label={label} title={label} data-chat-connectors>
					<Plug className={cn("size-3.5", attention && "text-warning")} aria-hidden="true" />
					<span className="tabular-nums">{total}</span>
				</Button>
			</DropdownMenuTrigger>
			<DropdownMenuContent align="start" className={cn(CHAT_MENU_CLASS, "min-w-64 max-w-80")}>
				<div className="flex max-h-80 flex-col overflow-y-auto">
					{rows.length > 0 ? (
						<>
							{own.length > 0 ? <GroupLabel text={fromOrchestrator} count={rows.length} /> : null}
							<div role="list" aria-label={own.length > 0 ? fromOrchestrator : label} className="flex flex-col">
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
										{state === "needsSignIn" && connector.auth === "oauth" ? (
											<Button
												type="button"
												variant="secondary"
												size="xs"
												className="shrink-0"
												aria-label={t("chat.connectors.signIn", { name: connector.name })}
												onClick={() => {
													setOpen(false);
													setSigningIn(connector);
												}}
											>
												{t("settings.connectors.signIn")}
											</Button>
										) : (
											<RowStateLabel state={state} error={report?.error} />
										)}
									</div>
								))}
							</div>
						</>
					) : null}
					{own.length > 0 ? (
						<>
							{rows.length > 0 ? <DropdownMenuSeparator /> : null}
							<GroupLabel text={fromAgent} count={own.length} />
							<div role="list" aria-label={fromAgent} className="flex flex-col">
								{own.map((server) => (
									<div key={server.name} role="listitem" className="flex items-start justify-between gap-3 px-2 py-1.5">
										<span className="min-w-0 truncate text-foreground">{server.name}</span>
										<RowStateLabel state={connectorRowState({ enabled: true, status: server.status === "needs-auth" ? "needs_signin" : "connected" }, server)} error={server.error} />
									</div>
								))}
							</div>
						</>
					) : null}
				</div>
				{hostId ? null : (
					<>
						<DropdownMenuSeparator />
						<DropdownMenuItem onSelect={() => useUiStore.getState().openGlobalSettings("connectors", { preserveProject: true })}>
							<Settings2 aria-hidden="true" />
							{t("chat.connectors.manage")}
						</DropdownMenuItem>
					</>
				)}
			</DropdownMenuContent>
		</DropdownMenu>
		{signingIn ? (
			<ConnectorSignInDialog
				connector={{ id: signingIn.key, name: signingIn.name, signedIn: false }}
				hostId={hostId}
				onClose={() => {
					setSigningIn(null);
					void refetch();
				}}
			/>
		) : null}
		</>
	);
}
