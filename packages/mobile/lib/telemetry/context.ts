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
};

export type TelemetryContext = {
	// The bifurcation the ask named: native app vs the mobile-web build.
	client: "mobile" | "mobile-web";
	platform: "ios" | "android" | "web" | "other";
	build_mode: "dev" | "device" | "simulator";
	app_version: string;
	telemetry_schema_version: number;
};

// Bump when the shape of any event payload changes in a way a query relies on.
// 3 marks the mobile.app.* names; 2 used ao.v2.mobile_app.*.
export const MOBILE_TELEMETRY_SCHEMA_VERSION = 3;

function platformOf(os: string): TelemetryContext["platform"] {
	if (os === "ios" || os === "android" || os === "web") return os;
	return "other";
}

export function buildMobileContext(input: TelemetryContextInput): TelemetryContext {
	const version = input.appVersion.trim() || "unknown";
	return {
		// The web build target (app.json defines one) reports as mobile-web so it
		// never inflates native install counts.
		client: input.platformOS === "web" ? "mobile-web" : "mobile",
		platform: platformOf(input.platformOS),
		build_mode: input.dev ? "dev" : input.isPhysicalDevice ? "device" : "simulator",
		app_version: version,
		telemetry_schema_version: MOBILE_TELEMETRY_SCHEMA_VERSION,
	};
}
