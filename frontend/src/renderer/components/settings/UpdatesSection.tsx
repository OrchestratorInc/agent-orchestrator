import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useCallback, useEffect, useLayoutEffect, useRef, useState, type ReactNode } from "react";
import { AlertTriangle, CheckCircle2, Download, Info, Loader2, RefreshCw } from "@/components/app-icons";
import { useTranslation } from "react-i18next";
import { aoBridge } from "../../lib/bridge";
import { cn } from "../../lib/utils";
import { parseNightlyVersion } from "../../lib/build-channel";
import { useUiStore } from "../../stores/ui-store";
import { useRequestUpdateInstall } from "../../hooks/useRequestUpdateInstall";
import { useUpdateStatus, requestUpdateDownload } from "../../hooks/useUpdateStatus";
import type { UpdateChannel, UpdateSettings, UpdateState, UpdateStatus } from "../../../main/update-settings";
import { localizedUpdateError } from "../../lib/update-error-copy";
import { Button } from "../ui/button";
import { Switch } from "../ui/switch";
import { ConfirmDialog } from "../ConfirmDialog";
import { DesktopReleaseNotes } from "../DesktopReleaseNotes";
import { SettingsOptionMenu } from "./SettingsOptionMenu";
import { SettingsRow } from "./SettingsRow";
import { SettingsSection } from "./SettingsSection";
import { captureRendererEvent, releaseChannelFrom, setReleaseChannelContext } from "../../lib/telemetry";

export const updateSettingsQueryKey = ["update-settings"] as const;

type PrimaryValue = UpdateChannel | "feature";

const DEFAULT_SETTINGS: UpdateSettings = { enabled: false, channel: "latest", nightlyAck: false, feature: null };
const MIN_MANUAL_CHECK_VISIBLE_MS = 1_000;
const MAX_MANUAL_CHECK_MS = 90_000;

let updateRequestSequence = 0;

function nextUpdateRequestId(prefix = "feature-update"): string {
	updateRequestSequence += 1;
	return `${prefix}-${updateRequestSequence}`;
}

