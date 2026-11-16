import { useTranslation } from "react-i18next";
import { RotateCw, WifiOff } from "@/components/app-icons";
import { Button } from "./ui/button";
import { Tooltip, TooltipContent, TooltipTrigger } from "./ui/tooltip";
import { noDragStyle } from "./PageTopbar";
import { formatTimeCompact } from "../lib/format-time";
import { useShellMaybe } from "../lib/shell-context";
import { useUiStore } from "../stores/ui-store";
import type { RemoteHost } from "../hooks/useRemoteHosts";

export function hostOfflineReasonKey(host: RemoteHost | undefined) {
	return host?.failureReason === "unauthorized" ? "remote.hostUnauthorized" as const
		: host?.failureReason === "incompatible" ? "remote.hostIncompatible" as const
		: "remote.hostOffline" as const;
}

export function HostOfflineStatus({ host, label, updatedAt }: { host: RemoteHost | undefined; label: string; updatedAt?: number }) {
	const { t } = useTranslation();
	const shell = useShellMaybe();
	const action = host?.status === "connecting"
		? <Button type="button" size="sm" variant="secondary" disabled>{t("terminal.connecting")}</Button>
		: host?.failureReason === "unauthorized"
			? <Button type="button" size="sm" variant="secondary" onClick={() => useUiStore.getState().openGlobalSettings("remoteHosts", { preserveProject: true })}>{t("remote.editHost", { label })}</Button>
			: <Button type="button" size="sm" variant="secondary" aria-label={t("remote.retryHost", { label })} disabled={!shell?.retryRemoteHosts} onClick={() => shell?.retryRemoteHosts?.()}>
				<RotateCw className="size-3.5" aria-hidden="true" />
				{t("remote.retry")}
			</Button>;
	return (
		<div className="flex min-w-0 items-center gap-2 text-xs" data-testid="host-offline" role="status" style={noDragStyle}>
			<Tooltip>
				<TooltipTrigger asChild>
					<span className="inline-flex shrink-0 items-center gap-1.5 text-warning" tabIndex={0}>
						<WifiOff className="size-icon-sm" aria-hidden="true" />
						{t("remote.offline")}
					</span>
				</TooltipTrigger>
				<TooltipContent className="max-w-72">{t(hostOfflineReasonKey(host))}{updatedAt ? ` ${t("remote.offlineCopy")}` : ""}</TooltipContent>
			</Tooltip>
			{updatedAt ? <span className="truncate text-muted-foreground">· {t("shell.updatedAt", { time: formatTimeCompact(new Date(updatedAt).toISOString()) })}</span> : null}
			{action}
		</div>
	);
}

export function HostAwayBanner({ hostId }: { hostId: string }) {
	const host = useShellMaybe()?.remoteHosts?.find((candidate) => candidate.hostId === hostId);
	return (
		<div className="flex shrink-0 items-center border-b border-border bg-surface px-4 py-2">
			<HostOfflineStatus host={host} label={host?.label ?? hostId} />
		</div>
	);
}
