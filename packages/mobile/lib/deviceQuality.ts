import { NetworkStateType, addNetworkStateListener, getNetworkStateAsync, type NetworkState } from "expo-network";
import { useCallback, useEffect, useRef, useState } from "react";
import { setSessionDeviceQuality, type DeviceStreamQuality } from "./api";
import type { ServerConfig } from "./config";
import type { EndpointKind } from "./endpoints";

// The stream's quality is the app's call, not the user's: it starts from what
// the connection is and then follows how the stream is actually arriving.

const LEVELS: readonly DeviceStreamQuality[] = ["low", "balanced", "high"];
const rank = (quality: DeviceStreamQuality) => LEVELS.indexOf(quality);

/**
 * The most the connection is trusted with before anything is measured: all of
 * it over Wi-Fi or a wired link to the machine (on the LAN or over
 * Tailscale), a middle setting over mobile data or through a tunnel or relay.
 */
export function deviceQualityCeiling(network: NetworkStateType | undefined, endpoint: EndpointKind | undefined): DeviceStreamQuality {
	if (network === NetworkStateType.CELLULAR) return "balanced";
	if (endpoint === "tunnel" || endpoint === "relay") return "balanced";
	return "high";
}

/** What the native stream view reports every two seconds. */
export type DeviceStreamStats = {
	/** "avcc" (a simulator's H.264 over HTTP) is judged like MJPEG: it has no capture times, but the hub sends its next frame as soon as the last is through. */
	mode: "h264" | "avcc" | "mjpeg";
	kbps: number;
	frames: number;
	/** MJPEG and avcc: the share of the time the link spent receiving a frame. */
	busy?: number;
	/** H.264: how much later than usual frames in motion arrived after capture. */
	lagMs?: number;
	/** H.264: the same for the latest frame of all (a still screen's refreshes run ~0.5 s late). */
	maxLagMs?: number;
};

export type QualityState = {
	quality: DeviceStreamQuality;
	ceiling: DeviceStreamQuality;
	/** Struggling reports in a row. */
	strikes: number;
	/** When the stream started arriving comfortably, or null. */
	healthySince: number | null;
	/** How long it must stay comfortable before trying a step up. */
	waitMs: number;
	lastUpAt: number;
};

const STRIKES_TO_DROP = 2;
const FIRST_WAIT_MS = 20_000;
const MAX_WAIT_MS = 5 * 60_000;
/** A drop this soon after a step up means the step up did not fit. */
const FAILED_UP_MS = 20_000;

export function initialQualityState(ceiling: DeviceStreamQuality): QualityState {
	return { quality: ceiling, ceiling, strikes: 0, healthySince: null, waitMs: FIRST_WAIT_MS, lastUpAt: Number.NEGATIVE_INFINITY };
}

/** The link cannot carry the stream: frames queue (H.264) or arrive back to back (MJPEG). */
function struggling(stats: DeviceStreamStats): boolean {
	if (stats.mode === "h264") return (stats.lagMs ?? 0) > 500 || (stats.maxLagMs ?? 0) > 1500;
	return stats.frames > 0 && (stats.busy ?? 0) > 0.85;
}

/** Plenty of room: a step up has a chance. A still screen (no frames) says nothing. */
function comfortable(stats: DeviceStreamStats): boolean {
	if (stats.frames === 0) return false;
	if (stats.mode === "h264") return stats.lagMs !== undefined && stats.lagMs < 150;
	return (stats.busy ?? 0) < 0.5;
}

/**
 * One report's effect. Two struggling reports in a row step the quality down;
 * a stretch of comfortable ones steps it back up toward the ceiling, waiting
 * twice as long after each step up that did not hold.
 */
export function nextQuality(state: QualityState, stats: DeviceStreamStats, now: number): QualityState {
	if (struggling(stats)) {
		const strikes = state.strikes + 1;
		if (strikes < STRIKES_TO_DROP || state.quality === "low") return { ...state, strikes, healthySince: null };
		const failedUp = now - state.lastUpAt < FAILED_UP_MS;
		return {
			...state,
			quality: LEVELS[rank(state.quality) - 1],
			strikes: 0,
			healthySince: null,
			waitMs: failedUp ? Math.min(state.waitMs * 2, MAX_WAIT_MS) : state.waitMs,
		};
	}
	if (!comfortable(stats)) return { ...state, strikes: 0 };
	const healthySince = state.healthySince ?? now;
	if (rank(state.quality) < rank(state.ceiling) && now - healthySince >= state.waitMs) {
		return { ...state, quality: LEVELS[rank(state.quality) + 1], strikes: 0, healthySince: now, lastUpAt: now };
	}
	return { ...state, strikes: 0, healthySince };
}

/** A new connection: start again from what it allows. */
export function withCeiling(state: QualityState, ceiling: DeviceStreamQuality): QualityState {
	if (ceiling === state.ceiling) return state;
	return initialQualityState(ceiling);
}

/** The phone's current connection type, kept up to date. */
function useNetworkType(): NetworkStateType | undefined {
	const [type, setType] = useState<NetworkStateType>();
	useEffect(() => {
		const apply = (state: NetworkState) => setType(state.type);
		void getNetworkStateAsync().then(apply).catch(() => {});
		const sub = addNetworkStateListener(apply);
		return () => sub.remove();
	}, []);
	return type;
}

/**
 * Picks the device stream's quality for this phone and keeps the daemon told.
 * Feed it the stream view's reports with the returned callback. The setting
 * belongs to the device, so it goes back to full quality when the screen closes
 * (the daemon does the same when the lease ends).
 */
export function useDeviceQuality(config: ServerConfig, sessionID: string): { quality: DeviceStreamQuality; report: (stats: DeviceStreamStats) => void } {
	const network = useNetworkType();
	const ceiling = deviceQualityCeiling(network, config.endpointKind);
	const state = useRef(initialQualityState(ceiling));
	const [quality, setQuality] = useState<DeviceStreamQuality>(ceiling);
	useEffect(() => {
		state.current = withCeiling(state.current, ceiling);
		setQuality(state.current.quality);
	}, [ceiling]);
	const report = useCallback((stats: DeviceStreamStats) => {
		state.current = nextQuality(state.current, stats, Date.now());
		setQuality(state.current.quality);
	}, []);
	useEffect(() => {
		// Nothing to change until the first connection type is known, and high
		// is what a fresh stream already is.
		if (network === undefined && quality === "high") return;
		void setSessionDeviceQuality(config, sessionID, quality).catch(() => {});
	}, [config, network, quality, sessionID]);
	useEffect(() => () => { void setSessionDeviceQuality(config, sessionID, "high").catch(() => {}); }, [config, sessionID]);
	return { quality, report };
}
