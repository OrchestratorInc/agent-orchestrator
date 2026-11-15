import { sentryVitePlugin } from "@sentry/vite-plugin";
import type { PluginOption } from "vite";

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
				release: { inject: false, create: false },
				sourcemaps: { disable: authToken ? false : "disable-upload" },
			}),
		],
	};
}
