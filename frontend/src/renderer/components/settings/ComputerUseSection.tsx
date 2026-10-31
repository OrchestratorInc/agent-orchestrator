import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Check, Copy, ExternalLink, LoaderCircle } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import type { components } from "../../../api/schema";
import { useSettings, useUpdateComputerUse } from "../../hooks/useSettings";
import { apiClient, apiErrorMessage } from "../../lib/api-client";
import { aoBridge } from "../../lib/bridge";
import { Button } from "../ui/button";
import { Switch } from "../ui/switch";
import type { InstallJob } from "./installState";
import { SettingsRow } from "./SettingsRow";
import { SettingsSection } from "./SettingsSection";

type ComputerUseStatus = components["schemas"]["ComputerUseStatus"];
type PermissionState = components["schemas"]["ComputerUsePermissions"]["accessibility"];

export const computerUseStatusQueryKey = ["computer-use", "status"] as const;
const installJobQueryKey = ["system-install", "cua-driver"] as const;
const JOB_POLL_MS = 1_500;
const GRANT_POLL_MS = 2_000;
const RELEASES_URL = "https://cua.ai/docs/how-to-guides/driver/install";
// Privacy & Security panes; must match the main process's allowlist in src/main/external-open.ts.
const PRIVACY_PANE_URLS = {
	accessibility: "x-apple.systempreferences:com.apple.preference.security?Privacy_Accessibility",
	screenRecording: "x-apple.systempreferences:com.apple.preference.security?Privacy_ScreenCapture",
} as const;

async function fetchStatus(): Promise<ComputerUseStatus> {
	const { data, error } = await apiClient.GET("/api/v1/computer-use/status");
	if (error || !data) throw new Error(apiErrorMessage(error));
	return data;
}

async function fetchInstallJob(): Promise<InstallJob> {
	const { data, error } = await apiClient.GET("/api/v1/system/install/{target}", { params: { path: { target: "cua-driver" } } });
	if (error || !data) throw new Error(apiErrorMessage(error));
	return data;
}

/**
 * Cua Driver, the computer-use driver behind the agents' computer-use tools. Installs
 * through the daemon's fixed installer allowlist like the agent CLIs do, then
 * shows the facts that decide whether agents can use it: the background
 * service and, on macOS, the privacy permissions CuaDriver.app holds.
 */
