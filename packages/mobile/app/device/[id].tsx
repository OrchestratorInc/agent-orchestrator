import { Feather } from "../../lib/icons";
import { useLocalSearchParams, useNavigation, useRouter } from "expo-router";
import { useCallback, useEffect, useLayoutEffect, useRef, useState } from "react";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { MenuView, type MenuAction, type MenuComponentRef, type NativeActionEvent } from "@expo/ui/community/menu";
import { ActivityIndicator, Image, Platform, Pressable, StatusBar, StyleSheet, Text, View, type GestureResponderEvent, type LayoutChangeEvent } from "react-native";
import { closeSessionDevice, getDeviceStatus, getSessionDevice, openSessionDevice, pressSessionDeviceButton, type DeviceStatus, type DeviceSummary, type SessionDeviceLease } from "../../lib/api";
import { authHeaders } from "../../lib/config";
import { userFacingError } from "../../lib/connectionError";
import { clampPoint, missingPlatforms, deviceAvccURL, deviceInputURL, deviceMjpegURL, deviceSocketHeaders, deviceVideoURL, emptyFrameLayers, fetchDeviceFrame, frameLoaded, withFrame, encodeButton, encodeTouch, openDeviceInput, type DeviceButton, type TouchPhase } from "../../lib/deviceInput";
import { DeviceTouchRipple, type RipplePoint } from "../../lib/DeviceTouchRipple";
import { DeviceStreamView } from "../../modules/device-stream";
import { useDeviceQuality } from "../../lib/deviceQuality";
import { haptics } from "../../lib/haptics";
import { hostRouteMatches } from "../../lib/hostRoute";
import { SettingsButtonRow, SettingsForm, SettingsSection } from "../../lib/SettingsForm";
import { HostScope, useApp } from "../../lib/store";
import type { Theme } from "../../lib/theme";
import { useTheme, useThemeState, useThemedStyles } from "../../lib/ThemeProvider";
import { iconSize, space, type } from "../../lib/tokens";
import { Button, EmptyState } from "../../lib/ui";

// Screenshots stand in for the desktop's live stream: React Native's Image
// cannot render MJPEG, and a still every few hundred milliseconds is enough to
// follow an agent and tap along.
/** How long a simulator's H.264 stream may take to start before MJPEG takes over. */
const AVCC_START_MS = 10_000;
const FRAME_INTERVAL_MS = 350;
const LEASE_POLL_MS = 2_000;
const DEVICES_POLL_MS = 5_000;

/** The session's simulator or emulator: watch it and tap it from the phone. */
export default function SessionDeviceScreen() {
	const { hostId } = useLocalSearchParams<{ hostId?: string }>();
	return hostId ? <HostScope key={hostId} hostId={hostId}><SessionDeviceContent /></HostScope> : <SessionDeviceContent />;
}

function SessionDeviceContent() {
	const { id: rawID, hostId: routeHostId } = useLocalSearchParams<{ id: string; hostId?: string }>();
	const sessionID = String(rawID ?? "");
	const navigation = useNavigation();
	const router = useRouter();
	const { config, currentHostId, connection } = useApp();
	const hostMatches = hostRouteMatches(routeHostId, currentHostId);
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const [lease, setLease] = useState<SessionDeviceLease | null | undefined>(undefined);
	const [error, setError] = useState<string>();

	useEffect(() => {
		if (!hostMatches || !config || !sessionID) return;
		const controller = new AbortController();
		const load = () => getSessionDevice(config, sessionID, { signal: controller.signal })
			.then((next) => { setLease(next); setError(undefined); })
			.catch((cause) => { if (!controller.signal.aborted) setError(userFacingError(cause)); });
		void load();
		const poll = setInterval(() => void load(), LEASE_POLL_MS);
		return () => { controller.abort(); clearInterval(poll); };
	}, [config, hostMatches, sessionID]);

	useLayoutEffect(() => {
		navigation.setOptions({ title: lease?.device.name ?? "Device" });
	}, [lease?.device.name, navigation]);

	if (!hostMatches) return <View style={styles.center}><EmptyState icon="smartphone" title="This device belongs to another machine" message="Open it from that machine's session." action={<Button title="Open board" icon="activity" onPress={() => router.navigate("/")} />} /></View>;
	if (!config && connection === "closed") return <View style={styles.center}><EmptyState icon="wifi-off" title="Machine offline" message="The device shows once the app reconnects." /></View>;
	if (!config || lease === undefined) {
		return <View style={styles.center}>
			{error ? <EmptyState icon="alert-triangle" title="Couldn't load the device" message={error} /> : <ActivityIndicator color={t.accent} />}
		</View>;
	}
	if (!lease) return <DevicePicker config={config} sessionID={sessionID} onOpened={setLease} />;
	return <DeviceLive config={config} lease={lease} onReleased={() => setLease(null)} sessionID={sessionID} />;
}

