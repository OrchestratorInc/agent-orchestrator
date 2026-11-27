// The context that rides on every mobile event, so any metric can be split by
// where it came from. Pure: it takes the platform, device, and version as
// inputs rather than importing react-native, so the mapping is unit-testable.
// A thin runtime wrapper (telemetry.ts) feeds it Platform.OS / __DEV__ / the
// app version.

export type TelemetryContextInput = {
	// react-native Platform.OS: "ios" | "android" | "web" | others.
	platformOS: string;
	// expo-device Device.isDevice: false on a simulator/emulator.
	isPhysicalDevice: boolean;
	// __DEV__ at runtime.
	dev: boolean;
	appVersion: string;
	/** expo-updates' running update. Absent where updates are off (dev). */
	update?: { id: string | null; embedded: boolean; channel: string | null };
};

export type TelemetryContext = {
	// The bifurcation the ask named: native app vs the mobile-web build.
	client: "mobile" | "mobile-web";
	platform: "ios" | "android" | "web" | "other";
	build_mode: "dev" | "device" | "simulator";
	app_version: string;
	/**
	 * The OTA update the JS is running, or `embedded` for the bundle shipped in
	 * the binary. Many fixes ship by OTA under the same app_version, so this is
	 * what tells two bundles of one store build apart. The SDK's own $app_build
	 * and $os_version already ride on every event.
	 */
	update_id?: string;
	update_channel?: "production" | "preview" | "development" | "other";
	telemetry_schema_version: number;
};

// Bump when the shape of any event payload changes in a way a query relies on.
// 3 marks the mobile.app.* names; 2 used ao.v2.mobile_app.*.
export const MOBILE_TELEMETRY_SCHEMA_VERSION = 3;

function platformOf(os: string): TelemetryContext["platform"] {
	if (os === "ios" || os === "android" || os === "web") return os;
	return "other";
}

const UPDATE_ID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
const CHANNELS = new Set(["production", "preview", "development"]);

/** The running update as a fixed-shape id: a UUID or `embedded`. Never free text. */
export function updateIdOf(update: TelemetryContextInput["update"]): string | undefined {
	if (!update) return undefined;
	if (update.embedded || !update.id) return "embedded";
	return UPDATE_ID.test(update.id) ? update.id.toLowerCase() : undefined;
}

export function buildMobileContext(input: TelemetryContextInput): TelemetryContext {
	const version = input.appVersion.trim() || "unknown";
	const updateId = updateIdOf(input.update);
	const channel = input.update?.channel;
	return {
		// The web build target (app.json defines one) reports as mobile-web so it
		// never inflates native install counts.
		client: input.platformOS === "web" ? "mobile-web" : "mobile",
		platform: platformOf(input.platformOS),
		build_mode: input.dev ? "dev" : input.isPhysicalDevice ? "device" : "simulator",
		app_version: version,
		...(updateId ? { update_id: updateId } : {}),
		...(input.update ? { update_channel: channel && CHANNELS.has(channel) ? channel as "production" | "preview" | "development" : "other" } : {}),
		telemetry_schema_version: MOBILE_TELEMETRY_SCHEMA_VERSION,
	};
}

const DAEMON_VERSION = /^\d+\.\d+\.\d+(?:[.-][A-Za-z0-9.]+)?$/;

/** A paired daemon's release, kept only in its version-number shape. */
export function safeDaemonVersion(value: unknown): string | undefined {
	return typeof value === "string" && value.length <= 64 && DAEMON_VERSION.test(value) ? value : undefined;
}
