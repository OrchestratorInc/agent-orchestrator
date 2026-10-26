import { CameraView, useCameraPermissions } from "expo-camera";
import { Stack, useFocusEffect, useLocalSearchParams, useNavigation, useRouter } from "expo-router";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { AppState, Linking, Platform, StyleSheet, Text, View } from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { pingServer } from "../lib/api";
import { AppSymbol } from "../lib/AppSymbol";
import { pickNormalLens } from "../lib/cameraLens";
import { saveConfig } from "../lib/config";
import {
	classifyConnectionFailure,
	describeConnectionFailure,
	describeUnreachableCode,
	isLocalNetworkHost,
	LOCAL_NETWORK_HINT,
	type ConnectionErrorCopy,
} from "../lib/connectionError";
import type { Theme } from "../lib/theme";
import { haptics } from "../lib/haptics";
import { clearOnboardingSkipped } from "../lib/onboardingStore";
import { completeOnboarding } from "../lib/onboardingNavigation";
import { pairFromCode, type MachineNameChoice } from "../lib/pairFlow";
import { registerForPush } from "../lib/push";
import { isLegacyPairingCode, parsePairingCode } from "../lib/pairingCode";
import { findHost, saveHost, setActiveHost, type Host } from "../lib/hosts";
import { loadAccount } from "../lib/account";
import { syncAccountHosts, unignoreAccountHost } from "../lib/accountHosts";
import { probeEndpoint } from "../lib/connectRuntime";
import { raceEndpoints } from "../lib/race";
import { createScanGate } from "../lib/scanGate";
import { mayAwaitLocalNetwork, raceAwaitingLocalNetwork } from "../lib/localNetworkWait";
import { loadLocalNetworkConfirmed, setLocalNetworkConfirmed } from "../lib/localNetworkStore";
import { connectSheetRoute, machineNameSheetRoute } from "../lib/sheetResult";
import { useApp } from "../lib/store";
import { Button, EmptyState, NumberedStep } from "../lib/ui";
import { Spinner } from "../lib/Spinner";
import type { PlatformSymbol } from "../lib/symbols";
import { useTheme, useThemedStyles } from "../lib/ThemeProvider";
import { NativeHeaderButton } from "../lib/native-header-button";
import { toolbarIcon } from "../lib/toolbarIcon";
import { MOBILE_EVENTS } from "../lib/telemetry/events";
import { mobileTelemetry } from "../lib/telemetry/runtime";
import { iconSize, onCamera, radius, space, type } from "../lib/tokens";
import { backOr } from "../lib/backNavigation";

// Whether the root stack draws this modal's bar (see app/_layout.tsx): a
// static choice, because a modal whose header appears after it mounts is
// rebuilt and would lose the scan in progress.
const NATIVE_BAR = Platform.OS === "ios";

// Typing the address in, as opposed to scanning it.
const MANUAL_ENTRY_SYMBOL: PlatformSymbol = { ios: "keyboard", android: "keyboard" };

