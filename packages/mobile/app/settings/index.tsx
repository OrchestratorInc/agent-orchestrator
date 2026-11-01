import * as Application from "expo-application";
import * as Clipboard from "expo-clipboard";
import * as Device from "expo-device";
import Constants from "expo-constants";
import { useFocusEffect, useRouter } from "expo-router";
import * as Updates from "expo-updates";
import { useCallback, useEffect, useRef, useState, type ReactElement } from "react";
import { Alert, Linking, Platform, StyleSheet, View } from "react-native";
import { loadAccount, signInToAccount, signOutOfAccount, type Account } from "../../lib/account";
import { clearAccountHosts, syncAccountHosts } from "../../lib/accountHosts";
import { formatVersionLine, type BuildInfo } from "../../lib/appInfo";
import { bugReportClipboard, bugReportOpenUrl, bugReportUrl } from "../../lib/bugReport";
import { isConfigured, type ServerConfig } from "../../lib/config";
import { classifyConnectionFailure } from "../../lib/connectionError";
import { describeDesktopStatus } from "../../lib/desktopStatus";
import { discordFeatureRequestURL } from "../../lib/discord";
import { isEmulatedHost, setEmulatedMachines, useEmulatedMachines } from "../../lib/emulatedMachines";
import { takeEnvironmentNotice } from "../../lib/environmentNotice";
import { environmentStatus } from "../../lib/environmentView";
import { haptics } from "../../lib/haptics";
import { activeHost, loadHosts, type Host as PairedHost } from "../../lib/hosts";
import { checkStore, openOrStartUpdate } from "../../lib/inAppUpdates";
import { MachineRow, machineStatusTone } from "../../lib/MachineRow";
import { canRepairWithQr } from "../../lib/machineRecovery";
import { openGitHub } from "../../lib/openGitHub";
import { getPushStatus, openNotificationSettings, registerForPush, unregisterFromPush } from "../../lib/push";
import { describePushToggle, describeRegisterFailure, type PushStatus } from "../../lib/pushStatus";
import {
	SettingsButtonRow,
	SettingsForm,
	SettingsLinkRow,
	SettingsPickerRow,
	SettingsSection,
	SettingsToggleRow,
	SettingsValueRow,
	type SettingsOption,
	type SettingsTone,
} from "../../lib/SettingsForm";
import { Spinner } from "../../lib/Spinner";
import { StackRootBack } from "../../lib/StackRootBack";
import { tunnelMayHaveRotated } from "../../lib/staleTunnel";
import { useApp } from "../../lib/store";
import {
	describeSoftwareUpdateRow,
	describeStoreRow,
	floorSignal,
	floorTarget,
	storeRowResult,
	tierOf,
	type StoreCheck,
	type StoreRowResult,
} from "../../lib/storeUpdate";
import { promptStoreUpdate } from "../../lib/storeUpdatePrompt";
import { useTheme, useThemeState } from "../../lib/ThemeProvider";
import { preferenceLabel, type ThemePreference } from "../../lib/themePreference";
import { ToastHost, type ToastController } from "../../lib/Toast";
import { checkAndDownload, describeUpdateRow, type UpdateOutcome } from "../../lib/updates";
import { useToast } from "../../lib/useToast";
import { VERSION_FLOOR } from "../../lib/versionFloor";
import { getThemeDefinition } from "../../../shared/theme/palettes";

// Light / Dark / System, in the desktop app's order and wording.
const APPEARANCE_OPTIONS: readonly SettingsOption<ThemePreference>[] = (["light", "dark", "system"] as const).map((value) => ({ value, label: preferenceLabel(value) }));

export { RouteErrorBoundary as ErrorBoundary } from "../../lib/RouteErrorBoundary";

