import { describe, expect, it, vi } from "vitest";

vi.mock("expo-network", () => ({
	NetworkStateType: { UNKNOWN: "UNKNOWN", WIFI: "WIFI", CELLULAR: "CELLULAR", ETHERNET: "ETHERNET", NONE: "NONE" },
	addNetworkStateListener: vi.fn(),
	getNetworkStateAsync: vi.fn(),
}));
vi.mock("./api", () => ({ setSessionDeviceQuality: vi.fn() }));

import { deviceQualityCeiling, initialQualityState, nextQuality, withCeiling, type DeviceStreamStats, type QualityState } from "./deviceQuality";

const busyMjpeg: DeviceStreamStats = { mode: "mjpeg", kbps: 9000, frames: 12, busy: 0.95 };
const easyMjpeg: DeviceStreamStats = { mode: "mjpeg", kbps: 20000, frames: 40, busy: 0.2 };
const stillMjpeg: DeviceStreamStats = { mode: "mjpeg", kbps: 0, frames: 0, busy: 0 };
const laggingH264: DeviceStreamStats = { mode: "h264", kbps: 4000, frames: 20, lagMs: 900 };
const easyH264: DeviceStreamStats = { mode: "h264", kbps: 4000, frames: 30, lagMs: 20, maxLagMs: 60 };
const stillH264: DeviceStreamStats = { mode: "h264", kbps: 200, frames: 4, maxLagMs: 480 };
const backedUpH264: DeviceStreamStats = { mode: "h264", kbps: 900, frames: 5, maxLagMs: 2400 };

function feed(state: QualityState, stats: DeviceStreamStats, from: number, reports: number): [QualityState, number] {
	let now = from;
	for (let i = 0; i < reports; i++) {
		now += 2_000;
		state = nextQuality(state, stats, now);
	}
	return [state, now];
}

describe("deviceQualityCeiling", () => {
	it("trusts Wi-Fi or a wired link to the machine with everything, and an unknown type too", () => {
		expect(deviceQualityCeiling("WIFI" as never, "lan")).toBe("high");
		expect(deviceQualityCeiling("ETHERNET" as never, "tailscale")).toBe("high");
		expect(deviceQualityCeiling(undefined, undefined)).toBe("high");
	});

	it("starts lower over mobile data or through a tunnel or relay", () => {
		expect(deviceQualityCeiling("CELLULAR" as never, "lan")).toBe("balanced");
		expect(deviceQualityCeiling("WIFI" as never, "tunnel")).toBe("balanced");
		expect(deviceQualityCeiling("WIFI" as never, "relay")).toBe("balanced");
	});
});

describe("nextQuality", () => {
	it("steps down after two struggling reports, not one", () => {
		let state = initialQualityState("high");
		state = nextQuality(state, busyMjpeg, 2_000);
		expect(state.quality).toBe("high");
		state = nextQuality(state, busyMjpeg, 4_000);
		expect(state.quality).toBe("balanced");
		[state] = feed(state, laggingH264, 4_000, 2);
		expect(state.quality).toBe("low");
		[state] = feed(state, laggingH264, 8_000, 4);
		expect(state.quality).toBe("low");
	});

	it("a still screen neither drops nor raises the quality", () => {
		let state: QualityState = { ...initialQualityState("high"), quality: "balanced" };
		const [after] = feed(state, stillMjpeg, 0, 60);
		expect(after.quality).toBe("balanced");
		state = nextQuality(state, busyMjpeg, 1);
		state = nextQuality(state, stillMjpeg, 2);
		state = nextQuality(state, busyMjpeg, 3);
		expect(state.quality).toBe("balanced");
	});

	it("climbs back to the ceiling after a comfortable stretch, never past it", () => {
		let [state, now] = feed(initialQualityState("high"), busyMjpeg, 0, 4);
		expect(state.quality).toBe("low");
		[state, now] = feed(state, easyH264, now, 11);
		expect(state.quality).toBe("balanced");
		[state, now] = feed(state, easyMjpeg, now, 11);
		expect(state.quality).toBe("high");
		[state] = feed(state, easyMjpeg, now, 30);
		expect(state.quality).toBe("high");
	});

	it("waits twice as long after a step up that did not hold", () => {
		let [state, now] = feed(initialQualityState("high"), busyMjpeg, 0, 2);
		expect(state.waitMs).toBe(20_000);
		[state, now] = feed(state, easyMjpeg, now, 11); // back up to high
		expect(state.quality).toBe("high");
		[state, now] = feed(state, busyMjpeg, now, 2); // and straight down again
		expect(state.quality).toBe("balanced");
		expect(state.waitMs).toBe(40_000);
		[state, now] = feed(state, easyMjpeg, now, 11);
		expect(state.quality).toBe("balanced");
		[state] = feed(state, easyMjpeg, now, 10);
		expect(state.quality).toBe("high");
	});
});

describe("H.264 on a still screen", () => {
	it("is neither struggling nor comfortable: its refresh frames run late by design", () => {
		const [state] = feed({ ...initialQualityState("high"), quality: "balanced" }, stillH264, 0, 60);
		expect(state.quality).toBe("balanced");
	});

	it("steps down when even the sparse frames fall seconds behind", () => {
		const [state] = feed(initialQualityState("high"), backedUpH264, 0, 2);
		expect(state.quality).toBe("balanced");
	});
});

describe("a simulator's H.264 over HTTP", () => {
	it("is judged by how busy the link is, as MJPEG is", () => {
		const busyAvcc: DeviceStreamStats = { mode: "avcc", kbps: 4800, frames: 50, busy: 0.93 };
		const easyAvcc: DeviceStreamStats = { mode: "avcc", kbps: 4800, frames: 60, busy: 0.1 };
		let [state, now] = feed(initialQualityState("high"), busyAvcc, 0, 2);
		expect(state.quality).toBe("balanced");
		[state] = feed(state, easyAvcc, now, 11);
		expect(state.quality).toBe("high");
	});
});

describe("withCeiling", () => {
	it("starts again from what a new connection allows", () => {
		const [low] = feed(initialQualityState("high"), busyMjpeg, 0, 4);
		expect(withCeiling(low, "balanced").quality).toBe("balanced");
		expect(withCeiling(withCeiling(low, "balanced"), "high").quality).toBe("high");
		expect(withCeiling(low, "high")).toBe(low);
	});
});