export default function PairScreen() {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const router = useRouter();
	const navigation = useNavigation();
	const insets = useSafeAreaInsets();
	const { from, repairHostId } = useLocalSearchParams<{ from?: string; repairHostId?: string }>();
	const fromOnboarding = from === "onboarding";
	const { reloadConfig } = useApp();
	const [permission, requestPermission] = useCameraPermissions();
	const [failure, setFailure] = useState<ConnectionErrorCopy | null>(null);
	const [busy, setBusy] = useState(false);
	// Set while a scan keeps trying a LAN address that iOS may be holding behind
	// its Local Network prompt; shown as progress rather than as an error.
	const [awaitingLocalNetwork, setAwaitingLocalNetwork] = useState(false);
	const [gate] = useState(createScanGate);
	// Answering that prompt sends the app inactive and straight back to active —
	// the only sign iOS gives that the user has decided. It is approximate: Control
	// Center, a notification or Face ID cycle the same way, which at worst ends a
	// wait early or retries a failed code once.
	const promptAnsweredAt = useRef<number | null>(null);
	// Read from the AppState listener, which outlives any one render.
	const latestPair = useRef<(code: string) => void>(() => {});
	useEffect(() => {
		let wasInactive = false;
		const sub = AppState.addEventListener("change", (state) => {
			if (state === "inactive") wasInactive = true;
			else if (state === "active" && wasInactive) {
				wasInactive = false;
				promptAnsweredAt.current = Date.now();
				// Answered after the wait gave up: connect now rather than
				// leaving the error up until the user finds Try again. Only while
				// this screen is in front — the manual-connect and name sheets sit
				// on top of it, and a pair behind them could push a second sheet or
				// close the screen mid-typing. Checked before promptAnswered() so
				// the one retry is not spent while hidden.
				if (!focused.current) return;
				const code = gate.promptAnswered();
				if (code) latestPair.current(code);
			}
		});
		return () => sub.remove();
	}, [gate]);
	const mounted = useRef(true);
	useEffect(() => {
		mounted.current = true;
		return () => { mounted.current = false; };
	}, []);
	const [repairHost, setRepairHost] = useState<Host | null | undefined>(() => repairHostId ? undefined : null);
	useEffect(() => {
		if (!repairHostId) {
			setRepairHost(null);
			return;
		}
		let current = true;
		// An unreadable host list must not leave the screen on a spinner; it reads
		// as "no longer saved", which offers the way back.
		void findHost(repairHostId).catch(() => null).then((host) => {
			if (current) setRepairHost(host);
		});
		return () => { current = false; };
	}, [repairHostId]);
	// The camera keeps running under the manual-connect sheet, so a QR code in
	// frame would otherwise pair behind it. This replaces the old `manualOpen`
	// flag now that the sheet is a route pushed on top rather than local state.
	const focused = useRef(true);
	useFocusEffect(
		useCallback(() => {
			focused.current = true;
			return () => {
				focused.current = false;
			};
		}, []),
	);
	const rejected = useRef<string | null>(null);
	const pendingCode = useRef<string | null>(null);

	const camera = useRef<CameraView>(null);
	const [lens, setLens] = useState<string | undefined>(undefined);

	async function onCameraReady() {
		if (Platform.OS !== "ios" || lens) return;
		try {
			const lenses = (await camera.current?.getAvailableLensesAsync()) ?? [];
			if (typeof __DEV__ !== "undefined" && __DEV__) console.log("[pair] available lenses", lenses);
			setLens(pickNormalLens(lenses));
		} catch {
			/* keep the native default */
		}
	}

	async function finish() {
		await clearOnboardingSkipped();
		await reloadConfig(); // reconnect with the new credentials
		if (fromOnboarding) completeOnboarding(navigation);
		else backOr(router);
	}

	async function onScan({ data }: { data: string }) {
		if (busy || !focused.current) return;
		// Cheap reject first: the camera sees every barcode in frame, and only a
		// code we can actually parse should stop the scanner.
		const offer = parsePairingCode(data);
		if (!offer) {
			if (rejected.current !== data) {
				rejected.current = data;
				// A v1 code is a recognisable thing, not noise: say what to do
				// about it rather than claiming it is not a pairing code.
				const reason = isLegacyPairingCode(data) ? "outdated-desktop" : "not-ao-qr";
				setFailure(describeConnectionFailure(reason, { host: "", port: "", platform: Platform.OS }));
			}
			return;
		}
		rejected.current = null;
		if (!gate.sighting(data, Date.now())) return;
		// Felt the moment the code is read, so nobody has to watch the screen to
		// know the scan took; success or warning follows when the attempt ends.
		haptics.tap();
		await pair(data, offer);
	}

	// Races the code's endpoints, verifies the winner, then stores the machine.
	// The scanned code is kept so "Try again" can re-run the whole thing rather
	// than making the user re-scan.
	async function pair(code: string, offer?: ReturnType<typeof parsePairingCode>) {
		if (!gate.start()) return;
		try {
			await attemptPair(code, offer);
		} catch (e) {
			// An attempt that threw (storage, navigation) never reported how it
			// ended. Without this the scanner would stay claimed and ignore scans,
			// and the spinner would just stop with nothing to act on.
			console.warn("[pair] attempt failed", e);
			setAwaitingLocalNetwork(false);
			setBusy(false);
			if (gate.release()) {
				// Recorded as a failure so a code still in view waits to be pointed
				// at again instead of retrying every frame.
				gate.failed(code, Date.now());
				haptics.warning();
				setFailure({
					title: "Couldn't pair",
					icon: "alert-circle",
					message: "Something went wrong on this phone. Try again.",
					showLocalNetworkHint: false,
				});
			}
		}
	}

	async function attemptPair(code: string, offer?: ReturnType<typeof parsePairingCode>) {
		pendingCode.current = code;
		setBusy(true);
		setFailure(null);

		// A generic scan of a machine already in the list is also an in-place
		// refresh. Preserve its human-readable name instead of asking again.
		const knownHost = repairHost ?? (offer?.hostId ? await findHost(offer.hostId) : null);
		const localNetworkConfirmed = await loadLocalNetworkConfirmed();
		promptAnsweredAt.current = null;
		let awaitedLocalNetwork = false;
		const result = await pairFromCode(code, {
			race: (endpoints, expectedHostId) => {
				const once = () => raceEndpoints(endpoints, expectedHostId, probeEndpoint);
				if (!mayAwaitLocalNetwork(endpoints, Platform.OS, localNetworkConfirmed)) return once();
				awaitedLocalNetwork = true;
				return raceAwaitingLocalNetwork(once, {
					now: Date.now,
					sleep: (ms) => new Promise((resolve) => setTimeout(resolve, ms)),
					stopped: () => !mounted.current,
					answeredAt: () => promptAnsweredAt.current,
					onWaiting: () => setAwaitingLocalNetwork(true),
				});
			},
			verify: async (config) => void (await pingServer(config)),
			chooseName: (suggested, appearance, hostId) => new Promise<MachineNameChoice | null>((resolve) => {
				router.push(machineNameSheetRoute({ suggested, appearance, hostId, onSelect: resolve }));
			}),
			saveHost,
			setActiveHost,
		}, { repairHost: knownHost ?? undefined });

		setAwaitingLocalNetwork(false);
		if (!result.ok) {
			if (result.reason === "cancelled") {
				pendingCode.current = null;
				gate.cancelled();
				setBusy(false);
				return;
			}
			gate.failed(code, Date.now(), {
				awaitedLocalNetwork: awaitedLocalNetwork && result.reason === "none-reachable",
			});
			haptics.warning();
			if (result.reason === "wrong-machine") {
				setFailure({
					title: "Different machine",
					icon: "alert-circle",
					message: `That code belongs to a different machine. Scan the code from ${repairHost?.name ?? "the selected machine"}.`,
					showLocalNetworkHint: false,
				});
			} else if (result.reason === "name-required") {
				setFailure({
					title: "Machine name required",
					icon: "alert-circle",
					message: "Enter a machine name before saving this connection.",
					showLocalNetworkHint: false,
				});
			} else if (result.reason === "verify-failed") {
				// The race reached this endpoint, so whatever went wrong happened
				// there — name it, and let a 401 read as a rejected password.
				setFailure(
					describeConnectionFailure(classifyConnectionFailure(result.status), {
						host: result.endpoint.host,
						port: String(result.endpoint.port),
						platform: Platform.OS,
					}),
				);
			} else if (result.reason === "none-reachable" || result.reason === "no-candidates") {
				setFailure(describeUnreachableCode(offer?.endpoints ?? [], Platform.OS));
			} else {
				setFailure(
					describeConnectionFailure(
						result.reason === "not-ao-qr"
							? "not-ao-qr"
							: result.reason === "outdated-desktop"
								? "outdated-desktop"
								: result.reason === "incompatible"
									? "incompatible-host"
									: classifyConnectionFailure(undefined),
						{ host: "", port: "", platform: Platform.OS },
					),
				);
			}
			setBusy(false);
			return;
		}

		gate.succeeded();
		if (isLocalNetworkHost(result.config.host)) void setLocalNetworkConfirmed();
		// The rest of the app still runs off ServerConfig, so the winning
		// endpoint is written there as well as into the host list.
		await saveConfig(result.config);
		void loadAccount().then(async (account) => {
			if (account) {
				await unignoreAccountHost(account.id, result.host.id);
				await syncAccountHosts(account);
			}
		}).catch(() => {});
		mobileTelemetry()?.capture(MOBILE_EVENTS.paired, { method: "qr", from_onboarding: fromOnboarding });
		if (fromOnboarding) mobileTelemetry()?.capture(MOBILE_EVENTS.onboardingCompleted);
		haptics.success();
		await finish();
		// A successful scan counts as a deliberate trigger for the one-shot OS
		// prompt (see `ask` on registerForPushNow in lib/push.ts): a granted device
		// registers with this machine now instead of waiting on the Settings switch.
		void registerForPush(result.config, { ask: true }).catch((e) => console.warn("[push] registration failed", e));
	}

	latestPair.current = (code) => void pair(code, parsePairingCode(code));

	function retry() {
		setFailure(null);
		rejected.current = null;
		if (pendingCode.current) {
			const offer = parsePairingCode(pendingCode.current);
			void pair(pendingCode.current, offer);
		}
	}

	const back = () => backOr(router);
	// iOS presents this as a modal under a native bar, which closes it from an
	// xmark item. Android's modal is a full-screen page with no bar, so it keeps
	// the Material close button in its own top row below.
	const headerItems = useMemo(() => NATIVE_BAR ? (
		<Stack.Toolbar placement="right">
			<Stack.Toolbar.Button icon={toolbarIcon("close")} accessibilityLabel="Close" onPress={() => backOr(router)} />
		</Stack.Toolbar>
	) : null, [router]);
	if (repairHost === undefined) {
		return <View style={styles.center}>{headerItems}<Spinner /></View>;
	}
	if (repairHostId && !repairHost) {
		return (
			<View style={styles.screen}>
				{headerItems}
				<EmptyState
					icon="monitor-off"
					title="Machine no longer saved"
					message="Return to Settings and choose another machine."
					action={<Button title="Back to Settings" onPress={back} />}
				/>
			</View>
		);
	}

	const live = permission?.granted === true;
	return (
		<View style={[styles.screen, { paddingTop: NATIVE_BAR ? space.md : insets.top }]}>
			{headerItems}
			{/* Presented as a modal, so it closes rather than goes back. */}
			{NATIVE_BAR ? null : <View style={styles.topBar}><NativeHeaderButton icon="close" label="Close" onPress={back} /></View>}

			{repairHost ? (
				<View style={styles.repairIntro}>
					<Text accessibilityRole="header" style={styles.repairTitle}>Reconnect {repairHost.name}</Text>
					<Text style={styles.repairText}>Scan the new code from Orchestrator → Settings → Connect Mobile on that machine.</Text>
				</View>
			) : <View style={styles.steps}>
					<NumberedStep n={1} total={3} title="Enable Orchestrator on a machine" compact />
					<NumberedStep n={2} total={3} title="Open Connect Mobile or run ao remote-host enable" compact />
					<NumberedStep n={3} total={3} title="Scan the code or enter details manually" compact />
			</View>}

			<View style={[styles.viewfinder, live && styles.viewfinderLive]}>
				{live ? (
					<>
						<CameraView
							ref={camera}
							style={StyleSheet.absoluteFill}
							facing="back"
							selectedLens={lens}
							onCameraReady={onCameraReady}
							barcodeScannerSettings={{ barcodeTypes: ["qr"] }}
							onBarcodeScanned={onScan}
						/>
						{/* Where to hold the code. Decoration: the steps above say it. */}
						<View pointerEvents="none" style={styles.reticleLayer} accessibilityElementsHidden importantForAccessibility="no-hide-descendants">
							<View style={styles.reticle} />
						</View>
					</>
				) : (
					<CameraGate
						// `permission` is null only while the hook resolves on first render.
						loading={!permission}
						// Once permanently denied, requestPermission is a silent no-op —
						// only the system settings page can flip it back.
						canAskAgain={permission?.canAskAgain ?? true}
						onRequest={requestPermission}
					/>
				)}
			</View>

			{/* From the moment a scan starts: the first race alone can take ~20 s
			    when a code's tunnel is down, and a frozen-looking scanner after the
			    haptic reads as broken. The Local Network line only while waiting on it. */}
			{(busy || awaitingLocalNetwork) && !failure ? (
				<View style={styles.notice} accessibilityLiveRegion="polite">
					<View style={styles.noticeLead}><Spinner size={iconSize.sm} /></View>
					<View style={styles.noticeBody}>
						<Text style={styles.noticeTitle}>Connecting…</Text>
						{awaitingLocalNetwork ? (
							<Text style={styles.noticeText}>If asked, allow access to your local network.</Text>
						) : null}
					</View>
				</View>
			) : null}

			{failure ? (
				<View style={styles.notice} accessibilityLiveRegion="polite">
					<View style={styles.noticeLead}><AppSymbol name="error" size={iconSize.sm} color={t.red} /></View>
					<View style={styles.noticeBody}>
						<Text style={styles.noticeTitle}>{failure.title}</Text>
						<Text style={styles.noticeText}>{failure.message}</Text>
						{failure.showLocalNetworkHint ? <Text style={styles.noticeText}>{LOCAL_NETWORK_HINT}</Text> : null}
						<View style={styles.noticeActions}>
							{/* Re-runs the scanned code without pointing the camera at it again. */}
							<Button variant="ghost" icon="refresh" title="Try again" onPress={retry} />
							{failure.showLocalNetworkHint ? (
								<Button variant="ghost" icon="settings" title="Open settings" onPress={() => void Linking.openSettings()} />
							) : null}
						</View>
					</View>
				</View>
			) : null}

			{/* Always reachable — including when the camera is permanently denied,
			    which would otherwise leave the user with no way forward at all. */}
			<View style={[styles.manual, { paddingBottom: insets.bottom + space.sm }]}>
				<Button
					variant="ghost"
					icon={MANUAL_ENTRY_SYMBOL}
					title="Enter details manually"
					onPress={() => router.push(connectSheetRoute(() => void finish(), repairHost?.id))}
				/>
			</View>
		</View>
	);
}