export default function SettingsScreen() {
	const notificationsRow = useNotificationsRow();
	const emulatingMachines = useEmulatedMachines();
	const t = useTheme();
	const router = useRouter();
	const { config, configured, selectedHostName, connection, error, errorStatus, activeEndpoints, linkHealth, loading: appLoading, hostStates, reloadConfig } = useApp();
	const [pairedHosts, setPairedHosts] = useState<PairedHost[]>([]);
	const [selectedHostId, setSelectedHostId] = useState<string | null>(null);
	const toast = useToast();
	const { show: showToast } = toast;
	const [loaded, setLoaded] = useState(false);
	const [account, setAccount] = useState<Account | null>(null);
	const [accountBusy, setAccountBusy] = useState(false);
	const [accountLoading, setAccountLoading] = useState(true);

	useFocusEffect(useCallback(() => {
		let current = true;
		void loadAccount().then((value) => { if (current) setAccount(value); }).catch(() => {
			if (current) setAccount(null);
		}).finally(() => { if (current) setAccountLoading(false); });
		return () => { current = false; };
	}, []));

	useFocusEffect(useCallback(() => {
		let current = true;
		void Promise.all([loadHosts(), activeHost()]).then(([hosts, active]) => {
			if (!current) return;
			setPairedHosts(hosts);
			setSelectedHostId(active?.id ?? null);
			setLoaded(true);
		});
		return () => { current = false; };
	}, []));

	// A machine forgotten on its own page leaves word of it for the list it
	// returns to.
	useFocusEffect(useCallback(() => {
		const notice = takeEnvironmentNotice();
		if (notice) showToast(notice);
	}, [showToast]));

	function signIn() {
		setAccountBusy(true);
		void signInToAccount().then(async (value) => {
			if (!value) return;
			setAccount(value);
			await syncAccountHosts(value);
			setPairedHosts(await loadHosts());
			await reloadConfig();
		}).catch((error: unknown) => {
			Alert.alert("Could not sign in", error instanceof Error ? error.message : "Try again.");
		}).finally(() => setAccountBusy(false));
	}

	function signOut() {
		setAccountBusy(true);
		void signOutOfAccount().then(async () => {
			await clearAccountHosts();
			setAccount(null);
			setPairedHosts(await loadHosts());
			await reloadConfig();
		}).catch(() => Alert.alert("Could not sign out", "Try again.")).finally(() => setAccountBusy(false));
	}

	if (!loaded) {
		return (
			<View style={[styles.center, { backgroundColor: t.bgBase }]}>
				<StackRootBack />
				<Spinner size="large" accessibilityLabel="Loading settings" />
			</View>
		);
	}

	const paired = pairedHosts.length > 0 || configured || selectedHostName !== null;
	const selectedHost = pairedHosts.find((host) => host.id === selectedHostId);
	// A dev build's fake machines, listed after the real ones; see lib/emulatedMachines.
	const emulatedHosts = hostStates.filter((host) => isEmulatedHost(host.hostId));
	const machineCount = pairedHosts.length + emulatedHosts.length;
	// Only a poll that actually failed is a failure. Before the first tick lands
	// errorStatus is null too, which on its own would read as unreachable.
	const classified = error ? classifyConnectionFailure(errorStatus ?? undefined) : null;
	const failure =
		classified === "unreachable" && tunnelMayHaveRotated(activeEndpoints, config?.endpointKind, connection === "open")
			? "tunnel-rotated"
			: classified ?? (paired && !config && !appLoading ? "unreachable" : null);
	const machineStatus = describeDesktopStatus({ configured: paired, connection, failure, link: linkHealth });
	function connectionStatus(hostId: string): { label: string; tone: SettingsTone } {
		// The default machine's status is the board poll's, which can say why it
		// fails; the others' is their own poll's.
		if (hostId === selectedHostId) return { label: machineStatus.label, tone: machineStatusTone(machineStatus.tone) };
		const status = environmentStatus(hostStates.find((snapshot) => snapshot.hostId === hostId));
		return { label: status.label, tone: status.kind === "connected" ? "good" : status.kind === "offline" ? "bad" : "default" };
	}
	return (
		<View style={[styles.screen, { backgroundColor: t.bgBase }]} collapsable={false}>
			<StackRootBack />
			<SettingsForm testID="settings-form">
				{/* Each machine once, as the row that opens its page: testing it,
				    making it the default, reconnecting, editing and forgetting are
				    all there. */}
				<SettingsSection title="Machines">
					{pairedHosts.map((host) => (
						<MachineRow
							key={host.id}
							host={host}
							status={connectionStatus(host.id)}
							isDefault={machineCount > 1 && host.id === selectedHostId}
							onPress={() => router.push({ pathname: "/settings/machine/[id]", params: { id: host.id } })}
						/>
					))}
					{/* Nothing behind them to open: a fake has no page. */}
					{emulatedHosts.map((host) => (
						<MachineRow
							key={host.hostId}
							host={{ id: host.hostId, name: host.name, appearance: host.appearance }}
							status={connectionStatus(host.hostId)}
							isDefault={false}
							onPress={() => showToast(`${host.name} is a fake machine for this development build.`)}
						/>
					))}
					{/* A phone paired before machines were saved has only the selected
					    config, which stands in as the one row, with no page to open. */}
					{pairedHosts.length === 0 && paired ? (
						<SettingsValueRow
							icon="laptop"
							label={selectedHostName ?? "This machine"}
							value={machineStatus.label}
							tone={machineStatusTone(machineStatus.tone)}
							testID="settings-machine-current"
						/>
					) : null}
					{/* The fix for a default machine whose saved address or password
					    stopped working, named when another machine could be meant. */}
					{selectedHost && canRepairWithQr(failure) ? (
						<SettingsButtonRow
							icon="scan"
							label="Scan new code"
							value={pairedHosts.length > 1 ? selectedHost.name : undefined}
							onPress={() => router.push({ pathname: "/pair", params: { repairHostId: selectedHost.id } })}
							testID="settings-scan-new-code"
						/>
					) : null}
					<SettingsButtonRow icon="addMachine" label="Add a machine" onPress={() => router.navigate("/pair")} testID="settings-add-machine" />
				</SettingsSection>

				<SettingsSection title="Account" footer="Paired machines sync across signed-in devices.">
					{account ? (
						<SettingsValueRow icon="account" label="Orchestrator Cloud" value={account.email} loading={accountBusy} testID="settings-account" />
					) : (
						<SettingsButtonRow icon="account" label="Sign in to Orchestrator Cloud" loading={accountLoading || accountBusy} onPress={signIn} testID="settings-sign-in" />
					)}
					{account ? (
						<SettingsButtonRow icon="signOut" label="Sign out" destructive disabled={accountBusy} onPress={() => confirmSignOut(signOut)} testID="settings-sign-out" />
					) : null}
				</SettingsSection>

				<SettingsSection title="Preferences">
					<AppearanceRow />
					<ColorThemeRow />
					{notificationsRow}
				</SettingsSection>

				<SettingsSection title="Updates">
					<SoftwareUpdateRow />
				</SettingsSection>

				<SettingsSection title="Support" footer={`Orchestrator ${formatVersionLine(buildInfo())}`}>
					<ReportProblemRow toast={toast.show} />
					<FeatureRequestRow />
				</SettingsSection>

				{/* Last, and never in a release build. */}
				{__DEV__ ? (
					<SettingsSection title="Developer" footer="Development builds only. Adds fake machines to the machine chips and the Machines list above, to check how the app looks with many machines. Nothing is paired, saved or sent anywhere.">
						<SettingsToggleRow
							icon="emulateMachines"
							label="Emulate more machines"
							value={emulatingMachines}
							onValueChange={(next) => {
								haptics.select();
								setEmulatedMachines(next);
							}}
							testID="settings-emulate-machines"
						/>
					</SettingsSection>
				) : null}
			</SettingsForm>
			<ToastHost toast={toast} />
		</View>
	);
}