export function ComputerUseSection({ titleHidden }: { titleHidden?: boolean }) {
	const { t } = useTranslation();
	const queryClient = useQueryClient();
	const status = useQuery({
		queryKey: computerUseStatusQueryKey,
		queryFn: fetchStatus,
		// A permission request waits on the user in System Settings; follow it.
		refetchInterval: (query) => (query.state.data?.permissions?.granting ? GRANT_POLL_MS : false),
	});
	const job = useQuery({
		queryKey: installJobQueryKey,
		queryFn: fetchInstallJob,
		refetchInterval: (query) => (query.state.data?.status === "running" ? JOB_POLL_MS : false),
	});
	const [action, setAction] = useState<"install" | "start" | "grant" | null>(null);
	const [actionError, setActionError] = useState<string | null>(null);
	const [copied, setCopied] = useState(false);
	const agentAccess = useUpdateComputerUse();
	// Set when this panel starts a fresh install: installing the driver is asking
	// for agents to use it, so its success turns agent access on. An update
	// leaves the user's choice alone.
	const enableOnInstall = useRef(false);

	// A finished install changes what status reports; refresh it once.
	const jobStatus = job.data?.status;
	const previousJobStatus = useRef(jobStatus);
	useEffect(() => {
		if (previousJobStatus.current === "running" && jobStatus && jobStatus !== "running") {
			void queryClient.invalidateQueries({ queryKey: computerUseStatusQueryKey });
			if (jobStatus === "succeeded" && enableOnInstall.current) agentAccess.update(true);
			enableOnInstall.current = false;
		}
		previousJobStatus.current = jobStatus;
	}, [jobStatus, queryClient, agentAccess]);

	const run = async (name: NonNullable<typeof action>, work: () => Promise<void>) => {
		setAction(name);
		setActionError(null);
		try {
			await work();
		} catch (cause) {
			setActionError(cause instanceof Error ? cause.message : String(cause));
		} finally {
			setAction(null);
		}
	};
	const install = () => run("install", async () => {
		enableOnInstall.current = !installed;
		const { data, error } = await apiClient.POST("/api/v1/system/install/{target}", { params: { path: { target: "cua-driver" } } });
		if (error || !data) throw new Error(apiErrorMessage(error));
		queryClient.setQueryData(installJobQueryKey, data);
	});
	const startService = () => run("start", async () => {
		const { data, error } = await apiClient.POST("/api/v1/computer-use/daemon/start");
		if (error || !data) throw new Error(apiErrorMessage(error));
		queryClient.setQueryData(computerUseStatusQueryKey, data);
	});
	const grant = () => run("grant", async () => {
		const { data, error } = await apiClient.POST("/api/v1/computer-use/permissions/grant");
		if (error || !data) throw new Error(apiErrorMessage(error));
		queryClient.setQueryData(computerUseStatusQueryKey, data);
	});
	const copyCommand = async (command: string) => {
		await navigator.clipboard.writeText(command);
		setCopied(true);
		window.setTimeout(() => setCopied(false), 1_500);
	};

	const current = status.data;
	const installed = Boolean(current?.installed);
	const installing = action === "install" || jobStatus === "running";
	const failed = jobStatus === "failed" || jobStatus === "interrupted";
	// Unsupported with a command: AO knows the command but cannot run it here,
	// for a fresh install or an update alike.
	const needsInstaller = !installed || Boolean(current?.updateAvailable);
	const manualCommand = jobStatus === "unsupported" && needsInstaller ? job.data?.command : undefined;
	const permissions = current?.permissions;

	const driverAction = installing ? (
		<span role="status" className="inline-flex items-center gap-1.5 text-xs text-settings-muted">
			<LoaderCircle className="size-3.5 animate-spin" aria-hidden="true" />
			{t(installed ? "settings.computerUse.updating" : "settings.computerUse.installing")}
		</span>
	) : manualCommand ? (
		<Button type="button" size="sm" variant="ghost" onClick={() => void copyCommand(manualCommand)}>
			{copied ? <Check aria-hidden="true" /> : <Copy aria-hidden="true" />}
			{t(copied ? "settings.harness.copied" : "settings.harness.copyCommand")}
		</Button>
	) : !installed && status.isSuccess ? (
		<Button type="button" size="sm" variant="ghost" onClick={() => void install()}>
			{t(failed ? "settings.computerUse.retryInstall" : "settings.computerUse.install")}
		</Button>
	) : installed && current?.updateAvailable && current.latestVersion ? (
		<Button type="button" size="sm" variant="ghost" onClick={() => void install()}>
			{t("settings.computerUse.update", { version: current.latestVersion })}
		</Button>
	) : installed ? (
		<span className="text-xs text-settings-muted">{t("settings.computerUse.installed")}</span>
	) : null;

	const driverDescription = failed && job.data?.error ? <span className="text-error">{job.data.error}</span>
		: manualCommand ? job.data?.error || t("settings.computerUse.manualInstall")
		: installed && current?.version ? <span className="font-mono">{current.version}</span>
		: t("settings.computerUse.driverDescription");

	return (
		<>
			<SettingsSection title={t("settings.computerUse")} sectionId="computerUse" titleHidden={titleHidden} grouped>
				<SettingsRow label={t("settings.computerUse.driver")} description={driverDescription}>
					{driverAction}
				</SettingsRow>
				<AgentAccessRow installed={installed} access={agentAccess} />
				{installed && current ? (
					<>
						{/* A running service needs no row; only its absence is worth reporting. */}
						{current.daemon !== "running" ? (
							<SettingsRow label={t("settings.computerUse.service")} description={<span className="text-error">{t("settings.computerUse.serviceNotRunning")}</span>}>
								<Button type="button" size="sm" variant="ghost" disabled={action !== null} onClick={() => void startService()}>
									{action === "start" ? <LoaderCircle className="animate-spin" aria-hidden="true" /> : null}
									{t("settings.computerUse.start")}
								</Button>
							</SettingsRow>
						) : null}
						{permissions ? (
							<SettingsRow
								label={t("settings.computerUse.permissions")}
								description={permissions.grantError ? <span className="text-error">{permissions.grantError}</span>
									: current.daemon !== "running" ? t("settings.computerUse.permissionsNeedService")
									// All granted reads from the trailing state alone; otherwise name each one.
									: allGranted(permissions.accessibility, permissions.screenRecording) ? undefined
									: `${t("settings.computerUse.accessibility")}: ${t(`settings.computerUse.permission.${permissions.accessibility}`)} · ${t("settings.computerUse.screenRecording")}: ${t(`settings.computerUse.permission.${permissions.screenRecording}`)}`}
							>
								{permissions.granting ? (
									<span role="status" className="inline-flex items-center gap-1.5 text-xs text-settings-muted">
										<LoaderCircle className="size-3.5 animate-spin" aria-hidden="true" />
										{t("settings.computerUse.granting")}
									</span>
								) : allGranted(permissions.accessibility, permissions.screenRecording) ? (
									// Each opens its own pane, where the user can review or revoke it.
									<span className="flex items-center gap-1">
										{(["accessibility", "screenRecording"] as const).map((pane) => (
											<Button key={pane} type="button" size="sm" variant="ghost" onClick={() => void aoBridge.app.openExternal(PRIVACY_PANE_URLS[pane])}>
												<Check aria-hidden="true" />{t(`settings.computerUse.${pane}`)}
											</Button>
										))}
									</span>
								) : (
									<Button type="button" size="sm" variant="ghost" disabled={action !== null} onClick={() => void grant()}>
										{t("settings.computerUse.grant")}
									</Button>
								)}
							</SettingsRow>
						) : null}
					</>
				) : null}
				{status.isError ? (
					<div className="settings-content-row">
						<p role="alert" className="text-xs text-error">{status.error instanceof Error ? status.error.message : t("settings.computerUse.loadFailed")}</p>
					</div>
				) : null}
			</SettingsSection>

			{actionError ? <p role="alert" className="px-(--settings-row-inset) text-xs text-error">{actionError}</p> : null}

			{(!installed || manualCommand) && status.isSuccess && !installing ? (
				<div className="flex px-(--settings-row-inset)">
					<Button type="button" size="sm" variant="ghost" className="-ml-2.5 text-settings-muted hover:text-settings-label" onClick={() => void aoBridge.app.openExternal(RELEASES_URL)}>
						<ExternalLink aria-hidden="true" />{t("settings.harness.installGuide")}
					</Button>
				</div>
			) : null}
		</>
	);
}

/**
 * "Let agents use this computer": while on, new chat sessions get the
 * computer-use tools and a standing instruction to read their guide first.
 * It needs Cua Driver installed to turn on; once on it can always be turned off.
 */
function AgentAccessRow({ installed, access }: { installed: boolean; access: ReturnType<typeof useUpdateComputerUse> }) {
	const { t } = useTranslation();
	const { settings, isLoading } = useSettings();
	const { update, saving, error } = access;
	const enabled = settings?.computerUse ?? false;
	return (
		<div className="flex w-full flex-col">
			<SettingsRow
				label={t("settings.computerUse.agentAccess")}
				description={!installed && !enabled ? t("settings.computerUse.agentAccessNeedsDriver") : t("settings.computerUse.agentAccessDescription")}
			>
				<Switch
					aria-label={t("settings.computerUse.agentAccess")}
					checked={enabled}
					disabled={isLoading || saving || !settings || (!installed && !enabled)}
					onCheckedChange={update}
				/>
			</SettingsRow>
			{error ? <p role="alert" className="px-3 pb-2 text-caption leading-4 text-error">{error}</p> : null}
		</div>
	);
}

function allGranted(...states: PermissionState[]): boolean {
	return states.every((state) => state === "granted");
}