/**
 * No device yet, or it was given back: the machine's simulators and emulators,
 * running ones first, to open one for this session as the desktop does. One
 * another session holds says so and cannot be taken.
 */
function DevicePicker({ config, sessionID, onOpened }: {
	config: NonNullable<ReturnType<typeof useApp>["config"]>;
	sessionID: string;
	onOpened: (lease: SessionDeviceLease) => void;
}) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const [status, setStatus] = useState<DeviceStatus>();
	const [loadError, setLoadError] = useState<string>();
	const [openError, setOpenError] = useState<string>();
	const [opening, setOpening] = useState<string>();

	useEffect(() => {
		const controller = new AbortController();
		const load = () => getDeviceStatus(config, { signal: controller.signal })
			.then((next) => { setStatus(next); setLoadError(undefined); })
			.catch((cause) => { if (!controller.signal.aborted) setLoadError(userFacingError(cause)); });
		void load();
		const poll = setInterval(() => void load(), DEVICES_POLL_MS);
		return () => { controller.abort(); clearInterval(poll); };
	}, [config]);

	const open = (device: DeviceSummary) => {
		haptics.tap();
		setOpening(device.id);
		setOpenError(undefined);
		void openSessionDevice(config, sessionID, { deviceId: device.id, platform: device.platform })
			.then((lease) => { if (lease) onOpened(lease); })
			.catch((cause) => setOpenError(userFacingError(cause)))
			.finally(() => setOpening(undefined));
	};

	if (!status) {
		return <View style={styles.center}>
			{loadError ? <EmptyState icon="alert-triangle" title="Couldn't load devices" message={loadError} /> : <ActivityIndicator color={t.accent} />}
		</View>;
	}
	const missing = missingPlatforms(status.platforms);
	if (!status.platforms.some((platform) => platform.available)) return <View style={styles.center}><EmptyState icon="smartphone" title="This machine can't run a simulator yet" message={missing} /></View>;
	if (!status.enabled || status.state === "off") return <View style={styles.center}><EmptyState icon="smartphone" title="Device support is off" message="Turn it on from a session's Device tab on the desktop." /></View>;
	if (status.state === "installing") return <View style={styles.center}><EmptyState icon="smartphone" title="Installing device tools…" busy /></View>;
	if (status.state === "error") return <View style={styles.center}><EmptyState icon="alert-triangle" title="Device support could not start." message={status.error} /></View>;
	if (status.devices.length === 0) return <View style={styles.center}><EmptyState icon="smartphone" title="No simulators or emulators found" message={["Create one in Xcode or Android Studio.", missing].filter(Boolean).join("\n\n")} /></View>;

	const heldBy = new Map(status.leases.map((held) => [held.device.id, held.sessionId]));
	const devices = [...status.devices].sort((a, b) => Number(b.booted) - Number(a.booted) || a.platform.localeCompare(b.platform) || a.name.localeCompare(b.name));
	return (
		<SettingsForm testID="device-picker">
			<SettingsSection title="Open a device" footer={[openError, missing].filter(Boolean).join("\n\n") || undefined}>
				{devices.map((device) => {
					const owner = heldBy.get(device.id);
					const takenByOther = owner !== undefined && owner !== sessionID;
					return (
						<SettingsButtonRow
							key={device.id}
							icon="device"
							label={device.name}
							value={takenByOther ? `In use by ${owner}` : device.booted ? "Running" : device.version}
							disabled={takenByOther || opening !== undefined}
							loading={opening === device.id}
							onPress={() => open(device)}
							testID={`device-option-${device.id}`}
						/>
					);
				})}
			</SettingsSection>
		</SettingsForm>
	);
}

