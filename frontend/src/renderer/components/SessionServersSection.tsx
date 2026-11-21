import { InspectorSection } from "@aoagents/product-ui";
import { useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { useLoopbackReach, usePortForwards } from "../hooks/useHostLoopback";
import { useSessionBrowserLink } from "../hooks/useSessionBrowserLink";
import { apiErrorException } from "../lib/api-error-exception";
import { clientForSessionHost } from "../lib/host-clients";
import { useUiStore } from "../stores/ui-store";
import { sessionIsActive, type WorkspaceSession } from "../types/workspace";
import { Button } from "./ui/button";
import { Pressable } from "./ui/pressable";

const SERVERS_POLL_MS = 5_000;

export function SessionServersSection({ session, hostId, visible }: { session: WorkspaceSession; hostId?: string; visible: boolean }) {
	const { t } = useTranslation();
	const showGlobalToast = useUiStore((state) => state.showGlobalToast);
	const servers = useQuery({
		queryKey: ["session-servers", hostId ?? "local", session.id],
		queryFn: async () => {
			const { data, error } = await clientForSessionHost(hostId).GET("/api/v1/sessions/{sessionId}/servers", {
				params: { path: { sessionId: session.id } },
			});
			if (error) throw apiErrorException(error);
			return data.servers;
		},
		enabled: visible && sessionIsActive(session),
		refetchInterval: SERVERS_POLL_MS,
		retry: false,
	}).data ?? [];
	const reach = useLoopbackReach(hostId);
	const { forwards, forward, stop } = usePortForwards();
	const openInBrowser = useSessionBrowserLink(session);
	if (servers.length === 0) return null;
	const tunnelHost = hostId && reach === "tunnel" ? hostId : undefined;
	const forwardFailed = (error: unknown) =>
		showGlobalToast(t("sessionServers.forwardFailed"), error instanceof Error ? error.message : undefined, "error");

	return (
		<InspectorSection surface={false} title={t("sessionServers.title")}>
			<ul className="m-0 flex list-none flex-col p-0 text-sm" data-testid="session-servers">
				{servers.map((server) => {
					const forwarded = tunnelHost ? forwards.find((item) => item.hostId === tunnelHost && item.remotePort === server.port) : undefined;
					return (
						<li className="flex min-h-7 items-center gap-2" key={server.port}>
							<Pressable
								aria-label={t("sessionServers.open", { port: server.port })}
								className="flex min-w-0 flex-1 items-center gap-2 rounded-sm hover:text-foreground"
								onClick={() => openInBrowser(`http://localhost:${server.port}/`)}
							>
								<span className="font-mono tabular-nums">{server.port}</span>
								<span className="truncate text-settings-muted" title={server.command}>{server.command}</span>
							</Pressable>
							{forwarded ? (
								<>
									<span className="font-mono text-xs text-settings-muted" title={t("sessionServers.forwardedTitle")}>{`localhost:${forwarded.localPort}`}</span>
									<Button
										aria-label={t("sessionServers.stopLabel", { port: server.port })}
										onClick={() => void stop(forwarded.hostId, forwarded.remotePort)}
										size="xs"
										type="button"
										variant="quiet"
									>
										{t("sessionServers.stop")}
									</Button>
								</>
							) : tunnelHost ? (
								<Button
									aria-label={t("sessionServers.forwardLabel", { port: server.port })}
									onClick={() => void forward(tunnelHost, server.port).catch(forwardFailed)}
									size="xs"
									type="button"
									variant="quiet"
								>
									{t("sessionServers.forward")}
								</Button>
							) : null}
						</li>
					);
				})}
			</ul>
		</InspectorSection>
	);
}