/**
 * Signing out forgets every machine the account synced to this phone, so it
 * asks first, the way the system confirms any destructive step.
 */
function confirmSignOut(signOut: () => void) {
	Alert.alert(
		"Sign out of Orchestrator Cloud?",
		"Machines synced from your account will be removed from this phone.",
		[
			{ text: "Cancel", style: "cancel" },
			{ text: "Sign out", style: "destructive", onPress: signOut },
		],
		{ cancelable: true },
	);
}

/**
 * Light, dark or follow the system: iOS's menu picker, and Material's
 * segmented buttons on Android, its single-select case.
 */
function AppearanceRow() {
	const { preference, setPreference } = useThemeState();
	return (
		<SettingsPickerRow
			icon="appearance"
			label="Appearance"
			options={APPEARANCE_OPTIONS}
			selectedValue={preference}
			onValueChange={(next) => {
				haptics.select();
				setPreference(next);
			}}
			appearance="segmented"
			testID="settings-appearance"
		/>
	);
}

/** The named colour theme; the picker is its own page, the list is too long for a menu. */
function ColorThemeRow() {
	const router = useRouter();
	const { style } = useThemeState();
	return (
		<SettingsLinkRow
			icon="theme"
			label="Theme"
			value={getThemeDefinition(style).label}
			onPress={() => router.push("/settings/theme")}
			testID="settings-theme"
		/>
	);
}

