import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useCallback, useEffect, useLayoutEffect, useRef, useState, type ReactNode } from "react";
import { AlertTriangle, CheckCircle2, ChevronRight, Download, Info, Loader2, RefreshCw } from "@/components/app-icons";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
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
import { Pressable } from "../ui/pressable";
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
// Hard ceiling on how long a manual check may hold the button. The IPC call is
// awaited and normally settles on its own, but if it ever does not, the pending
// request id disables the Check button for the rest of the session with nothing
// on screen to explain it. Releasing the button is always safe: the main process
// serializes updater operations, so a redundant check queues rather than racing.
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
	// Set only for the owned pin/home transition request, so unrelated hourly
	// updater events cannot auto-progress through download/install.
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
			// Reported on the switch rather than inferred later, because someone who
			// moves to nightly and does not update yet is on nightly by intent while
			// still running a stable build.
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
		// Optimistic; the main process clears the pin against persisted state.
		setForm({ ...formRef.current, feature: null });
		const requestId = nextUpdateRequestId();
		autoProgressRef.current = requestId;
		handledStatusRef.current = null;
		try {
			// Single updater-serialized op: clears the pin and checks the home channel
			// atomically, so a concurrent settings-write cannot restore the pin.
			await aoBridge.updates.returnHome(requestId);
			void queryClient.invalidateQueries({ queryKey: updateSettingsQueryKey });
		} catch {
			if (autoProgressRef.current === requestId) autoProgressRef.current = null;
			// The optimistic form update may now disagree with disk; re-sync to truth.
			void queryClient.invalidateQueries({ queryKey: updateSettingsQueryKey });
		}
	};

	const activeQuery = useQuery({
		queryKey: ["feature-active"],
		queryFn: () => aoBridge.featureBuilds.getActive(),
	});
	const activeBuild = activeQuery.data ?? null;
	// Show the escape hatch whenever a feature build is running or pinned.
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
						// The nightly note is a standing property of the chosen channel, not
						// an alert: it reads as part of the description, in the same muted
						// tone and on the same left edge, never warning-colored (one step
						// from --destructive, it read as a permanent failure).
						primaryValue === "nightly" ? (
							<>
								<span className="block">{t("settings.updates.channelHelp")}</span>
								<span className="mt-0.5 block">{t("settings.updates.nightlyWarning")}</span>
							</>
						) : t("settings.updates.channelHelp")
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
		// Capture focus before React removes the download action from the DOM.
		if (!node && downloadButtonRef.current && downloadButtonRef.current === document.activeElement) restoreActionFocusRef.current = true;
		downloadButtonRef.current = node;
	}, []);
	useLayoutEffect(() => {
		if (!restoreActionFocusRef.current) return;
		restoreActionFocusRef.current = false;
		if (document.activeElement !== document.body) return;
		const nextAction = restartButtonRef.current ?? checkButtonRef.current;
		// A fast failure can arrive inside the manual-check minimum busy window.
		if (nextAction?.disabled) statusLineRef.current?.focus();
		else nextAction?.focus();
	});

	const manualCheckPending = manualCheckRequestId !== null;
	const checking = status.state === "checking" || manualCheckPending;
	const downloading = status.state === "downloading" || status.state === "preparing";
	const downloadLabel = downloading ? t("settings.updates.updating") : t("settings.updates.update");
	const updateTargetLabel = status.version ? t("settings.updates.updateTo", { version: `v${status.version}` }) : t("settings.updates.updateToLatest");
	const downloadAccessibleLabel = downloading ? `${downloadLabel} ${updateTargetLabel}` : updateTargetLabel;
	// Only the user's OWN in-flight check blocks the button. A background check
	// also reports "checking", and gating on that swallowed the first click
	// whenever Settings was opened during one — every 15 minutes on nightly.
	// A click during a background check is fine: the main process serializes
	// updater operations, so the manual check simply queues behind it.
	const busy = manualCheckPending || downloading;
	// The minimum-spinner window keeps "checking" on screen briefly after the
	// updater has already answered. Only the Check button shows checking feedback;
	// the status row resumes with the updater's terminal result.
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
	// Use the live updater state, not displayState: the manual-check minimum
	// spinner time forces displayState back to "checking" even after a channel
	// switch finds an update, which would hide this guidance until the timer fires.
	// Not shown for "downloaded": the status line already says "Downloaded.
	// Restart to finish updating." and names the build under it, so adding
	// "Restart to switch to Nightly." put two restart sentences side by side.
	const channelSwitchMessage = channelSwitchInFlight &&
		(status.state === "available" || status.state === "downloading")
		? t("settings.updates.channelSwitchUpdate", {
			// Short form: "Restart to switch to Nightly (Pre-release)." reads as a
			// parenthetical stuck mid-sentence. The suffix belongs in the picker.
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

	// The last-checked time rides on the status line for settled states and
	// stands alone while a check runs. Downloads and staged builds already name
	// their next step, so the time would only be noise there.
	const checkedLabel = status.checkedAt ? checkedAgoLabel(now, status.checkedAt, locale, t) : null;
	const showChecked = checkedLabel !== null && !["downloading", "preparing", "downloaded"].includes(displayState);
	const checked = showChecked ? (
		<span className="tabular-nums" title={checkedAt ?? undefined} data-testid="update-checked-at">{checkedLabel}</span>
	) : null;
	const releaseNotesVersion = status.state === "downloaded" ? status.version : (status.staged?.version ?? status.version);

	// One settings row: the version is the label, the live status line is its
	// description, and the actions sit at the trailing edge. Only Update and
	// Restart & install are primary; Check for updates is a quiet row action.
	// Every line in the description starts at the same left edge: icons sit
	// inline in the text, so a wrapped line never hangs under an indent.
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
							// An installed nightly is identified by its full stamp under the base
							// version, in the row's own type so it lines up with every other line.
							<p className="tabular-nums">
								<span className="break-all">{version.data}</span>
								<Separator />
								<span>
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
							// min-h-4 keeps one line while the button owns the checking feedback,
							// so the notices below do not jump up.
							className="flex min-h-4 min-w-0 flex-col gap-1.5 outline-none"
						>
							{showStatusState ? (
								<UpdateStatusLine state={displayState} status={status} trailing={checked} />
							) : checked ? (
								<p>{checked}</p>
							) : null}
							{status.state === "downloading" && status.percent !== undefined && (
								<div className="flex items-center gap-2">
									<progress
										aria-label={t("settings.updates.progress")}
										max={100}
										value={status.percent}
										className="block h-1 w-full max-w-64 appearance-none overflow-hidden rounded-full bg-(--color-border) [&::-moz-progress-bar]:bg-(--color-accent) [&::-webkit-progress-bar]:bg-(--color-border) [&::-webkit-progress-value]:bg-(--color-accent) [&::-webkit-progress-value]:transition-[width] [&::-webkit-progress-value]:duration-(--duration-normal)"
									/>
									{status.transferred !== undefined && status.total !== undefined && (
										<span className="shrink-0 tabular-nums">
											{t("settings.updates.bytes", { downloaded: (status.transferred / 1_000_000).toFixed(1), total: (status.total / 1_000_000).toFixed(1) })}
										</span>
									)}
								</div>
							)}
							{channelSwitchMessage && <p>{channelSwitchMessage}</p>}
						</div>
						{/* The primary error already explains the failed check. Keep a separate
						    notice only when it adds information (for example beside a staged build). */}
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
				{/* Actions. Fixed to the right and never widened by a version string:
				    the primary label used to carry the full nightly stamp and grew
				    across the version. The target build is named in the status line. */}
				<div className="flex shrink-0 flex-wrap items-center justify-end gap-2">
					{/* Reflect every download, including automatic and channel-switch work. */}
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
						// Opens the restart confirmation rather than installing outright:
						// installing quits the app, which costs a turn on any chat session
						// running a daemon-owned driver.
						<Button ref={restartButtonRef} type="button" variant="primary" size="sm" onClick={requestUpdateInstall}>
							<RefreshCw className="size-icon-sm" aria-hidden="true" />
							{t("settings.updates.restartInstall")}
						</Button>
					)}
					{/* Always rendered. Hiding it whenever something was available or
					    staged left no way to re-check at all: a user sitting on a build
					    that will not install had a single dead Restart button and no
					    other action on the page. */}
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

			{/* Release notes used to live only in the restart confirmation, so
			    skipping that dialog when nothing is at risk would have hidden them
			    entirely. They are a disclosure here: one quiet row by default, so a
			    staged build does not push the preferences off screen. The shared
			    renderer keeps contributor handles out while preserving linked PR
			    numbers. */}
			{(status.state === "downloaded" || status.staged) && status.releaseNotes ? (
				<ReleaseNotesDisclosure notes={status.releaseNotes} version={releaseNotesVersion} />
			) : null}
		</>
	);
}

