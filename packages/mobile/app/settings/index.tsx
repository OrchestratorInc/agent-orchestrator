import * as Application from "expo-application";
import * as Clipboard from "expo-clipboard";
import * as Device from "expo-device";
import Constants from "expo-constants";
import { Stack, useFocusEffect, useRouter } from "expo-router";
import * as Updates from "expo-updates";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Alert, Linking, Platform, StyleSheet, View } from "react-native";
import { ApiError, pingServer } from "../../lib/api";
import { loadAccount, signInToAccount, signOutOfAccount, type Account } from "../../lib/account";
import { clearAccountHosts, syncAccountHosts } from "../../lib/accountHosts";
import { formatVersionLine, type BuildInfo } from "../../lib/appInfo";
import { backOr } from "../../lib/backNavigation";
import { bugReportClipboard, bugReportOpenUrl, bugReportUrl } from "../../lib/bugReport";
import { isConfigured, type ServerConfig } from "../../lib/config";
import { classifyConnectionFailure, describeConnectionFailure } from "../../lib/connectionError";
import { describeDesktopStatus } from "../../lib/desktopStatus";
import { discordFeatureRequestURL } from "../../lib/discord";
import { environmentSummary } from "../../lib/environmentView";
import { haptics } from "../../lib/haptics";
import { activeHost, loadHosts, type Host as PairedHost } from "../../lib/hosts";
import { checkStore, openOrStartUpdate } from "../../lib/inAppUpdates";
import { toggleLayoutGrid, useLayoutGrid } from "../../lib/layoutGrid";
import { MachineSelector, MachineStatusRow } from "../../lib/MachineSelector";
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
} from "../../lib/SettingsForm";
import { Spinner } from "../../lib/Spinner";
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
import { toolbarIcon } from "../../lib/toolbarIcon";
import { checkAndDownload, describeUpdateRow, type UpdateOutcome } from "../../lib/updates";
import { VERSION_FLOOR } from "../../lib/versionFloor";
import { getThemeDefinition } from "../../../shared/theme/palettes";

// Light / Dark / System, in the desktop app's order and wording.
const APPEARANCE_OPTIONS: readonly SettingsOption<ThemePreference>[] = (["light", "dark", "system"] as const).map((value) => ({ value, label: preferenceLabel(value) }));

export { RouteErrorBoundary as ErrorBoundary } from "../../lib/RouteErrorBoundary";