/**
 * The Notifications row, or nothing when it would not show. The screen places
 * the result rather than a component that may draw nothing, so the section
 * counts only rows that are there: one that rendered empty still took a place,
 * and the row above it lost its rounded bottom corners.
 */
function useNotificationsRow(): ReactElement | null {
	const { config, connection } = useApp();
	const [snapshot, setSnapshot] = useState<{ config: ServerConfig | null; status: PushStatus } | null>(null);
	const status = snapshot?.config === config ? snapshot.status : null;
	const refreshId = useRef(0);
	// The position the user just picked, shown while the change is applied.
	const [pending, setPending] = useState<boolean | null>(null);
	const refresh = useCallback(() => {
		const id = ++refreshId.current;
		getPushStatus(config).then((status) => {
			if (id === refreshId.current) setSnapshot({ config, status });
		}).catch(() => {});
	}, [config]);

	useFocusEffect(useCallback(() => refresh(), [refresh]));
	useEffect(() => refresh(), [connection, refresh]);
	const toggle = describePushToggle(status, config);

	async function onToggle(next: boolean) {
		if (toggle.blocked) {
			Alert.alert("Notifications are blocked", "Allow notifications for Orchestrator in your system settings, then come back.", [
				{ text: "Not now", style: "cancel" },
				{ text: "Open settings", onPress: openNotificationSettings },
			]);
			return;
		}
		setPending(next);
		let registered = false;
		try {
			if (!next) {
				await unregisterFromPush(config);
				haptics.tap();
			} else if (config) {
				const result = await registerForPush(config, { ask: true });
				registered = result.ok;
				if (result.ok) haptics.success();
				else {
					haptics.error();
					const { title, message } = describeRegisterFailure(result.reason, Platform.OS, result.status);
					Alert.alert(title, message);
				}
			}
			// Show the outcome now. A status read queues behind the connected
			// machines' push refresh, which can take seconds; it still runs below.
			if (status) setSnapshot({ config, status: { ...status, registered, granted: status.granted || registered } });
		} finally {
			setPending(null);
			refresh();
		}
	}

	// Notifications are on by default (PushManager registers as soon as the
	// system allows it), so the row only shows when they are off and can be
	// turned on here; one just switched stays up until the change lands.
	// Still being checked, or impossible here (no device, nothing paired): hidden too.
	if (pending === null && (toggle.value || toggle.disabled)) return null;

	return (
		<SettingsToggleRow
			icon="notifications"
			label="Notifications"
			value={pending ?? toggle.value}
			disabled={toggle.disabled || pending !== null}
			onValueChange={(next) => { void onToggle(next); }}
			testID="settings-notifications"
		/>
	);
}