/** Muted middle dot between two facts on one line. */
function Separator() {
	return <span aria-hidden="true" className="px-1.5 text-settings-muted">·</span>;
}

/**
 * "Checked just now" under a minute, then "Last checked 5 minutes ago",
 * "… 2 hours ago", "… yesterday" in the person's language. The absolute
 * time stays on hover.
 */
function checkedAgoLabel(now: number, checkedAt: number, locale: string, t: TFunction): string {
	const elapsed = Math.max(0, now - checkedAt);
	if (elapsed < 60_000) return t("settings.updates.checkedJustNow", { defaultValue: "Checked just now" });
	const minutes = Math.floor(elapsed / 60_000);
	const hours = Math.floor(minutes / 60);
	const days = Math.floor(hours / 24);
	const relative = new Intl.RelativeTimeFormat(locale, { numeric: "auto" });
	const time = days > 0 ? relative.format(-days, "day") : hours > 0 ? relative.format(-hours, "hour") : relative.format(-minutes, "minute");
	return t("settings.updates.lastChecked", { time });
}

function ReleaseNotesDisclosure({ notes, version }: { notes: string; version?: string }) {
	const { t } = useTranslation();
	const [open, setOpen] = useState(false);
	const regionId = "update-release-notes-body";
	return (
		<div className="flex flex-col" data-testid="update-release-notes">
			<Pressable
				aria-expanded={open}
				aria-controls={regionId}
				onClick={() => setOpen((value) => !value)}
				className="settings-content-row group flex w-full items-center gap-1.5 text-sm leading-5 text-settings-label focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-inset"
			>
				<ChevronRight
					className={cn(
						"size-icon-sm shrink-0 text-settings-muted transition-transform duration-(--duration-fast) motion-reduce:transition-none",
						open && "rotate-90",
					)}
					aria-hidden="true"
				/>
				<span>{t("update.restart.whatsNew")}</span>
				{version ? <span className="min-w-0 truncate text-control text-settings-muted tabular-nums">{`v${version}`}</span> : null}
			</Pressable>
			<div
				id={regionId}
				className={cn(
					"grid transition-[grid-template-rows] duration-(--duration-normal) ease-out motion-reduce:transition-none",
					open ? "grid-rows-[1fr]" : "grid-rows-[0fr]",
				)}
				inert={!open}
				aria-hidden={!open || undefined}
			>
				<div className="overflow-hidden">
					<DesktopReleaseNotes
						notes={notes}
						textClassName="settings-thin-scrollbar max-h-56 overflow-y-auto overscroll-contain px-(--settings-row-inset) pb-3 pl-[calc(var(--settings-row-inset)+var(--size-icon-sm)+0.375rem)] text-pretty text-control leading-5 text-settings-muted"
					/>
				</div>
			</div>
		</div>
	);
}

