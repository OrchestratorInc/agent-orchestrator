import Constants from "expo-constants";
import { useSegments } from "expo-router";
import * as Updates from "expo-updates";
import { useEffect, useRef } from "react";
import { AppState, type AppStateStatus } from "react-native";
import { useCloud } from "./cloud/CloudProvider";
import { captureMobileException, setMobileSentryContext } from "./sentry";
import { useApp } from "./store";
import { updateIdOf } from "./telemetry/context";
import {
	initMobileTelemetry,
	loadMobileOptOut,
	markLaunchBackgrounded,
	markLaunchUsable,
	markLaunchVisible,
	setLaunchHasMachine,
	mobileTelemetry,
	syncDesktopTelemetryIdentity,
	telemetryActiveStorage,
} from "./telemetry/runtime";
import { surfaceForSegments } from "./telemetry/surfaces";
import { createStallProbe } from "./telemetry/stallProbe";
import { uiUsage } from "./telemetry/uiUsageRegistry";

// How often the stall probe asks to run; a run 100 ms late or more is a stall.
const STALL_PROBE_MS = 500;

// RN's global JS error hook. Typed locally so we don't depend on RN internals.
type ErrorUtilsLike = {
	getGlobalHandler?: () => ((error: unknown, isFatal?: boolean) => void) | undefined;
	setGlobalHandler?: (handler: (error: unknown, isFatal?: boolean) => void) => void;
};

// Headless. Mounted once in the app shell beside PushManager. Initialises the
// PostHog client and emits the daily-active heartbeat on launch and on each
// return to the foreground (which catches a UTC-day rollover while the app was
// backgrounded). The reservation caps it to once per UTC day regardless.
//
// It also feeds UI usage (the surface on screen, foreground time, links) and
// launch timing (first frame, first frame with a machine's live data).
export function TelemetryManager() {
	const { hostStates, cloud } = useApp();
	const { sessionsLoaded: cloudSessionsLoaded } = useCloud();
	const surface = surfaceForSegments(useSegments());
	// Live data from a paired machine, or Orchestrator Cloud's first list.
	const usable = hostStates.some(({ connection, lastSyncAt }) => connection === "open" && lastSyncAt > 0)
		|| (Boolean(cloud) && cloudSessionsLoaded);
	const hasMachine = hostStates.length > 0 || Boolean(cloud);
	// The newest refresh any paired machine has answered, and whether one is paired.
	const lastSync = hostStates.reduce((latest, { lastSyncAt }) => Math.max(latest, lastSyncAt), 0);
	const pairedRef = useRef(false);
	pairedRef.current = hostStates.length > 0;
	const connected = hostStates.flatMap(({ config, connection }) => (config && connection === "open" ? [config] : []));
	// Re-sync when a desktop connects, its endpoint or credential changes, or the
	// app returns to the foreground; a poll refresh alone does not re-run it.
	const connectedKey = JSON.stringify(connected.map(({ hostId, host, httpPort, secure, password }) => [hostId, host, httpPort, secure, password]));
	const connectedRef = useRef(connected);
	connectedRef.current = connected;

	useEffect(() => {
		initMobileTelemetry();
		const version = Constants.nativeAppVersion ?? Constants.expoConfig?.version ?? "unknown";
		const build = Constants.nativeBuildVersion ?? "unknown";
		setMobileSentryContext({
			release: `${version}@${build}`,
			channel: Updates.channel ?? undefined,
			updateId: updateIdOf(Updates.isEnabled ? { id: Updates.updateId, embedded: Updates.isEmbeddedLaunch, channel: Updates.channel } : undefined),
		});
		// The persisted opt-out is restored before the first heartbeat can fire.
		void loadMobileOptOut().then(() => mobileTelemetry()?.active(telemetryActiveStorage));
		// The telemetry preference and authenticated desktop identity govern
		// Sentry through the same runtime callbacks.
		if (mobileTelemetry()) {
			// Forward uncaught JS errors, preserving RN's own handler.
			const errorUtils = (globalThis as unknown as { ErrorUtils?: ErrorUtilsLike }).ErrorUtils;
			const prev = errorUtils?.getGlobalHandler?.();
			errorUtils?.setGlobalHandler?.((error, isFatal) => {
				captureMobileException(error, { category: "native_crash", unhandled: true });
				prev?.(error, isFatal);
			});
		}

		const frame = requestAnimationFrame(() => markLaunchVisible());
		// Only while in front: a suspended app's first timer back is not a stall.
		const stalls = createStallProbe({
			intervalMs: STALL_PROBE_MS,
			now: Date.now,
			setTimer: (fn, ms) => setTimeout(fn, ms),
			clearTimer: (handle) => clearTimeout(handle),
			onLate: (lateMs) => uiUsage()?.stalled(lateMs),
		});
		if (uiUsage()) stalls.start();

		const onChange = (state: AppStateStatus) => {
			if (state === "active") {
				void mobileTelemetry()?.active(telemetryActiveStorage);
				uiUsage()?.setForeground(true);
				// Times how long a paired machine's data takes to be fresh again.
				if (pairedRef.current) uiUsage()?.resumed();
				if (uiUsage()) stalls.start();
			} else {
				stalls.stop();
			}
			if (state === "background") {
				// iOS passes through `inactive` for the app switcher and Control
				// Center too; only a real background ends the stint.
				uiUsage()?.setForeground(false);
				markLaunchBackgrounded();
			}
		};
		const sub = AppState.addEventListener("change", onChange);
		return () => {
			cancelAnimationFrame(frame);
			stalls.stop();
			sub.remove();
		};
	}, []);

	// After the mount effect above, which creates the tracker.
	useEffect(() => {
		uiUsage()?.setRoute(surface);
	}, [surface]);

	useEffect(() => {
		setLaunchHasMachine(hasMachine);
	}, [hasMachine]);

	useEffect(() => {
		if (lastSync > 0) uiUsage()?.dataRefreshed(lastSync);
	}, [lastSync]);

	useEffect(() => {
		if (!usable) return;
		const frame = requestAnimationFrame(() => markLaunchUsable());
		return () => cancelAnimationFrame(frame);
	}, [usable]);

	useEffect(() => {
		void syncDesktopTelemetryIdentity(connectedRef.current).catch(() => {});
		const onChange = (state: AppStateStatus) => {
			if (state === "active") void syncDesktopTelemetryIdentity(connectedRef.current).catch(() => {});
		};
		const sub = AppState.addEventListener("change", onChange);
		return () => sub.remove();
	}, [connectedKey]);

	return null;
}
