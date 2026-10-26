import { Host, Picker, Row, Switch } from "@expo/ui";
import { tint } from "@expo/ui/swift-ui/modifiers";
import { Feather } from "../../lib/icons";
import * as Application from "expo-application";
import * as Clipboard from "expo-clipboard";
import * as Device from "expo-device";
import Constants from "expo-constants";
import { Stack, useFocusEffect, useRouter } from "expo-router";
import * as Updates from "expo-updates";
import { Children, useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { ActivityIndicator, Alert, Linking, Platform, Pressable, ScrollView, StyleSheet, Text, View } from "react-native";
import { ApiError, pingServer } from "../../lib/api";
import { loadAccount, signInToAccount, signOutOfAccount, type Account } from "../../lib/account";
import { clearAccountHosts, syncAccountHosts } from "../../lib/accountHosts";
import { formatVersionLine, type BuildInfo } from "../../lib/appInfo";
import { bugReportClipboard, bugReportOpenUrl, bugReportUrl } from "../../lib/bugReport";
import { isConfigured, type ServerConfig } from "../../lib/config";
import { environmentSummary } from "../../lib/environmentView";
import { classifyConnectionFailure, describeConnectionFailure } from "../../lib/connectionError";
import { describeDesktopStatus } from "../../lib/desktopStatus";
import { discordFeatureRequestURL } from "../../lib/discord";
import { haptics } from "../../lib/haptics";
import { activeHost, loadHosts, type Host as PairedHost } from "../../lib/hosts";
import { toggleLayoutGrid, useLayoutGrid } from "../../lib/layoutGrid";
import { checkStore, openOrStartUpdate } from "../../lib/inAppUpdates";
import { MachineSelector } from "../../lib/MachineSelector";
import { canRepairWithQr } from "../../lib/machineRecovery";
import { promptStoreUpdate } from "../../lib/storeUpdatePrompt";
import { openGitHub } from "../../lib/openGitHub";
import { tunnelMayHaveRotated } from "../../lib/staleTunnel";
import { getPushStatus, openNotificationSettings, registerForPush, unregisterFromPush } from "../../lib/push";
import { describePushToggle, describeRegisterFailure, type PushStatus } from "../../lib/pushStatus";
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
import type { Theme } from "../../lib/theme";
import { getThemeDefinition } from "../../../shared/theme/palettes";
import { preferenceLabel, type ThemePreference } from "../../lib/themePreference";
import { toolbarIcon } from "../../lib/toolbarIcon";

// Light / Dark / System, in the desktop app's order and wording.
const THEME_OPTIONS: { value: ThemePreference; icon: keyof typeof Feather.glyphMap }[] = [
	{ value: "light", icon: "sun" },
	{ value: "dark", icon: "moon" },
	{ value: "system", icon: "smartphone" },
];
import { useTheme, useThemedStyles, useThemeState } from "../../lib/ThemeProvider";
import { checkAndDownload, describeUpdateRow, type UpdateOutcome } from "../../lib/updates";
import { VERSION_FLOOR } from "../../lib/versionFloor";
import { type, space } from "../../lib/tokens";
import { backOr } from "../../lib/backNavigation";


export { RouteErrorBoundary as ErrorBoundary } from "../../lib/RouteErrorBoundary";

export default function SettingsScreen() {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const router = useRouter();
	const { config, configured, selectedHostName, switchHost, connection, error, errorStatus, activeEndpoints, loading: appLoading, reloadConfig } = useApp();
	const scrollRef = useRef<ScrollView>(null);
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

	// iOS presents Settings as a sheet, which closes from its bar. Android's
	// Settings is a page, and its top app bar has the system back button.
	const headerItems = useMemo(() => Platform.OS === "ios" ? (
		<Stack.Toolbar placement="right">
			<Stack.Toolbar.Button icon={toolbarIcon("close")} accessibilityLabel="Close settings" onPress={() => backOr(router)} />
		</Stack.Toolbar>
	) : null, [router]);

	if (!loaded) return <View style={styles.center}>{headerItems}<ActivityIndicator color={t.accent} /></View>;

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
	const machineStatusColor = machineStatus.tone === "ok" ? t.green : machineStatus.tone === "error" ? t.red : undefined;
	return (
		<View style={styles.screen} collapsable={false}>
			{headerItems}
			<ScrollView
				ref={scrollRef}
				contentInsetAdjustmentBehavior="automatic"
				contentContainerStyle={styles.content}
				keyboardShouldPersistTaps="handled"
			>
				<SettingsSection title="Machines">
					<SettingsCard>
						<MachineSelector
							hosts={pairedHosts}
							selectedHostId={selectedHostId}
							selectedHostName={selectedHostName}
							statusLabel={machineStatus.label}
							statusColor={machineStatusColor}
							switchingHostId={switchingHostId}
							onSelect={(id) => { void selectHost(id); }}
						/>
						{selectedHost && canRepairWithQr(failure) ? (
							<CardRow
								icon="maximize"
								label="Scan new code"
								onPress={() => router.push({ pathname: "/pair", params: { repairHostId: selectedHost.id } })}
							/>
						) : null}
						<ConnectionTestRow paired={paired} selectedHostId={selectedHostId} />
						<CardRow icon="plus" label="Add a machine" showChevron={false} onPress={() => router.navigate("/pair")} />
						{selectedHost && !selectedConfigReady ? (
							<CardRow icon="refresh-cw" label="Retry selected machine" onPress={() => { void reloadConfig(); }} />
						) : null}
					</SettingsCard>
				</SettingsSection>

				<SettingsSection title="Environments">
					<SettingsCard>
						<CardRow
							icon="globe"
							label="Environments"
							value={environmentSummary(pairedHosts.length, selectedHost?.name ?? selectedHostName)}
							onPress={() => router.push("/settings/environments")}
						/>
					</SettingsCard>
				</SettingsSection>

				<SettingsSection title="Account" footer="Paired machines sync across signed-in devices.">
					<SettingsCard>
						<CardRow icon="user" label={account ? "Orchestrator Cloud" : "Sign in to Orchestrator Cloud"} value={account?.email} loading={accountLoading || accountBusy} onPress={account ? undefined : () => {
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
						}} />
						{account ? <CardRow icon="log-out" label="Sign out" onPress={() => confirmSignOut(() => {
							setAccountBusy(true);
							void signOutOfAccount().then(async () => {
								await clearAccountHosts();
								setAccount(null);
								setPairedHosts(await loadHosts());
								await reloadConfig();
							}).catch(() => Alert.alert("Could not sign out", "Try again.")).finally(() => setAccountBusy(false));
						})} disabled={accountBusy} /> : null}
					</SettingsCard>
				</SettingsSection>

				{__DEV__ ? (
					<SettingsSection title="Developer" footer="Layout grid draws the app's 4pt steps, with the 44pt control lines emphasised.">
						<SettingsCard>
							<LayoutGridRow />
						</SettingsCard>
					</SettingsSection>
				) : null}

				<SettingsSection title="Preferences">
					<SettingsCard>
						<AppearanceRow />
						<ColorThemeRow />
						<NotificationsRow />
					</SettingsCard>
				</SettingsSection>

				<SettingsSection title="Updates">
					<SettingsCard><SoftwareUpdateRow /></SettingsCard>
				</SettingsSection>

				<SettingsSection title="Support">
					<SettingsCard>
						<ReportProblemRow />
						<FeatureRequestRow />
					</SettingsCard>
				</SettingsSection>

				<VersionFooter />
			</ScrollView>
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

function SettingsSection({ title, footer, children }: { title: string; footer?: string; children: ReactNode }) {
	const styles = useThemedStyles(makeStyles);
	return (
		<View style={styles.section}>
			<Text style={styles.sectionTitle}>{title}</Text>
			{children}
			{footer ? <Text style={styles.sectionFooter}>{footer}</Text> : null}
		</View>
	);
}

function SettingsCard({ children }: { children: ReactNode }) {
	const styles = useThemedStyles(makeStyles);
	const rows = Children.toArray(children);
	return (
		<View style={styles.card}>
			{rows.map((row, index) => (
				<View key={(row as { key?: string }).key ?? index}>
					{index > 0 ? <View style={styles.separator} /> : null}
					{row}
				</View>
			))}
		</View>
	);
}

/** The theme options, inline inside Settings on Android. */
function ThemeChoices({ preference, onSelect }: { preference: ThemePreference; onSelect(next: ThemePreference): void }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	return (
		<View style={styles.inlineChoices}>
			{THEME_OPTIONS.map((option) => {
				const selected = preference === option.value;
				return (
					<Pressable
						key={option.value}
						accessibilityRole="button"
						accessibilityState={{ selected }}
						onPress={() => { haptics.select(); onSelect(option.value); }}
						style={({ pressed }) => [styles.inlineChoice, pressed && { opacity: 0.6 }]}
					>
						<Feather name={option.icon} size={15} color={selected ? t.textPrimary : t.textTertiary} />
						<Text style={[styles.inlineChoiceLabel, selected && { fontFamily: "Geist_600SemiBold", color: t.textPrimary, fontWeight: "600" }]}>{preferenceLabel(option.value)}</Text>
						{selected ? <Feather name="check" size={15} color={t.textPrimary} /> : null}
					</Pressable>
				);
			})}
		</View>
	);
}

function CardRow({
	icon,
	label,
	value,
	valueColor,
	onPress,
	disabled = false,
	loading = false,
	showChevron = true,
	right,
}: {
	icon: keyof typeof Feather.glyphMap;
	label: string;
	value?: string;
	valueColor?: string;
	onPress?: () => void;
	disabled?: boolean;
	loading?: boolean;
	showChevron?: boolean;
	right?: ReactNode;
}) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const content = (
		<>
			<Feather name={icon} size={17} color={disabled ? t.textFaint : t.textSecondary} style={styles.rowIcon} />
			<Text style={[styles.rowLabel, disabled && styles.disabled]} numberOfLines={1}>{label}</Text>
			{right ?? (loading ? <ActivityIndicator size="small" color={t.textTertiary} /> : (
				<>
					{value ? <Text style={[styles.rowValue, valueColor ? { color: valueColor } : null]} numberOfLines={1}>{value}</Text> : null}
					{onPress && showChevron ? <Feather name="chevron-right" size={17} color={t.textFaint} /> : null}
				</>
			))}
		</>
	);
	if (!onPress) return <View style={styles.row}>{content}</View>;
	return <Pressable disabled={disabled || loading} onPress={() => { haptics.tap(); onPress(); }} style={({ pressed }) => [styles.row, pressed && styles.rowPressed, disabled && styles.disabled]}>{content}</Pressable>;
}

function ConnectionTestRow({ paired, selectedHostId }: { paired: boolean; selectedHostId: string | null }) {
	const t = useTheme();
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

	const value = testing ? "Testing…" : result?.msg ?? "Test now";
	const color = result ? (result.ok ? t.green : t.red) : t.textSecondary;
	return (
		<CardRow
			icon="activity"
			label="Test connection"
			value={value}
			valueColor={color}
			disabled={!paired}
			loading={testing}
			onPress={paired ? test : undefined}
		/>
	);
}

function LayoutGridRow() {
	const active = useLayoutGrid();
	return (
		<CardRow
			icon="grid"
			label="Layout grid"
			value={active ? "On" : "Off"}
			onPress={() => {
				haptics.select();
				toggleLayoutGrid();
			}}
		/>
	);
}

function AppearanceRow() {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const { preference, scheme, setPreference } = useThemeState();
	const [open, setOpen] = useState(false);
	if (Platform.OS === "android") {
		// The choices expand in place rather than opening a sheet over Settings.
		return (
			<>
				<CardRow
					icon="sun"
					label="Appearance"
					value={preferenceLabel(preference)}
					onPress={() => { haptics.tap(); setOpen((value) => !value); }}
				/>
				{open ? <ThemeChoices preference={preference} onSelect={(next) => { setPreference(next); setOpen(false); }} /> : null}
			</>
		);
	}
	return (
		<CardRow
			icon="sun"
			label="Appearance"
			right={
				<View style={styles.appearancePicker}>
					<Host matchContents={{ horizontal: true }} style={{ height: 38 }} colorScheme={scheme}>
						{/* A menu picker draws its value in the tint, system blue on its own.
						    Secondary text is how a grouped list shows a row's value, and how
						    the rows around this one show theirs. */}
						<Row alignment="center" modifiers={[tint(t.textSecondary)]}>
							<Picker
								selectedValue={preference}
								onValueChange={(value) => {
									haptics.select();
									setPreference(String(value) as ThemePreference);
								}}
								appearance="menu"
								testID="settings-appearance"
							>
								<Picker.Item label="System" value="system" />
								<Picker.Item label="Light" value="light" />
								<Picker.Item label="Dark" value="dark" />
							</Picker>
						</Row>
					</Host>
				</View>
			}
		/>
	);
}

/** The named colour theme; the picker is its own screen, the list is too long for a menu. */
function ColorThemeRow() {
	const router = useRouter();
	const { style } = useThemeState();
	return (
		<CardRow
			icon="palette"
			label="Theme"
			value={getThemeDefinition(style).label}
			onPress={() => router.push("/settings/theme")}
		/>
	);
}

function NotificationsRow() {
	const { scheme } = useThemeState();
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
		<CardRow
			icon="bell"
			label="Notifications"
			disabled={toggle.disabled}
			right={
				// No tint: the switch keeps the system's colours, the green track on
				// iOS and the Material You scheme on Android.
				<Host style={{ width: 54, height: 34 }} colorScheme={scheme}>
					<Switch value={pending ?? toggle.value} disabled={toggle.disabled || pending !== null} onValueChange={onToggle} />
				</Host>
			}
		/>
	);
}

function SoftwareUpdateRow() {
	const t = useTheme();
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

	return (
		<CardRow
			icon="download-cloud"
			label="Software update"
			showChevron={false}
			value={row.value}
			valueColor={row.tone === "good" ? t.green : row.tone === "bad" ? t.red : undefined}
			loading={row.busy}
			onPress={row.action === null ? undefined : onPress}
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
	return <CardRow icon="help-circle" label="Report a problem" showChevron={false} value={copied ? "Details copied" : undefined} onPress={report} />;
}

function FeatureRequestRow() {
	return (
		<CardRow
			icon="message-circle"
			label="Request a feature"
			showChevron={false}
			value="Discord"
			onPress={() => { void Linking.openURL(discordFeatureRequestURL()).catch(() => {}); }}
		/>
	);
}

function VersionFooter() {
	const styles = useThemedStyles(makeStyles);
	return <Text style={styles.versionFooter}>Orchestrator {formatVersionLine(buildInfo())}</Text>;
}

const makeStyles = (t: Theme) => StyleSheet.create({
	screen: { flex: 1, backgroundColor: t.bgBase },
	center: { flex: 1, alignItems: "center", justifyContent: "center", backgroundColor: t.bgBase },
	content: { paddingHorizontal: space.lg, paddingTop: space.xs, paddingBottom: space.xxxl, gap: space.lg },
	section: { gap: space.xs },
	sectionTitle: { fontFamily: "Geist_600SemiBold", color: t.textTertiary, fontSize: type.caption1.fontSize, lineHeight: type.caption1.lineHeight, fontWeight: "600", paddingHorizontal: space.sm },
	sectionFooter: { fontFamily: "Geist_400Regular", color: t.textTertiary, fontSize: type.caption2.fontSize, lineHeight: type.caption2.lineHeight, paddingHorizontal: space.sm },
	card: { backgroundColor: t.bgElevated, borderRadius: 16, borderCurve: "continuous", overflow: "hidden" },
	separator: { height: StyleSheet.hairlineWidth, backgroundColor: t.borderSubtle, marginLeft: 50 },
	// Choices that expand inside a row's own card, indented under its label so
	// they read as belonging to the row above rather than as a new group.
	inlineChoices: { borderTopWidth: StyleSheet.hairlineWidth, borderTopColor: t.borderSubtle, backgroundColor: t.bgSubtle, paddingVertical: space.hair },
	inlineChoice: { minHeight: 44, flexDirection: "row", alignItems: "center", gap: space.sm, paddingLeft: 50, paddingRight: space.md },
	inlineChoiceLabel: { fontFamily: "Geist_400Regular", flex: 1, color: t.textSecondary, fontSize: type.subheadline.fontSize, lineHeight: type.subheadline.lineHeight },
	row: { minHeight: 52, flexDirection: "row", alignItems: "center", paddingHorizontal: space.md, gap: space.sm },
	rowPressed: { backgroundColor: t.bgElevatedHover },
	rowIcon: { fontFamily: "Geist_400Regular", width: 26, textAlign: "center" },
	rowLabel: { fontFamily: "Geist_600SemiBold", color: t.textPrimary, fontSize: type.subheadline.fontSize, lineHeight: type.subheadline.lineHeight, fontWeight: "600", flex: 1 },
	rowValue: { fontFamily: "Geist_400Regular", color: t.textSecondary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight, maxWidth: "42%" },
	appearancePicker: { width: 124, height: 38, alignItems: "flex-end", justifyContent: "center" },
	disabled: { opacity: 0.45 },
	versionFooter: { fontFamily: "Geist_400Regular", color: t.textFaint, fontSize: type.caption2.fontSize, lineHeight: type.caption2.lineHeight, textAlign: "center", marginTop: -6 },
});