/** Caution/failure note under the status line: tone and an inline icon, no box. */
function UpdateNotice({ tone, text }: { tone: "warning" | "error"; text: string }) {
	return (
		<p className={cn("text-pretty", tone === "warning" ? "text-warning" : "text-error")}>
			<AlertTriangle className="mr-1.5 inline-block size-icon-sm align-[-2px]" aria-hidden="true" />
			{text}
		</p>
	);
}

function DownloadProgressIcon({ percent }: { percent: number }) {
	const clamped = Math.min(100, Math.max(0, percent));
	return (
		<span className="relative mr-1.5 inline-grid size-icon-sm place-items-center align-[-2px]" aria-hidden="true">
			<svg className="absolute inset-0 size-full -rotate-90" viewBox="0 0 24 24" fill="none">
				<circle cx="12" cy="12" r="9" className="stroke-current/20" strokeWidth="2.5" />
				<circle cx="12" cy="12" r="9" className="stroke-current" strokeWidth="2.5" strokeLinecap="round" strokeDasharray={`${clamped * 0.5655} 56.55`} />
			</svg>
		</span>
	);
}

/**
 * The single status line for the section: an inline state icon, the state,
 * then muted facts after middle dots (target build, last checked).
 * Every state gets a visible line: `available` used to be suppressed
 * entirely (the button was the only clue), which made a wedged check and a
 * check in progress look identical.
 */