export function UpdatesSection({ titleHidden }: { titleHidden?: boolean } = {}) {
	const { t } = useTranslation();
	const queryClient = useQueryClient();
	const query = useQuery({
		queryKey: updateSettingsQueryKey,
		queryFn: () => aoBridge.updateSettings.get(),
		refetchInterval: 3_000,
	});

	const [form, setForm] = useState<UpdateSettings>(DEFAULT_SETTINGS);
	const formRef = useRef(form);
	formRef.current = form;
	const [showFeature, setShowFeature] = useState(false);
	const [savingField, setSavingField] = useState<"automatic" | "channel" | null>(null);
	const [pendingPin, setPendingPin] = useState<{ pr: number; title: string } | null>(null);
	const [manualCheckFailure, setManualCheckFailure] = useState<string | null>(null);
	const [manualCheckRequestId, setManualCheckRequestId] = useState<string | null>(null);
	const [channelSwitch, setChannelSwitch] = useState<{ channel: UpdateChannel; requestId: string } | null>(null);
	const channelSwitchRef = useRef<typeof channelSwitch>(null);
	channelSwitchRef.current = channelSwitch;
	const manualCheckStartedAtRef = useRef<number | null>(null);
	const manualCheckFinishTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
	const manualCheckWatchdogRef = useRef<ReturnType<typeof setTimeout> | null>(null);
	const developerMode = useUiStore((state) => state.developerMode);

	const clearManualCheckWatchdog = () => {
		if (manualCheckWatchdogRef.current === null) return;
		clearTimeout(manualCheckWatchdogRef.current);
		manualCheckWatchdogRef.current = null;
	};

	const finishManualCheck = (requestId: string, error?: unknown) => {
		if (error) setManualCheckFailure(error instanceof Error ? error.message : t("settings.updates.updateFailed"));
		clearManualCheckWatchdog();
		if (manualCheckFinishTimerRef.current !== null) clearTimeout(manualCheckFinishTimerRef.current);
		const elapsed = manualCheckStartedAtRef.current === null ? MIN_MANUAL_CHECK_VISIBLE_MS : Date.now() - manualCheckStartedAtRef.current;
		const clear = () => {
			manualCheckFinishTimerRef.current = null;
			setManualCheckRequestId((pending) => {
				if (pending !== requestId) return pending;
				manualCheckStartedAtRef.current = null;
				return null;
			});
		};
		const remaining = Math.max(0, MIN_MANUAL_CHECK_VISIBLE_MS - elapsed);
		if (remaining === 0) clear();
		else manualCheckFinishTimerRef.current = setTimeout(clear, remaining);
	};

	const startManualCheck = (requestId: string) => {
		setManualCheckFailure(null);
		clearManualCheckWatchdog();
		manualCheckStartedAtRef.current = Date.now();
		setManualCheckRequestId(requestId);
		manualCheckWatchdogRef.current = setTimeout(() => {
			manualCheckWatchdogRef.current = null;
			setManualCheckFailure(t("settings.updates.checkTimedOut"));
			setManualCheckRequestId((pending) => (pending === requestId ? null : pending));
		}, MAX_MANUAL_CHECK_MS);
	};

	const status = useUpdateStatus((next) => {
		if (next.requestId && next.requestId === manualCheckRequestId && next.state !== "checking") {
			finishManualCheck(next.requestId);
		}
		const pending = channelSwitchRef.current;
		if (pending && next.requestId === pending.requestId && ["not-available", "error", "unsupported"].includes(next.state)) {
			setChannelSwitch(null);
		}
	}, true);

	useEffect(
		() => () => {
			if (manualCheckFinishTimerRef.current !== null) clearTimeout(manualCheckFinishTimerRef.current);
			if (manualCheckWatchdogRef.current !== null) clearTimeout(manualCheckWatchdogRef.current);
		},
		[],
	);
	const autoProgressRef = useRef<string | null>(null);
	const handledStatusRef = useRef<UpdateState | null>(null);

	useEffect(() => {
		if (query.data) setForm(query.data);
	}, [query.data]);

	useEffect(() => {
		const requestId = autoProgressRef.current;
		if (!requestId || status.requestId !== requestId) return;
		if (handledStatusRef.current === status.state) return;
		handledStatusRef.current = status.state;
		if (status.state === "available") {
			void requestUpdateDownload(requestId);
		} else if (status.state === "downloaded") {
			void aoBridge.updates.install();
			autoProgressRef.current = null;
		} else if (status.state === "error" || status.state === "unsupported" || status.state === "not-available") {
			autoProgressRef.current = null;
		}
	}, [status]);

	const save = useMutation({
		mutationFn: async (next: UpdateSettings) => {
			await aoBridge.updateSettings.set(next);
			return next;
		},
		onSuccess: (next) => {
			setSavingField(null);
			setForm(next);
			void queryClient.invalidateQueries({ queryKey: updateSettingsQueryKey });
		},
		onError: () => {
			setSavingField(null);
			const previous = queryClient.getQueryData<UpdateSettings>(updateSettingsQueryKey);
			if (previous) setForm(previous);
		},
	});

	const channelOptions: { value: PrimaryValue; label: string }[] = [
		{ value: "latest", label: t("settings.updates.channel.stable") },
		{ value: "nightly", label: t("settings.updates.channel.nightly") },
		{ value: "feature", label: t("settings.updates.channel.feature") },
	];
	const primaryValue: PrimaryValue = developerMode && (form.feature !== null || showFeature) ? "feature" : form.channel;

	const setEnabled = (enabled: boolean) => {
		setSavingField("automatic");
		const next = { ...formRef.current, enabled };
		setForm(next);
		save.mutate(next);
	};

	const handlePrimaryChannel = (value: PrimaryValue) => {
		if (value === "feature") {
			setShowFeature(true);
			return;
		}
		setShowFeature(false);
		setSavingField("channel");
		const next = {
			...formRef.current,
			channel: value,
			nightlyAck: value === "nightly",
			feature: null,
		};
		const from = releaseChannelFrom(formRef.current);
		const to = releaseChannelFrom(next);
		setForm(next);
		save.mutate(next);
		if (from !== to) {
			setReleaseChannelContext(to);
			void captureRendererEvent("ao.renderer.update_channel_changed", { from_channel: from, to_channel: to });
		}
		const requestId = nextUpdateRequestId("channel-update");
		setChannelSwitch({ channel: value, requestId });
		startManualCheck(requestId);
		void aoBridge.updates
			.check({ settings: next, requestId })
			.catch(() => {
				setChannelSwitch((pending) => (pending?.requestId === requestId ? null : pending));
			})
			.finally(() => finishManualCheck(requestId));
	};

	const confirmPinBuild = async () => {
		if (!pendingPin) return;
		const { pr } = pendingPin;
		setPendingPin(null);
		const next = { ...formRef.current, feature: { pr } };
		setForm(next);
		const requestId = nextUpdateRequestId();
		autoProgressRef.current = requestId;
		handledStatusRef.current = null;
		try {
			await aoBridge.updates.check({ settings: next, requestId });
			void queryClient.invalidateQueries({ queryKey: updateSettingsQueryKey });
		} catch {
			if (autoProgressRef.current === requestId) autoProgressRef.current = null;
			void queryClient.invalidateQueries({ queryKey: updateSettingsQueryKey });
		}
	};

	const handleReturnToHome = async () => {
		setShowFeature(false);
		setForm({ ...formRef.current, feature: null });
		const requestId = nextUpdateRequestId();
		autoProgressRef.current = requestId;
		handledStatusRef.current = null;
		try {
			await aoBridge.updates.returnHome(requestId);
			void queryClient.invalidateQueries({ queryKey: updateSettingsQueryKey });
		} catch {
			if (autoProgressRef.current === requestId) autoProgressRef.current = null;
			void queryClient.invalidateQueries({ queryKey: updateSettingsQueryKey });
		}
	};

	const activeQuery = useQuery({
		queryKey: ["feature-active"],
		queryFn: () => aoBridge.featureBuilds.getActive(),
	});
	const activeBuild = activeQuery.data ?? null;
	const featurePr = activeBuild?.pr ?? (developerMode ? null : (form.feature?.pr ?? null));

	return (
		<>
			<SettingsSection title={t("settings.updates.versionSection")} sectionId="updates" titleHidden={titleHidden} grouped>
				<UpdateActions
					status={manualCheckFailure ? { ...status, state: "error", message: manualCheckFailure, errorInfo: undefined } : status}
					manualCheckRequestId={manualCheckRequestId}
					startManualCheck={startManualCheck}
					finishManualCheck={finishManualCheck}
					channelSwitch={channelSwitch}
				/>

				{featurePr != null && (
					<SettingsRow
						label={activeBuild
							? t("settings.updates.onFeatureBuild", { pr: featurePr })
							: t("settings.updates.featurePinned", { pr: featurePr })}
						description={t("settings.updates.featureTracking", { pr: featurePr })}
					>
						<Button type="button" variant="quiet" size="sm" onClick={() => void handleReturnToHome()}>
							{form.channel === "nightly" ? t("settings.updates.returnToNightly") : t("settings.updates.returnToStable")}
						</Button>
					</SettingsRow>
				)}
			</SettingsSection>

			<SettingsSection title={t("settings.updates.preferencesSection")} grouped>
				<SettingsRow
					label={t("settings.updates.automatic")}
					description={t("settings.updates.automaticHelp")}
				>
					<Switch
						aria-label={t("settings.updates.automatic")}
						checked={form.enabled}
						onCheckedChange={setEnabled}
						disabled={savingField === "automatic"}
					/>
				</SettingsRow>

				<SettingsRow
					label={t("settings.updates.channel")}
					description={
						<>
							{t("settings.updates.channelHelp")}
							{primaryValue === "nightly" && (
								<span className="nightly-warning mt-1 flex items-start gap-1.5">
									<Info className="mt-0.5 size-icon-sm shrink-0" aria-hidden="true" />
									<span className="min-w-0">{t("settings.updates.nightlyWarning")}</span>
								</span>
							)}
						</>
					}
				>
					<SettingsOptionMenu
						aria-label={t("settings.updates.channel")}
						value={primaryValue}
						options={developerMode ? channelOptions : channelOptions.filter((option) => option.value !== "feature")}
						onChange={handlePrimaryChannel}
						disabled={savingField === "channel"}
					/>
				</SettingsRow>

				{primaryValue === "feature" && (
					<FeatureBuildsSelect
						currentPr={form.feature?.pr ?? null}
						onPin={(pr, title) => setPendingPin({ pr, title })}
					/>
				)}

				{save.isError && (
					<p role="alert" className="settings-content-row text-xs text-error">
						{save.error instanceof Error ? save.error.message : t("settings.updates.saveFailed")}
					</p>
				)}
			</SettingsSection>
			<ConfirmDialog
				open={pendingPin !== null}
				title={t("settings.updates.switchFeatureTitle")}
				description={pendingPin ? t("settings.updates.switchFeatureBody", pendingPin) : null}
				confirmLabel={t("settings.updates.confirm")}
				onConfirm={() => void confirmPinBuild()}
				onOpenChange={(open) => !open && setPendingPin(null)}
			/>
		</>
	);
}

