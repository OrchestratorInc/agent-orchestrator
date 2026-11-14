import { Fragment, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { AlertTriangle, Folder } from "@/components/app-icons";
import type { RemoteHost } from "../hooks/useRemoteHosts";
import { hostUpdateAction, startHostUpdate, useHostUpdateStore } from "../hooks/useHostUpdates";
import type { WorkspaceSummary } from "../types/workspace";
import { SidebarMenuButton, SidebarMenuItem } from "./ui/sidebar";

type Props = {
	hosts: RemoteHost[];
	workspaces: WorkspaceSummary[];
	failedHostIds?: string[];
	renderProject: (host: RemoteHost, workspace: WorkspaceSummary) => ReactNode;
	onRetry: () => void;
};

/** Only host-specific chrome lives here; project and session rows are shared. */
export function RemoteHostsSection({ hosts, workspaces, failedHostIds = [], renderProject, onRetry }: Props) {
	const { t } = useTranslation();
	const localVersion = useHostUpdateStore((state) => state.localVersion);
	const hostUpdates = useHostUpdateStore((state) => state.byUrl);
	return <>
		{hosts.map((host) => {
			const projects = workspaces.filter((workspace) => workspace.hostId === host.hostId);
			const incompatible = host.status === "offline" && host.failureReason === "incompatible";
			const hostVersion = incompatible ? hostUpdates[host.url]?.view?.version : undefined;
			const update = incompatible ? hostUpdateAction(hostUpdates[host.url], localVersion) : null;
			const offlineTitle = !incompatible ? t("remote.hostOffline")
				: hostVersion && localVersion ? t("remote.hostVersionMismatch", { host: hostVersion, local: localVersion })
				: t("remote.hostIncompatible");
			const offlineAction = update?.kind === "updating" ? t("shell.updating")
				: update?.kind === "update" || update?.kind === "failed" ? t("remote.updateHost")
				: host.failureReason === "unauthorized" ? t("remote.passwordRejected") : incompatible ? t("remote.updateAO") : t("remote.retry");
			// While the host connects or is offline, its last lists show under its status;
			// a host that refused this device's password shows nothing it kept.
			if (host.status !== "connected") return <Fragment key={host.url}><SidebarMenuItem data-host-id={host.hostId}>
				<SidebarMenuButton
					aria-label={host.status !== "offline" ? undefined : update?.kind === "update" || update?.kind === "failed" ? t("shell.updateMachine", { name: host.label }) : t("remote.retryHost", { label: host.label })}
					className="h-9 gap-2 rounded-lg px-2.5 text-sm text-muted-foreground hover:bg-interactive-hover [&_svg]:size-icon-md"
					disabled={host.status === "connecting" || update?.kind === "updating"}
					onClick={update?.kind === "update" || update?.kind === "failed" ? () => void startHostUpdate(host.url) : onRetry}
					title={host.status === "offline" ? offlineTitle : undefined}
				>
					{host.status === "offline" ? <AlertTriangle aria-hidden="true" /> : <Folder aria-hidden="true" />}
					<span className="truncate">{host.label}</span>
					<span className="ml-auto text-xs">{host.status === "connecting" ? t("terminal.connecting") : offlineAction}</span>
				</SidebarMenuButton>
			</SidebarMenuItem>
				{host.failureReason !== "unauthorized" && projects.map((workspace) => <Fragment key={`${host.hostId}:${workspace.id}`}>{renderProject(host, workspace)}</Fragment>)}
			</Fragment>;
			return <Fragment key={host.url}>
				{projects.map((workspace) => <Fragment key={`${host.hostId}:${workspace.id}`}>{renderProject(host, workspace)}</Fragment>)}
				{failedHostIds.includes(host.hostId) && <SidebarMenuItem>
					<SidebarMenuButton className="h-8 rounded-lg px-2.5 text-left text-xs text-destructive hover:bg-interactive-hover" onClick={onRetry}>{t("remoteHosts.loadFailed")}</SidebarMenuButton>
				</SidebarMenuItem>}
			</Fragment>;
		})}
	</>;
}