function CameraGate({
	loading,
	canAskAgain,
	onRequest,
}: {
	loading: boolean;
	canAskAgain: boolean;
	onRequest: () => void;
}) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	if (loading) {
		return (
			<View style={styles.gate}>
				<Text style={styles.gateHint}>Starting camera…</Text>
			</View>
		);
	}
	return (
		<View style={styles.gate}>
			<AppSymbol name={{ ios: "camera", android: "photo_camera" }} size={iconSize.xl} color={t.textTertiary} />
			<Text accessibilityRole="header" style={styles.gateTitle}>Camera access needed</Text>
			<Text style={styles.gateHint}>
				{canAskAgain
					? "Orchestrator uses the camera only to read a pairing QR code."
					: "Camera access is turned off for Orchestrator. Enable it in system settings, or enter your details manually below."}
			</Text>
			{canAskAgain ? (
				// App Review 5.1.1(iv): the button ahead of the system permission
				// prompt must read "Continue"/"Next", never "Allow ...", so the grant
				// decision is only ever made in the system dialog itself.
				<Button title="Continue" onPress={onRequest} style={{ marginTop: space.lg }} />
			) : (
				<Button
					title="Open settings"
					variant="secondary"
					icon="settings"
					onPress={() => Linking.openSettings()}
					style={{ marginTop: space.lg }}
				/>
			)}
		</View>
	);
}

