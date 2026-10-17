import { Feather } from "../lib/icons";
import { CameraView, useCameraPermissions } from "expo-camera";
import { useFocusEffect, useLocalSearchParams, useNavigation, useRouter } from "expo-router";
import { useCallback, useEffect, useRef, useState } from "react";
import { ActivityIndicator, Linking, Platform, Pressable, StyleSheet, Text, View } from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { pingServer } from "../lib/api";
import { pickNormalLens } from "../lib/cameraLens";
import { saveConfig } from "../lib/config";
import {
	classifyConnectionFailure,
	describeConnectionFailure,
	LOCAL_NETWORK_HINT,
	type ConnectionErrorCopy,
} from "../lib/connectionError";
import type { Theme } from "../lib/theme";
import { haptics } from "../lib/haptics";
import { clearOnboardingSkipped } from "../lib/onboardingStore";
import { completeOnboarding } from "../lib/onboardingNavigation";
import { pairFromCode } from "../lib/pairFlow";
import { registerForPush } from "../lib/push";
import { isLegacyPairingCode, parsePairingCode } from "../lib/pairingCode";
import { findHost, saveHost, setActiveHost, type Host } from "../lib/hosts";
import { loadAccount } from "../lib/account";
import { syncAccountHosts, unignoreAccountHost } from "../lib/accountHosts";
import { probeEndpoint } from "../lib/connectRuntime";
import { raceEndpoints } from "../lib/race";
import { connectSheetRoute, machineNameSheetRoute } from "../lib/sheetResult";
import { useApp } from "../lib/store";
import { Button, NumberedStep } from "../lib/ui";
import { useTheme, useThemedStyles } from "../lib/ThemeProvider";
import { MinimalBackButton } from "../lib/MinimalBackButton";
import { MOBILE_EVENTS } from "../lib/telemetry/events";
import { mobileTelemetry } from "../lib/telemetry/runtime";
import { iconSize, space, type } from "../lib/tokens";
import { backOr } from "../lib/backNavigation";

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
	const scanned = useRef(false);
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
		if (scanned.current || busy || !focused.current) return;
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
		scanned.current = true;
		await pair(data, offer);
	}

	// Races the code's endpoints, verifies the winner, then stores the machine.
	// The scanned code is kept so "Try again" can re-run the whole thing rather
	// than making the user re-scan.
	async function pair(code: string, offer?: ReturnType<typeof parsePairingCode>) {
		pendingCode.current = code;
		setBusy(true);
		setFailure(null);

		// A generic scan of a machine already in the list is also an in-place
		// refresh. Preserve its human-readable name instead of asking again.
		const knownHost = repairHost ?? (offer?.hostId ? await findHost(offer.hostId) : null);
		const result = await pairFromCode(code, {
			race: (endpoints, expectedHostId) => raceEndpoints(endpoints, expectedHostId, probeEndpoint),
			verify: async (config) => void (await pingServer(config)),
			chooseName: (suggested) => new Promise<string | null>((resolve) => {
				router.push(machineNameSheetRoute({ suggested, onSelect: resolve }));
			}),
			saveHost,
			setActiveHost,
		}, { repairHost: knownHost ?? undefined });

		if (!result.ok) {
			if (result.reason === "cancelled") {
				pendingCode.current = null;
				scanned.current = false;
				setBusy(false);
				return;
			}
			haptics.warning();
			// Use the first endpoint from the offer for error reporting, if available
			const firstEndpoint = offer?.endpoints[0];
			const errorTarget = firstEndpoint
				? { host: firstEndpoint.host, port: String(firstEndpoint.port), platform: Platform.OS }
				: { host: "", port: "", platform: Platform.OS };
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
						errorTarget,
					),
				);
			}
			setBusy(false);
			return;
		}

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

	function retry() {
		setFailure(null);
		rejected.current = null;
		if (pendingCode.current) {
			const offer = parsePairingCode(pendingCode.current);
			void pair(pendingCode.current, offer);
			return;
		}
		scanned.current = false;
	}

	const back = () => backOr(router);
	if (repairHost === undefined) {
		return <View style={styles.center}><ActivityIndicator color={t.accent} /></View>;
	}
	if (repairHostId && !repairHost) {
		return (
			<View style={[styles.screen, styles.missing, { paddingTop: insets.top + space.xl }]}>
				<Feather name="monitor-off" size={iconSize.xl} color={t.textTertiary} />
				<Text style={styles.missingTitle}>Machine no longer saved</Text>
				<Text style={styles.missingText}>Return to Settings and choose another machine.</Text>
				<Button title="Back to Settings" variant="ghost" onPress={back} />
			</View>
		);
	}

	return (
		<View style={[styles.screen, { paddingTop: insets.top }]}>
			<View style={styles.topBar}><MinimalBackButton onPress={back} /></View>

			{repairHost ? (
				<View style={styles.repairIntro}>
					<Text style={styles.repairTitle}>Reconnect {repairHost.name}</Text>
					<Text style={styles.repairText}>Scan the new code from Orchestrator → Settings → Connect Mobile on that machine.</Text>
				</View>
			) : <View style={styles.steps}>
					<NumberedStep n={1} title="Enable Orchestrator on a machine" compact />
					<NumberedStep n={2} title="Open Connect Mobile or run ao remote-host enable" compact />
					<NumberedStep n={3} title="Scan the code or enter details manually" compact />
			</View>}

			<View style={styles.viewfinder}>
				{permission?.granted ? (
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
						<Corner style={styles.cTL} />
						<Corner style={styles.cTR} />
						<Corner style={styles.cBL} />
						<Corner style={styles.cBR} />
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

			{failure ? (
				<View style={styles.errorBox}>
					<Feather name="alert-circle" size={iconSize.sm} color={t.red} />
					<View style={{ flex: 1 }}>
						<Text style={styles.errorText}>{failure.message}</Text>
						{failure.showLocalNetworkHint ? (
							<Text style={[styles.errorText, { marginTop: space.xs }]}>{LOCAL_NETWORK_HINT}</Text>
						) : null}
						<View style={styles.errorActions}>
							{/* Re-arms the scanner; without it a failed scan is a dead end,
							    since the camera intentionally stops after one attempt. */}
							<Button title="Try again" variant="ghost" icon="refresh-cw" onPress={retry} />
							{failure.showLocalNetworkHint ? (
								<Button
									title="Open settings"
									variant="ghost"
									icon="settings"
									onPress={() => Linking.openSettings()}
								/>
							) : null}
						</View>
					</View>
				</View>
			) : null}

			{/* Always reachable — including when the camera is permanently denied,
			    which would otherwise leave the user with no way forward at all. */}
			<Pressable
				onPress={() => { haptics.tap(); router.push(connectSheetRoute(() => void finish(), repairHost?.id)); }}
				style={[styles.manual, { paddingBottom: insets.bottom + 14 }]}
				accessibilityRole="button"
			>
				<Feather name="edit-3" size={iconSize.sm} color={t.textSecondary} />
				<Text style={styles.manualText}>Enter details manually</Text>
			</Pressable>
		</View>
	);
}

function Corner({ style }: { style: object }) {
	const styles = useThemedStyles(makeStyles);
	return <View style={[styles.corner, style]} />;
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
			<Feather name="camera-off" size={iconSize.xl} color={t.textTertiary} />
			<Text style={styles.gateTitle}>Camera access needed</Text>
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
					variant="ghost"
					icon="settings"
					onPress={() => Linking.openSettings()}
					style={{ marginTop: space.lg }}
				/>
			)}
		</View>
	);
}

