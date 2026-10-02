import { Fragment, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { AlertTriangle, Folder, FolderPlus } from "lucide-react";
import type { RemoteHost } from "../hooks/useRemoteHosts";
import { STANDALONE_WORKSPACE_ID, type WorkspaceSummary } from "../types/workspace";
import { SidebarMenuButton, SidebarMenuItem } from "./ui/sidebar";

type Props = {
	hosts: RemoteHost[];
	workspaces: WorkspaceSummary[];
	failedHostIds?: string[];
	loadedProjectHostIds?: string[];
	renderProject: (host: RemoteHost, workspace: WorkspaceSummary) => ReactNode;
	onAddProject: (hostId: string) => void;
	onRetry: () => void;
};

/** Only host-specific chrome lives here; project and session rows are shared. */
export function RemoteHostsSection({ hosts, workspaces, failedHostIds = [], loadedProjectHostIds = [], renderProject, onAddProject, onRetry }: Props) {
	const { t } = useTranslation();
	return <>
		{hosts.map((host) => {
			if (host.status !== "connected") return <SidebarMenuItem key={host.url} data-host-id={host.hostId}>
				<SidebarMenuButton
					aria-label={host.status === "offline" ? t("remote.retryHost", { label: host.label }) : undefined}
					className="h-9 gap-2 rounded-lg px-2.5 text-sm text-muted-foreground hover:bg-interactive-hover [&_svg]:size-icon-md"
					disabled={host.status === "connecting"}
					onClick={onRetry}
					title={host.status === "offline" ? t(host.failureReason === "incompatible" ? "remote.hostIncompatible" : "remote.hostOffline") : undefined}
				>
					{host.status === "offline" ? <AlertTriangle aria-hidden="true" /> : <Folder aria-hidden="true" />}
					<span className="truncate">{host.label}</span>
					<span className="ml-auto text-xs">{host.status === "connecting" ? t("terminal.connecting") : host.failureReason === "unauthorized" ? t("remote.passwordRejected") : host.failureReason === "incompatible" ? t("remote.updateAO") : t("remote.retryHost", { label: host.label })}</span>
				</SidebarMenuButton>
			</SidebarMenuItem>;
			const projects = workspaces.filter((workspace) => workspace.hostId === host.hostId);
			const projectCount = projects.filter((workspace) => workspace.id !== STANDALONE_WORKSPACE_ID).length;
			return <Fragment key={host.url}>
				{projects.map((workspace) => <Fragment key={`${host.hostId}:${workspace.id}`}>{renderProject(host, workspace)}</Fragment>)}
				<SidebarMenuItem>
					<SidebarMenuButton
						aria-label={t("remote.addProjectOnHost", { label: host.label, defaultValue: "Add project on {{label}}" })}
						className="h-8 gap-2 rounded-lg px-2.5 text-sm text-muted-foreground hover:bg-interactive-hover hover:text-foreground [&_svg]:size-icon-md"
						onClick={() => onAddProject(host.hostId)}
					>
						<FolderPlus aria-hidden="true" />
						<span className="truncate">{t("remote.addProjectOnHost", { label: host.label, defaultValue: "Add project on {{label}}" })}</span>
						{loadedProjectHostIds.includes(host.hostId) && <span className="ml-auto shrink-0 text-xs tabular-nums">{t("remote.projectCount", { count: projectCount, defaultValue: projectCount === 1 ? "{{count}} project" : "{{count}} projects" })}</span>}
					</SidebarMenuButton>
				</SidebarMenuItem>
				{failedHostIds.includes(host.hostId) && <SidebarMenuItem>
					<SidebarMenuButton className="h-8 rounded-lg px-2.5 text-left text-xs text-destructive hover:bg-interactive-hover" onClick={onRetry}>{t("remoteHosts.loadFailed")}</SidebarMenuButton>
				</SidebarMenuItem>}
			</Fragment>;
		})}
	</>;
}