function SoftwareUpdateRow() {
	const { isUpdatePending, isChecking, isDownloading } = Updates.useUpdates();
	const [otaManual, setOtaManual] = useState<UpdateOutcome | null>(null);
	const [manualBusy, setManualBusy] = useState(false);
	const [storeChecking, setStoreChecking] = useState(false);
	const [storeLast, setStoreLast] = useState<StoreRowResult | null>(null);
	const [storeCheck, setStoreCheck] = useState<StoreCheck | null>(null);

	const ota = describeUpdateRow({
		enabled: Updates.isEnabled,
		pending: isUpdatePending,
		phase: isDownloading ? "downloading" : isChecking || manualBusy ? "checking" : "idle",
		lastManual: otaManual,
	});
	const store = describeStoreRow({ enabled: !__DEV__, checking: storeChecking, last: storeLast });
	const row = describeSoftwareUpdateRow({ ota, store });

	// The same alert the launch check raises. Not now only closes it here: the
	// launch nudge's snooze counts prompts nobody asked for, and this one was.
	function presentStorePrompt(check: StoreCheck | null) {
		const confirmed = check?.updateAvailable === true;
		promptStoreUpdate({
			version: confirmed && Platform.OS === "ios" ? check?.storeVersion : floorTarget(VERSION_FLOOR),
			storeConfirmed: confirmed,
			onUpdate: () => { void openOrStartUpdate(check); },
		});
	}

	async function onPress() {
		if (row.action === "store") {
			presentStorePrompt(storeCheck);
			return;
		}
		if (row.action === "restart") {
			try { await Updates.reloadAsync(); } catch (error) { console.warn("[updates] reload failed", error); }
			return;
		}
		if (row.action !== "check") return;

		setManualBusy(Updates.isEnabled);
		setStoreChecking(!__DEV__);
		setOtaManual(null);
		setStoreLast(null);
		try {
			const [otaResult, nativeResult] = await Promise.all([
				checkAndDownload(Updates),
				__DEV__ ? Promise.resolve<StoreCheck | null>(null) : checkStore(),
			]);
			setOtaManual(otaResult);
			if (!__DEV__) {
				setStoreCheck(nativeResult);
				const floor = floorSignal(Application.nativeApplicationVersion, VERSION_FLOOR);
				const nativeOutcome = storeRowResult(nativeResult, tierOf(nativeResult, Platform.OS, floor));
				setStoreLast(nativeOutcome);
				if (nativeOutcome.kind === "available") presentStorePrompt(nativeResult);
				else if (nativeOutcome.kind === "error" || otaResult.kind === "error") haptics.error();
				else haptics.success();
			} else if (otaResult.kind === "error") haptics.error();
			else haptics.success();
		} finally {
			setManualBusy(false);
			setStoreChecking(false);
		}
	}

	// With nothing to do ("Automatic", or a check already running) it reads as
	// a plain value rather than a dimmed button.
	if (row.action === null) {
		return <SettingsValueRow icon="update" label="Software update" value={row.busy ? undefined : row.value} tone={row.tone} loading={row.busy} testID="settings-software-update" />;
	}
	return (
		<SettingsButtonRow
			icon="update"
			label="Software update"
			value={row.value}
			tone={row.tone}
			onPress={() => { void onPress(); }}
			testID="settings-software-update"
		/>
	);
}

function buildInfo(): BuildInfo {
	return {
		version: Application.nativeApplicationVersion ?? Constants.expoConfig?.version,
		build: Application.nativeBuildVersion,
		updateId: Updates.updateId,
		channel: Updates.channel,
		runtimeVersion: Updates.runtimeVersion,
		embedded: Updates.isEnabled ? Updates.isEmbeddedLaunch : undefined,
	};
}

function ReportProblemRow({ toast }: { toast: ToastController["show"] }) {
	const { config, connection } = useApp();
	function report() {
		const environment = {
			build: buildInfo(),
			platform: Platform.OS,
			osVersion: Platform.Version,
			deviceModel: Device.modelName,
			paired: !!config && isConfigured(config),
			connection,
		};
		// Copied as well as prefilled: a signed-out browser, or a GitHub app that
		// intercepts the link anyway, can drop the form and leave the reporter
		// typing into an empty box with no idea what to include.
		void Clipboard.setStringAsync(bugReportClipboard(environment));
		// Said once, so the copy is discoverable if the form comes up empty,
		// without leaving it on the row for the rest of the visit.
		toast("Details copied. Paste them in if the report comes up empty.", { duration: "long" });
		void openGitHub(bugReportOpenUrl(bugReportUrl(environment), Platform.OS));
	}
	return <SettingsButtonRow icon="report" label="Report a problem" onPress={report} testID="settings-report-problem" />;
}

function FeatureRequestRow() {
	return (
		<SettingsButtonRow
			icon="feature"
			label="Request a feature"
			value="Discord"
			onPress={() => { void Linking.openURL(discordFeatureRequestURL()).catch(() => {}); }}
			testID="settings-request-feature"
		/>
	);
}

const styles = StyleSheet.create({
	screen: { flex: 1 },
	center: { flex: 1, alignItems: "center", justifyContent: "center" },
});