function FeatureBuildsSelect({
	currentPr,
	onPin,
}: {
	currentPr: number | null;
	onPin: (pr: number, title: string) => void;
}) {
	const { t } = useTranslation();
	const buildsQuery = useQuery({ queryKey: ["feature-builds"], queryFn: () => aoBridge.featureBuilds.list() });
	const builds = buildsQuery.data ?? [];

	if (!buildsQuery.isLoading && builds.length === 0) {
		return <div className="settings-row-bar text-sm text-settings-muted">{t("settings.updates.noFeatureReleases")}</div>;
	}

	return (
		<SettingsRow label={t("settings.updates.featureBuild")}>
			<SettingsOptionMenu
				aria-label={t("settings.updates.featureBuild")}
				value={currentPr === null ? "__none__" : currentPr.toString()}
				placeholder={t("settings.updates.selectFeature")}
				options={builds.map((build) => ({ value: build.pr.toString(), label: `PR #${build.pr}: ${build.title}` }))}
				disabled={buildsQuery.isLoading}
				onChange={(value) => {
					const build = builds.find((item) => item.pr === Number(value));
					if (build) onPin(build.pr, build.title);
				}}
			/>
		</SettingsRow>
	);
}

function UpdateActions({
	status,
	manualCheckRequestId,
	startManualCheck,
	finishManualCheck,
	channelSwitch,
}: {
	status: UpdateStatus;
	manualCheckRequestId: string | null;
	startManualCheck: (requestId: string) => void;
	finishManualCheck: (requestId: string, error?: unknown) => void;
	channelSwitch: { channel: UpdateChannel; requestId: string } | null;
}) {
	const { t, i18n } = useTranslation();
	const locale = i18n.resolvedLanguage ?? i18n.language;
	const version = useQuery({ queryKey: ["app-version"], queryFn: () => aoBridge.app.getVersion() });
	const requestUpdateInstall = useRequestUpdateInstall();
	const installed = parseNightlyVersion(version.data);
	const downloadButtonRef = useRef<HTMLButtonElement | null>(null);
	const restartButtonRef = useRef<HTMLButtonElement | null>(null);
	const checkButtonRef = useRef<HTMLButtonElement | null>(null);
	const statusLineRef = useRef<HTMLDivElement | null>(null);
	const restoreActionFocusRef = useRef(false);
	const setDownloadButtonRef = useCallback((node: HTMLButtonElement | null) => {
		if (!node && downloadButtonRef.current && downloadButtonRef.current === document.activeElement) restoreActionFocusRef.current = true;
		downloadButtonRef.current = node;
	}, []);
	useLayoutEffect(() => {
		if (!restoreActionFocusRef.current) return;
		restoreActionFocusRef.current = false;
		if (document.activeElement !== document.body) return;
		const nextAction = restartButtonRef.current ?? checkButtonRef.current;
		if (nextAction?.disabled) statusLineRef.current?.focus();
		else nextAction?.focus();
	});

	const manualCheckPending = manualCheckRequestId !== null;
	const checking = status.state === "checking" || manualCheckPending;
	const downloading = status.state === "downloading" || status.state === "preparing";
	const downloadLabel = downloading ? t("settings.updates.updating") : t("settings.updates.update");
	const updateTargetLabel = status.version ? t("settings.updates.updateTo", { version: `v${status.version}` }) : t("settings.updates.updateToLatest");
	const downloadAccessibleLabel = downloading ? `${downloadLabel} ${updateTargetLabel}` : updateTargetLabel;
	const busy = manualCheckPending || downloading;
	const displayState: UpdateState = checking && !downloading && status.state !== "error" && status.state !== "downloaded" ? "checking" : status.state;
	const showStatusState = displayState !== "checking";

	const [now, setNow] = useState(Date.now());
	useEffect(() => {
		const timer = setInterval(() => setNow(Date.now()), 1_000);
		return () => clearInterval(timer);
	}, []);
	const checkedAt = status.checkedAt
		? new Intl.DateTimeFormat(locale, { dateStyle: "medium", timeStyle: "medium" }).format(status.checkedAt)
		: null;

	const channelSwitchInFlight = channelSwitch !== null && (!status.requestId || status.requestId === channelSwitch.requestId);
	const channelSwitchMessage = channelSwitchInFlight &&
		(status.state === "available" || status.state === "downloading")
		? t("settings.updates.channelSwitchUpdate", {
			channel: channelSwitch.channel === "nightly" ? t("settings.updates.channel.nightlyShort") : t("settings.updates.channel.stable"),
		})
		: null;

	const checkNow = async () => {
		const requestId = nextUpdateRequestId("manual-update");
		startManualCheck(requestId);
		try {
			await aoBridge.updates.check({ requestId });
		} catch (error) {
			finishManualCheck(requestId, error);
		} finally {
			finishManualCheck(requestId);
		}
	};

	return (
		<>
			<SettingsRow
				label={
					<span
						aria-label={t("settings.updates.currentVersion", { version: version.data ? `v${version.data}` : "…" })}
						className="tabular-nums"
						data-testid="app-version"
					>
						{version.data ? `v${installed?.base ?? version.data}` : "…"}
					</span>
				}
				description={
					<div className="flex min-w-0 flex-col gap-1">
						{installed && (
							<p className="flex min-w-0 flex-wrap items-baseline gap-x-1.5">
								<span className="min-w-0 break-all font-mono">{version.data}</span>
								<span className="whitespace-nowrap">
									{t("settings.updates.nightlyBuiltAt", {
										date: new Intl.DateTimeFormat(locale, { dateStyle: "medium", timeStyle: "short" }).format(installed.builtAt),
									})}
								</span>
							</p>
						)}
						<div
							ref={statusLineRef}
							id="update-status-line"
							data-testid="update-status-line"
							role="status"
							aria-live="polite"
							aria-atomic="true"
							aria-busy={checking || downloading}
							tabIndex={-1}
							className="flex min-h-4 min-w-0 flex-col gap-1"
						>
							{showStatusState && <UpdateStatusLine state={displayState} status={status} />}
							{status.state === "downloading" && status.percent !== undefined && <progress aria-label={t("settings.updates.progress")} max={100} value={status.percent} className="h-1 w-full" />}
							{status.transferred !== undefined && status.total !== undefined && <p className="tabular-nums">{t("settings.updates.bytes", { downloaded: (status.transferred / 1_000_000).toFixed(1), total: (status.total / 1_000_000).toFixed(1) })}</p>}
							{channelSwitchMessage && <p>{channelSwitchMessage}</p>}
							{checkedAt ? (
								<p className="tabular-nums" data-testid="update-checked-at">
									<span title={new Date(status.checkedAt!).toISOString()}>{now - status.checkedAt! < 60_000 ? t("settings.updates.checkedJustNow", { defaultValue: "Checked just now" }) : t("settings.updates.checkedMinutesAgo", { defaultValue: "Checked {{count}} minutes ago", count: Math.floor((now - status.checkedAt!) / 60_000) })} · {checkedAt}</span>
								</p>
							) : null}
						</div>
						{status.checkError && !(displayState === "error" && status.message === status.checkError) && (
							<UpdateNotice tone="error" text={localizedUpdateError(status.checkError, status.checkErrorInfo, t("settings.updates.updateFailed"), t)} />
						)}
						{!status.checkError && !status.staleCheckNudge && status.checksFailing && (
							<UpdateNotice tone="warning" text={t("settings.updates.checksFailing")} />
						)}
						{status.staleCheckNudge && <UpdateNotice tone="warning" text={t("settings.updates.networkStale")} />}
					</div>
				}
			>
				<div className="flex shrink-0 flex-wrap items-center justify-end gap-2">
					{(status.state === "available" || downloading) && (
						<Button
							ref={setDownloadButtonRef}
							type="button"
							variant="primary"
							size="sm"
							aria-label={downloadAccessibleLabel}
							aria-describedby="update-status-line"
							aria-disabled={downloading}
							className="aria-disabled:cursor-wait aria-disabled:opacity-50"
							onClick={() => {
								if (!downloading) void requestUpdateDownload();
							}}
						>
							<Download className="size-icon-sm" aria-hidden="true" />
							{downloadLabel}
						</Button>
					)}
					{(!downloading && (status.state === "downloaded" || (status.staged && status.staged.ready !== false))) && (
						<Button ref={restartButtonRef} type="button" variant="primary" size="sm" onClick={requestUpdateInstall}>
							<RefreshCw className="size-icon-sm" aria-hidden="true" />
							{t("settings.updates.restartInstall")}
						</Button>
					)}
					<Button
						ref={checkButtonRef}
						type="button"
						aria-label={checking ? t("settings.updates.checking") : t("settings.updates.check")}
						aria-describedby={showStatusState ? "update-status-line" : undefined}
						variant="quiet"
						size="sm"
						onClick={() => void checkNow()}
						disabled={busy}
					>
						{checking ? (
							<Loader2 className="size-icon-sm animate-spin motion-reduce:animate-none" aria-hidden="true" />
						) : (
							<RefreshCw className="size-icon-sm" aria-hidden="true" />
						)}
						{checking ? t("settings.updates.checking") : t("settings.updates.check")}
					</Button>
				</div>
			</SettingsRow>

			{(status.state === "downloaded" || status.staged) && status.releaseNotes ? (
				<div className="settings-content-row flex flex-col gap-1.5" data-testid="update-release-notes">
					<p className="text-sm font-medium text-settings-label">{t("update.restart.whatsNew")}</p>
					<DesktopReleaseNotes
						notes={status.releaseNotes}
						textClassName="settings-thin-scrollbar max-h-40 overflow-y-auto overscroll-contain text-pretty text-sm leading-5 text-settings-label"
					/>
				</div>
			) : null}
		</>
	);
}