function DeviceLive({ config, lease, onReleased, sessionID }: {
	config: NonNullable<ReturnType<typeof useApp>["config"]>;
	lease: SessionDeviceLease;
	onReleased: () => void;
	sessionID: string;
}) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const insets = useSafeAreaInsets();
	const { device } = lease;
	// The latest frame stays up until the next has loaded behind it (see
	// FrameLayers), so polling never flashes an empty screen. Frames are pulled one after another: a slow
	// network slows the picture rather than piling up requests.
	const [layers, setLayers] = useState(emptyFrameLayers);
	const [aspect, setAspect] = useState(9 / 19.5);
	const [live, setLive] = useState(false);
	// Both platforms draw their stream natively (Android's H.264, iOS's MJPEG);
	// a build without the native view polls screenshots.
	const NativeStream = DeviceStreamView;
	const [streaming, setStreaming] = useState(false);
	// A simulator streams as H.264 (~5 Mbit/s scrolling, against ~112 for
	// MJPEG). A daemon without that route never starts it: MJPEG after 10 s.
	const [avccFailed, setAvccFailed] = useState(false);
	// Once it has played it is supported: a later outage reconnects, not falls back.
	const avccPlayed = useRef(false);
	const [ripple, setRipple] = useState<RipplePoint | null>(null);
	const rippleCount = useRef(0);
	// The device gets the whole screen, as a phone's own screen would: no stack
	// header and no status bar while it is live. The controls float over it.
	const navigation = useNavigation();
	const router = useRouter();
	const { scheme } = useThemeState();
	const menu = useRef<MenuComponentRef>(null);
	// A swipe from the screen's edge belongs to the device (iOS's back swipe
	// inside the app, Android's drawers), not to the stack: Done leaves.
	useLayoutEffect(() => {
		navigation.setOptions({ headerShown: false, gestureEnabled: false, fullScreenGestureEnabled: false });
		return () => navigation.setOptions({ headerShown: true, gestureEnabled: true, fullScreenGestureEnabled: true });
	}, [navigation]);
	// The stream's quality follows the connection and how the stream arrives.
	const { report: reportStream } = useDeviceQuality(config, sessionID);
	const socket = useRef<WebSocket | null>(null);
	const size = useRef({ width: 0, height: 0 });

	useEffect(() => {
		if (NativeStream) return;
		const controller = new AbortController();
		let timer: ReturnType<typeof setTimeout> | undefined;
		const next = () => {
			fetchDeviceFrame(config, sessionID, controller.signal)
				.then((uri) => { if (!controller.signal.aborted) setLayers((current) => withFrame(current, uri)); })
				.catch(() => {})
				.then(() => { if (!controller.signal.aborted) timer = setTimeout(next, FRAME_INTERVAL_MS); });
		};
		next();
		return () => { controller.abort(); if (timer) clearTimeout(timer); };
	}, [config, NativeStream, sessionID]);

	// Shut down outside AO: say so rather than hold the last frame.
	const off = !device.booted;
	// The touch socket is held only while the device is on: one opened while it
	// is off goes nowhere once it boots, so turning it on opens a fresh one.
	useEffect(() => {
		if (off) {
			setLive(false);
			return;
		}
		let stopped = false;
		let retry: ReturnType<typeof setTimeout> | undefined;
		const connect = () => {
			const ws = openDeviceInput(config, deviceInputURL(config, sessionID, device.platform, device.id));
			socket.current = ws;
			ws.onopen = () => setLive(true);
			ws.onclose = () => {
				if (stopped || socket.current !== ws) return;
				setLive(false);
				retry = setTimeout(connect, 1_500);
			};
			ws.onerror = () => ws.close();
		};
		connect();
		return () => {
			stopped = true;
			if (retry) clearTimeout(retry);
			const ws = socket.current;
			socket.current = null;
			ws?.close();
		};
	}, [config, device.id, device.platform, off, sessionID]);

	const send = useCallback((payload: ArrayBuffer | string | null) => {
		const ws = socket.current;
		if (payload !== null && ws?.readyState === WebSocket.OPEN) ws.send(payload);
	}, []);
	const touch = useCallback((phase: TouchPhase, event: GestureResponderEvent) => {
		const point = clampPoint(event.nativeEvent.locationX, event.nativeEvent.locationY, size.current.width, size.current.height);
		send(encodeTouch(device.platform, phase, point.x, point.y, event.nativeEvent.timestamp));
		if (phase === "begin") setRipple({ x: event.nativeEvent.locationX, y: event.nativeEvent.locationY, id: ++rippleCount.current });
	}, [device.platform, send]);
	const press = (button: DeviceButton) => {
		haptics.tap();
		const viaSocket = () => send(encodeButton(device.platform, button));
		// The simulator's own Home and app switcher buttons do nothing on a Face
		// ID iPhone; the daemon makes the home indicator swipe instead.
		if (device.platform === "ios" && (button === "recents" || button === "home")) {
			void pressSessionDeviceButton(config, sessionID, button).then((handled) => { if (!handled) viaSocket(); }).catch(viaSocket);
			return;
		}
		viaSocket();
	};
	// The phone's own navigation order (Back, Home, Recents), under the thumb
	// below the screen rather than in the bar at the top.
	// iOS has no Back, and its app switcher is a row of cards, not Android's square.
	const buttons: { button: DeviceButton; icon: "chevron-left" | "home" | "square" | "gallery-horizontal"; label: string }[] = [
		...(device.platform === "android" ? [{ button: "back" as const, icon: "chevron-left" as const, label: "Back" }] : []),
		{ button: "home", icon: "home", label: "Home" },
		{ button: "recents", icon: device.platform === "ios" ? "gallery-horizontal" : "square", label: "Recents" },
	];

	const leave = () => { haptics.tap(); if (router.canGoBack()) router.back(); else router.navigate("/"); };
	const release = (shutdown: boolean) => void closeSessionDevice(config, sessionID, { shutdown }).then(onReleased).catch(() => {});
	// What the screen does not show by itself lives in one menu, named in words:
	// giving the device back, and powering it off.
	const menuActions: MenuAction[] = [
		{ id: "release", title: "Release device" },
		{ id: "shutdown", title: "Shut down device", attributes: { destructive: true } },
	];
	const onMenu = (event: NativeActionEvent) => {
		const id = event.nativeEvent.event;
		if (id === "release") release(false);
		else if (id === "shutdown") release(true);
	};
	const menuButton = <View style={styles.pillIcon}><Feather name="more-horizontal" size={iconSize.md} color={t.textPrimary} /></View>;
	const streamMode = device.platform === "android" ? "h264" : avccFailed ? "mjpeg" : "avcc";
	useEffect(() => {
		if (streamMode === "avcc" && streaming) avccPlayed.current = true;
		if (streamMode !== "avcc" || off || streaming || avccPlayed.current) return;
		const fallback = setTimeout(() => setAvccFailed(true), AVCC_START_MS);
		return () => clearTimeout(fallback);
	}, [off, streamMode, streaming]);
	const [starting, setStarting] = useState(false);
	const start = () => {
		setStarting(true);
		void openSessionDevice(config, sessionID, { deviceId: device.id, platform: device.platform }).catch(() => {}).finally(() => setStarting(false));
	};
	const subtitle = [device.version, off ? "Off" : live ? null : "Connecting…"].filter(Boolean).join(" · ");

	return <View style={styles.screen}>
		<StatusBar hidden animated />
		<View style={[styles.stage, { paddingTop: insets.top + PILL_HEIGHT + space.lg, paddingBottom: Math.max(insets.bottom, space.md) + CAPSULE_HEIGHT + space.lg }]}>
			{off ? <EmptyState icon="smartphone" title={`${device.name} is off`} action={<Button title={starting ? "Starting…" : "Start"} disabled={starting} onPress={start} />} /> : null}
			{/* Hidden rather than unmounted while off: React Native reuses the native
			    stream view, and a reused one connected but drew nothing. An empty url
			    stops the stream until the device runs again. */}
			<View
				accessibilityLabel={`${device.name} screen`}
				onLayout={(event: LayoutChangeEvent) => { size.current = event.nativeEvent.layout; }}
				onMoveShouldSetResponder={() => true}
				onResponderGrant={(event) => touch("begin", event)}
				onResponderMove={(event) => touch("move", event)}
				onResponderRelease={(event) => touch("end", event)}
				onResponderTerminate={(event) => touch("end", event)}
				onStartShouldSetResponder={() => true}
				style={[styles.device, { aspectRatio: aspect, borderRadius: device.platform === "ios" ? 40 : 24 }, off && styles.hidden]}
			>
				{NativeStream ? <NativeStream
					headers={device.platform === "android" ? deviceSocketHeaders(config) : authHeaders(config)}
					mode={streamMode}
					onSize={(event) => { const { width, height } = event.nativeEvent; if (width && height) setAspect(width / height); }}
					onState={(event) => setStreaming(event.nativeEvent.state === "streaming")}
					onStats={(event) => reportStream(event.nativeEvent)}
					pointerEvents="none"
					style={StyleSheet.absoluteFill}
					url={off ? "" : streamMode === "h264" ? deviceVideoURL(config, sessionID, device.id) : streamMode === "avcc" ? deviceAvccURL(config, sessionID, device.id) : deviceMjpegURL(config, sessionID, device.id)}
				/> : null}
				{NativeStream ? (streaming ? null : <ActivityIndicator color={t.accent} />) : layers.uris[0] === null && layers.uris[1] === null ? <ActivityIndicator color={t.accent} /> : null}
				<DeviceTouchRipple point={ripple} />
				{([0, 1] as const).map((layer) => {
					const uri = layers.uris[layer];
					return uri ? <Image
						key={layer}
						fadeDuration={0}
						onLoad={(event) => {
							const { width, height } = event.nativeEvent.source;
							if (width && height) setAspect(width / height);
							setLayers((current) => frameLoaded(current, layer));
						}}
						source={{ uri }}
						style={[StyleSheet.absoluteFill, { zIndex: layers.front === layer ? 1 : 0 }]}
					/> : null;
				})}
			</View>
		</View>
		<View style={[styles.pill, { top: insets.top + space.sm }]}>
			<Pressable accessibilityRole="button" accessibilityLabel="Done" hitSlop={6} onPress={leave} style={({ pressed }) => [styles.done, pressed && styles.dimmed]}>
				<Text style={styles.doneText}>Done</Text>
			</Pressable>
			<View style={styles.titleBlock}>
				<Text numberOfLines={1} style={styles.title}>{device.name}</Text>
				{subtitle ? <Text numberOfLines={1} style={styles.subtitle}>{subtitle}</Text> : null}
			</View>
			<MenuView ref={menu} actions={menuActions} colorScheme={scheme} title={device.name} onPressAction={onMenu} testID="device-menu">
				{Platform.OS === "android" ? (
					<Pressable accessibilityRole="button" accessibilityLabel="Device options" hitSlop={6} onPress={() => menu.current?.show()}>{menuButton}</Pressable>
				) : (
					<View accessible accessibilityRole="button" accessibilityLabel="Device options">{menuButton}</View>
				)}
			</MenuView>
		</View>
		<View style={[styles.capsule, { bottom: Math.max(insets.bottom, space.md) }]}>
			{buttons.map(({ button, icon, label }, index) => <Pressable key={button} accessibilityRole="button" accessibilityLabel={label} disabled={!live} onPress={() => press(button)} style={({ pressed }) => [styles.capsuleButton, index > 0 && styles.capsuleDivider, (!live || pressed) && styles.dimmed]}>
				<Feather name={icon} size={iconSize.sm} color={t.textPrimary} />
				<Text style={styles.capsuleLabel}>{label}</Text>
			</Pressable>)}
		</View>
	</View>;
}