const CORNER = 26;
const CORNER_W = 3;

const makeStyles = (t: Theme) =>
	StyleSheet.create({
	screen: { flex: 1, backgroundColor: t.bgBase },
	center: { flex: 1, alignItems: "center", justifyContent: "center", backgroundColor: t.bgBase },
	topBar: { paddingHorizontal: space.lg, paddingTop: space.xxs, paddingBottom: space.sm },

	steps: { paddingHorizontal: space.xl, paddingBottom: space.lg },
	repairIntro: { paddingHorizontal: space.xl, paddingBottom: space.lg },
	repairTitle: { fontFamily: "Geist_600SemiBold", color: t.textPrimary, fontSize: type.title3.fontSize, fontWeight: "600" },
	repairText: { fontFamily: "Geist_400Regular", color: t.textSecondary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight, marginTop: space.xs },
	missing: { paddingHorizontal: space.xl, gap: space.md },
	missingTitle: { fontFamily: "Geist_600SemiBold", color: t.textPrimary, fontSize: type.title3.fontSize, fontWeight: "600", textAlign: "center" },
	missingText: { fontFamily: "Geist_400Regular", color: t.textSecondary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight, textAlign: "center" },

	viewfinder: {
		flex: 1,
		marginHorizontal: space.lg,
		borderRadius: 16, borderCurve: "continuous",
		overflow: "hidden",
		// The viewfinder is a camera preview, so it stays dark in both themes.
		backgroundColor: "#0c0d10",
	},
	corner: {
		position: "absolute",
		width: CORNER,
		height: CORNER,
		borderColor: "rgba(255,255,255,0.92)",
	},
	cTL: { top: 14, left: 14, borderTopWidth: CORNER_W, borderLeftWidth: CORNER_W, borderTopLeftRadius: 6 },
	cTR: { top: 14, right: 14, borderTopWidth: CORNER_W, borderRightWidth: CORNER_W, borderTopRightRadius: 6 },
	cBL: { bottom: 14, left: 14, borderBottomWidth: CORNER_W, borderLeftWidth: CORNER_W, borderBottomLeftRadius: 6 },
	cBR: { bottom: 14, right: 14, borderBottomWidth: CORNER_W, borderRightWidth: CORNER_W, borderBottomRightRadius: 6 },

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

	errorBox: {
		flexDirection: "row",
		gap: space.sm,
		alignItems: "flex-start",
		backgroundColor: t.tintRed,
		borderRadius: 8, borderCurve: "continuous",
		padding: space.md,
		marginHorizontal: space.lg,
		marginTop: space.md,
	},
	errorText: { fontFamily: "Geist_400Regular", color: t.red, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight },
	errorActions: { flexDirection: "row", flexWrap: "wrap", gap: space.sm, marginTop: space.sm },

	manual: {
		flexDirection: "row",
		alignItems: "center",
		justifyContent: "center",
		gap: space.sm,
		paddingTop: space.lg,
	},
	manualText: { fontFamily: "Geist_600SemiBold", color: t.textSecondary, fontSize: type.subheadline.fontSize, fontWeight: "600" },
});

export { RouteErrorBoundary as ErrorBoundary } from "../lib/RouteErrorBoundary";