export default function SettingsScreen() {
	const t = useTheme();
	const router = useRouter();
	const { config, configured, selectedHostName, switchHost, connection, error, errorStatus, activeEndpoints, loading: appLoading, reloadConfig } = useApp();
	const [pairedHosts, setPairedHosts] = useState<PairedHost[]>([]);
	const [selectedHostId, setSelectedHostId] = useState<string | null>(null);
	const [switchingHostId, setSwitchingHostId] = useState<string | null>(null);
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

	async function selectHost(id: string) {
		if (id === selectedHostId || switchingHostId !== null) return;
		setSwitchingHostId(id);
		try {
			await switchHost(id);
			setSelectedHostId(id);
		} catch {
			Alert.alert("Could not change default machine", "Try again from Settings.");
		} finally {
			setSwitchingHostId(null);
		}
	}

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

	// iOS presents Settings as a sheet, which closes from its bar. Android's
	// Settings is a page, and its top app bar has the system back button.
	const headerItems = useMemo(() => Platform.OS === "ios" ? (
		<Stack.Toolbar placement="right">
			<Stack.Toolbar.Button icon={toolbarIcon("close")} accessibilityLabel="Close settings" onPress={() => backOr(router)} />
		</Stack.Toolbar>
	) : null, [router]);

	if (!loaded) {
		return (
			<View style={[styles.center, { backgroundColor: t.bgBase }]}>
				{headerItems}
				<Spinner size="large" accessibilityLabel="Loading settings" />
			</View>
		);
	}

	const paired = pairedHosts.length > 0 || configured || selectedHostName !== null;
	const selectedHost = pairedHosts.find((host) => host.id === selectedHostId);
	const selectedConfigReady = !!config && isConfigured(config) && (!selectedHost || config.hostId === selectedHost.id);
	// Only a poll that actually failed is a failure. Before the first tick lands
	// errorStatus is null too, which on its own would read as unreachable.
	const classified = error ? classifyConnectionFailure(errorStatus ?? undefined) : null;
	const failure =
		classified === "unreachable" && tunnelMayHaveRotated(activeEndpoints, config?.endpointKind, connection === "open")
			? "tunnel-rotated"
			: classified ?? (paired && !config && !appLoading ? "unreachable" : null);
	const machineStatus = describeDesktopStatus({ configured: paired, connection, failure });
	return (
		<View style={[styles.screen, { backgroundColor: t.bgBase }]} collapsable={false}>
			{headerItems}
			<SettingsForm testID="settings-form">
				<SettingsSection title="Machines">
					<MachineSelector
						hosts={pairedHosts}
						selectedHostId={selectedHostId}
						selectedHostName={selectedHostName}
						switchingHostId={switchingHostId}
						onSelect={(id) => { void selectHost(id); }}
					/>
					<MachineStatusRow hosts={pairedHosts} statusLabel={machineStatus.label} statusTone={machineStatus.tone} switchingHostId={switchingHostId} />
					{selectedHost && canRepairWithQr(failure) ? (
						<SettingsButtonRow
							icon="scan"
							label="Scan new code"
							onPress={() => router.push({ pathname: "/pair", params: { repairHostId: selectedHost.id } })}
							testID="settings-scan-new-code"
						/>
					) : null}
					<ConnectionTestRow paired={paired} selectedHostId={selectedHostId} />
					<SettingsButtonRow icon="addMachine" label="Add a machine" onPress={() => router.navigate("/pair")} testID="settings-add-machine" />
					{selectedHost && !selectedConfigReady ? (
						<SettingsButtonRow icon="retry" label="Retry selected machine" onPress={() => { void reloadConfig(); }} testID="settings-retry-machine" />
					) : null}
				</SettingsSection>

				<SettingsSection title="Environments">
					<SettingsLinkRow
						icon="environments"
						label="Environments"
						value={environmentSummary(pairedHosts.length, selectedHost?.name ?? selectedHostName)}
						onPress={() => router.push("/settings/environments")}
						testID="settings-environments"
					/>
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

				{__DEV__ ? (
					<SettingsSection title="Developer" footer="Layout grid draws the app's 4pt steps, with the 44pt control lines emphasised.">
						<LayoutGridRow />
					</SettingsSection>
				) : null}

				<SettingsSection title="Preferences">
					<AppearanceRow />
					<ColorThemeRow />
					<NotificationsRow />
				</SettingsSection>

				<SettingsSection title="Updates">
					<SoftwareUpdateRow />
				</SettingsSection>

				<SettingsSection title="Support" footer={`Orchestrator ${formatVersionLine(buildInfo())}`}>
					<ReportProblemRow />
					<FeatureRequestRow />
				</SettingsSection>
			</SettingsForm>
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

function ConnectionTestRow({ paired, selectedHostId }: { paired: boolean; selectedHostId: string | null }) {
	const { config, reloadConfig, refresh } = useApp();
	const [testing, setTesting] = useState(false);
	const [result, setResult] = useState<{ ok: boolean; msg: string } | null>(null);

	useEffect(() => setResult(null), [config?.host, config?.httpPort, selectedHostId]);

	async function test() {
		setTesting(true);
		setResult(null);
		// Race every known path first, like the app itself does, rather than
		// pinging only the last address that happened to win. Without the
		// endpoint refresh: it is authenticated, so with a stale password it and
		// the ping would spend two failed attempts per tap towards the lockout.
		let target = config;
		let rejected = false;
		try {
			target = await reloadConfig({ refreshEndpoints: false });
			if (!target) throw new Error("No verified connection to the selected machine");
			await pingServer(target);
			haptics.success();
			setResult({ ok: true, msg: "Connected" });
		} catch (error) {
			haptics.error();
			const status = error instanceof ApiError ? error.status : undefined;
			const failure = classifyConnectionFailure(status);
			rejected = failure === "auth" || failure === "rate-limited";
			const { title } = describeConnectionFailure(failure, {
				host: target?.host ?? "",
				port: target?.httpPort ?? "",
				platform: Platform.OS,
			});
			setResult({ ok: false, msg: title });
		} finally {
			// Poll now so the status row above lands on the same answer instead
			// of waiting out the poll interval — unless the desktop rejected the
			// password: another request would spend a second failed attempt
			// towards its lockout, and the poll already reports a rejection on its
			// own. Not awaited: against a dead address it is another full request
			// timeout.
			if (!rejected) void refresh();
			setTesting(false);
		}
	}

	return (
		<SettingsButtonRow
			icon="testConnection"
			label="Test connection"
			value={testing ? "Testing…" : result?.msg ?? "Test now"}
			tone={result ? (result.ok ? "good" : "bad") : "default"}
			disabled={!paired}
			loading={testing}
			onPress={() => { void test(); }}
			testID="settings-test-connection"
		/>
	);
}

function LayoutGridRow() {
	const active = useLayoutGrid();
	return <SettingsToggleRow icon="layoutGrid" label="Layout grid" value={active} onValueChange={(next) => toggleLayoutGrid(next)} testID="settings-layout-grid" />;
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

function NotificationsRow() {
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

function ReportProblemRow() {
	const { config, connection } = useApp();
	// Says what just happened, so the copy is discoverable if the form comes up empty.
	const [copied, setCopied] = useState(false);
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
		setCopied(true);
		void openGitHub(bugReportOpenUrl(bugReportUrl(environment), Platform.OS));
	}
	return <SettingsButtonRow icon="report" label="Report a problem" value={copied ? "Details copied" : undefined} onPress={report} testID="settings-report-problem" />;
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