const PILL_HEIGHT = 52;
const CAPSULE_HEIGHT = 48;

const makeStyles = (t: Theme) => StyleSheet.create({
	hidden: { display: "none" },
	screen: { flex: 1, backgroundColor: t.bgBase },
	center: { flex: 1, alignItems: "center", justifyContent: "center", gap: space.md, paddingHorizontal: space.xxxl, backgroundColor: t.bgBase },
	dimmed: { opacity: 0.5 },
	stage: { flex: 1, alignItems: "center", justifyContent: "center", paddingHorizontal: space.md },
	device: { maxWidth: "100%", maxHeight: "100%", height: "100%", borderCurve: "continuous", overflow: "hidden", backgroundColor: "#000" },
	// The two floating surfaces share one look: the app's elevated surface with
	// a hairline, rounded as a pill, so they read as controls over the device.
	pill: { position: "absolute", left: space.md, right: space.md, height: PILL_HEIGHT, flexDirection: "row", alignItems: "center", gap: space.sm, paddingHorizontal: space.xs, borderRadius: PILL_HEIGHT / 2, borderCurve: "continuous", backgroundColor: t.bgElevated, borderWidth: StyleSheet.hairlineWidth, borderColor: t.borderDefault },
	done: { height: 40, paddingHorizontal: space.lg, borderRadius: 20, alignItems: "center", justifyContent: "center", backgroundColor: t.bgSurface },
	doneText: { ...type.headline, color: t.textPrimary },
	titleBlock: { flex: 1, alignItems: "center" },
	title: { ...type.headline, color: t.textPrimary },
	subtitle: { ...type.caption1, color: t.textSecondary },
	pillIcon: { width: 40, height: 40, borderRadius: 20, alignItems: "center", justifyContent: "center", backgroundColor: t.bgSurface },
	capsule: { position: "absolute", alignSelf: "center", height: CAPSULE_HEIGHT, flexDirection: "row", alignItems: "center", borderRadius: CAPSULE_HEIGHT / 2, borderCurve: "continuous", overflow: "hidden", backgroundColor: t.bgElevated, borderWidth: StyleSheet.hairlineWidth, borderColor: t.borderDefault },
	capsuleButton: { height: CAPSULE_HEIGHT, flexDirection: "row", alignItems: "center", gap: space.xs, paddingHorizontal: space.lg },
	capsuleDivider: { borderLeftWidth: StyleSheet.hairlineWidth, borderLeftColor: t.borderDefault },
	capsuleLabel: { ...type.caption1, color: t.textPrimary },
});

export { RouteErrorBoundary as ErrorBoundary } from "../../lib/RouteErrorBoundary";