function UpdateNotice({ tone, text }: { tone: "warning" | "error"; text: string }) {
	return (
		<p className={cn("flex items-start gap-1.5 text-xs leading-4", tone === "warning" ? "text-warning" : "text-error")}>
			<AlertTriangle className="mt-px size-icon-sm shrink-0" aria-hidden="true" />
			<span className="min-w-0">{text}</span>
		</p>
	);
}

function DownloadProgressIcon({ percent }: { percent: number }) {
	const clamped = Math.min(100, Math.max(0, percent));
	return (
		<span className="relative grid size-icon-sm shrink-0 place-items-center" aria-hidden="true">
			<svg className="absolute inset-0 size-full -rotate-90" viewBox="0 0 24 24" fill="none">
				<circle cx="12" cy="12" r="9" className="stroke-current/20" strokeWidth="2.5" />
				<circle cx="12" cy="12" r="9" className="stroke-current" strokeWidth="2.5" strokeLinecap="round" strokeDasharray={`${clamped * 0.5655} 56.55`} />
			</svg>
		</span>
	);
}

function UpdateStatusLine({
	state,
	status,
}: {
	state: UpdateState;
	status: UpdateStatus;
}) {
	const { t } = useTranslation();
	let className = "text-settings-muted";
	let icon: ReactNode = null;
	let label: string;
	let detail: string | null = null;


	switch (state) {
		case "checking":
			icon = <Loader2 className="size-icon-sm shrink-0 animate-spin motion-reduce:animate-none" aria-hidden="true" />;
			label = t("settings.updates.checking");
			break;
		case "available":
			className = "text-settings-label";
			icon = <Download className="size-icon-sm shrink-0" aria-hidden="true" />;
			label = t("settings.updates.availableNow");
			detail = status.version ? t("settings.updates.targetVersion", { version: status.version }) : null;
			break;
		case "downloading":
			className = "text-settings-label tabular-nums";
			icon = <DownloadProgressIcon percent={status.percent ?? 0} />;
			label = status.percent === undefined ? t("settings.updates.startingDownload", { defaultValue: "Starting download…" }) : t("settings.updates.downloading", { percent: status.percent });
			detail = status.version ? t("settings.updates.targetVersion", { version: status.version }) : null;
			break;
		case "preparing":
			icon = <Loader2 className="size-icon-sm animate-spin" aria-hidden="true" />;
			label = t("settings.updates.preparing", { defaultValue: "Preparing update…" });
			detail = status.version ? t("settings.updates.targetVersion", { version: status.version }) : null;
			break;
		case "downloaded":
			className = "text-success";
			icon = <CheckCircle2 className="size-icon-sm shrink-0" aria-hidden="true" />;
			label = t("settings.updates.downloaded");
			detail = status.version ? t("settings.updates.targetVersion", { version: status.version }) : null;
			break;
		case "not-available":
			className = "text-success";
			icon = <CheckCircle2 className="size-icon-sm shrink-0" aria-hidden="true" />;
			label = t("settings.updates.latest");
			break;
		case "unsupported":
			icon = <Info className="size-icon-sm shrink-0" aria-hidden="true" />;
			label = status.message ?? t("settings.updates.needInstalledApp");
			break;
		case "error":
			className = "text-error";
			icon = <AlertTriangle className="size-icon-sm shrink-0" aria-hidden="true" />;
			label = status.netError
				? t("settings.updates.netErrorRestartGuidance")
				: localizedUpdateError(status.message, status.errorInfo, t("settings.updates.updateFailed"), t);
			detail = status.version ? t("settings.updates.availableVersion", { version: status.version }) : null;
			break;
		default:
			label = t("settings.updates.notChecked");
	}

	return (
		<div className={cn("flex min-w-0 items-start gap-1.5", className)}>
			{icon !== null && <span className="flex h-4 shrink-0 items-center">{icon}</span>}
			<div className="min-w-0">
				<p className="text-pretty">{label}</p>
				{detail !== null && (
					<p className="mt-0.5 truncate text-settings-muted">{detail}</p>
				)}
			</div>
		</div>
	);
}