const makeStyles = (t: Theme) =>
	StyleSheet.create({
	screen: { flex: 1, backgroundColor: t.bgBase },
	center: { flex: 1, alignItems: "center", justifyContent: "center", backgroundColor: t.bgBase },
	topBar: { paddingHorizontal: space.lg, paddingTop: space.xxs, paddingBottom: space.sm },

	steps: { paddingHorizontal: space.xl, paddingBottom: space.lg },
	repairIntro: { paddingHorizontal: space.xl, paddingBottom: space.lg },
	repairTitle: { fontFamily: "Geist_600SemiBold", color: t.textPrimary, fontSize: type.title3.fontSize, fontWeight: "600" },
	repairText: { fontFamily: "Geist_400Regular", color: t.textSecondary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight, marginTop: space.xs },

	// A card in the theme until the camera runs: the permission prompt inside it
	// is themed text, which the dark preview backdrop would swallow in light mode.
	viewfinder: {
		flex: 1,
		marginHorizontal: space.lg,
		borderRadius: radius.lg, borderCurve: "continuous",
		overflow: "hidden",
		backgroundColor: t.bgElevated,
	},
	viewfinderLive: { backgroundColor: onCamera.backdrop },
	reticleLayer: { position: "absolute", top: 0, right: 0, bottom: 0, left: 0, alignItems: "center", justifyContent: "center" },
	// One rounded square, as the system code scanners frame a code. Square from
	// the width; the height cap only bites in a viewfinder squeezed by large text.
	reticle: {
		width: "62%",
		aspectRatio: 1,
		maxHeight: "80%",
		borderRadius: radius.xl,
		borderCurve: "continuous",
		borderWidth: 3,
		borderColor: onCamera.reticle,
	},

	gate: { flex: 1, alignItems: "center", justifyContent: "center", padding: space.xxl },
	gateTitle: { fontFamily: "Geist_600SemiBold", color: t.textPrimary, fontSize: type.callout.fontSize, fontWeight: "600", marginTop: space.md },
	gateHint: { fontFamily: "Geist_400Regular",
		color: t.textSecondary,
		fontSize: type.footnote.fontSize,
		lineHeight: type.footnote.lineHeight,
		textAlign: "center",
		marginTop: space.sm,
		maxWidth: 300,
	},

	// Connecting and failure share one quiet slot under the viewfinder: no fill
	// or border, aligned with the numbered steps above.
	notice: {
		flexDirection: "row",
		gap: space.sm,
		alignItems: "flex-start",
		paddingHorizontal: space.xl,
		marginTop: space.lg,
	},
	noticeLead: { width: iconSize.sm, height: type.subheadline.lineHeight, alignItems: "center", justifyContent: "center" },
	noticeBody: { flex: 1, gap: space.xxs },
	noticeTitle: { fontFamily: "Geist_600SemiBold", color: t.textPrimary, fontSize: type.subheadline.fontSize, lineHeight: type.subheadline.lineHeight, fontWeight: "600" },
	noticeText: { fontFamily: "Geist_400Regular", color: t.textSecondary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight },
	// Text buttons pad their label for the press state; pulled back by that
	// padding so the first label lines up with the message above it.
	noticeActions: { flexDirection: "row", flexWrap: "wrap", columnGap: space.xs, marginLeft: -space.md },

	manual: { alignItems: "center", paddingTop: space.sm },
});

export { RouteErrorBoundary as ErrorBoundary } from "../lib/RouteErrorBoundary";
