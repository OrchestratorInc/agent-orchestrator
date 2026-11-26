import { sentryVitePlugin } from "@sentry/vite-plugin";
import type { PluginOption } from "vite";

// Release builds (AO_PUBLISHED_BUILD=1, set by build-artifacts.yml) emit hidden
// source maps and stamp each bundle with a Sentry debug ID, so desktop error
// frames map back to source. The maps are uploaded, never shipped: forge.config.ts
// leaves every .map out of the package. With SENTRY_AUTH_TOKEN the plugin uploads
// them itself; without it the release conductor uploads the sentry-sourcemaps-*
// run artifact. Other builds get neither maps nor debug IDs.
export function sentrySourceMaps(env: NodeJS.ProcessEnv = process.env): { sourcemap: "hidden" | false; plugins: PluginOption[] } {
	if (env.AO_PUBLISHED_BUILD !== "1") return { sourcemap: false, plugins: [] };
	const authToken = env.SENTRY_AUTH_TOKEN || undefined;
	return {
		sourcemap: "hidden",
		plugins: [
			sentryVitePlugin({
				org: "untrivial-x0",
				project: "ao-electron",
				authToken,
				telemetry: false,
				// Electron main sets the release; only debug IDs are needed here.
				release: { inject: false, create: false },
				sourcemaps: { disable: authToken ? false : "disable-upload" },
			}),
		],
	};
}