function UpdateStatusLine({
	state,
	status,
	trailing,
}: {
	state: UpdateState;
	status: UpdateStatus;
	trailing?: ReactNode;
}) {
	const { t } = useTranslation();
	const iconClass = "mr-1.5 inline-block size-icon-sm align-[-2px]";
	let className = "text-settings-muted";
	let icon: ReactNode = null;
	let label: string;
	let detail: string | null = null;

	switch (state) {
		case "checking":
			icon = <Loader2 className={cn(iconClass, "animate-spin motion-reduce:animate-none")} aria-hidden="true" />;
			label = t("settings.updates.checking");
			break;
		case "available":
			className = "text-settings-label";
			icon = <Download className={iconClass} aria-hidden="true" />;
			label = status.version ? t("settings.updates.availableVersion", { version: status.version }) : t("settings.updates.availableNow");
			break;
		case "downloading":
			className = "text-settings-label tabular-nums";
			icon = <DownloadProgressIcon percent={status.percent ?? 0} />;
			label = status.percent === undefined ? t("settings.updates.startingDownload", { defaultValue: "Starting download…" }) : t("settings.updates.downloading", { percent: status.percent });
			detail = status.version ? `v${status.version}` : null;
			break;
		case "preparing":
			icon = <Loader2 className={cn(iconClass, "animate-spin motion-reduce:animate-none")} aria-hidden="true" />;
			label = t("settings.updates.preparing", { defaultValue: "Preparing update…" });
			detail = status.version ? `v${status.version}` : null;
			break;
		case "downloaded":
			className = "text-success";
			icon = <CheckCircle2 className={iconClass} aria-hidden="true" />;
			label = t("settings.updates.downloaded");
			detail = status.version ? `v${status.version}` : null;
			break;
		case "not-available":
			className = "text-success";
			icon = <CheckCircle2 className={iconClass} aria-hidden="true" />;
			label = t("settings.updates.latest");
			break;
		case "unsupported":
			icon = <Info className={iconClass} aria-hidden="true" />;
			label = status.message ?? t("settings.updates.needInstalledApp");
			break;
		case "error":
			className = "text-error";
			icon = <AlertTriangle className={iconClass} aria-hidden="true" />;
			// The main process already rewrites updater failures and says what they
			// mean, so the sentence is localized here; the English fallback still
			// keeps a dump it has not learned yet out of Settings (#5755).
			label = status.netError
				? t("settings.updates.netErrorRestartGuidance")
				: localizedUpdateError(status.message, status.errorInfo, t("settings.updates.updateFailed"), t);
			// Name the build being held back (a full disk refuses staging, a
			// download failed) so the user still learns what they are missing.
			detail = status.version ? t("settings.updates.availableVersion", { version: status.version }) : null;
			break;
		default:
			label = t("settings.updates.notChecked");
	}

	return (
		<p className="min-w-0 text-pretty">
			<span className={className}>
				{icon}
				<span>{label}</span>
			</span>
			{detail !== null && (
				<>
					<Separator />
					<span className="text-settings-muted">{detail}</span>
				</>
			)}
			{trailing ? (
				<>
					<Separator />
					<span className="text-settings-muted">{trailing}</span>
				</>
			) : null}
		</p>
	);
}
