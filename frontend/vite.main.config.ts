import { defineConfig } from "vite";
import { sentrySourceMaps } from "./vite.sentry";

const sentry = sentrySourceMaps();

export default defineConfig({
	define: {
		"process.env.AO_PUBLISHED_BUILD": JSON.stringify(process.env.AO_PUBLISHED_BUILD === "1" ? "1" : ""),
	},
	plugins: sentry.plugins,
	build: {
		sourcemap: sentry.sourcemap,
		rollupOptions: {
			external: ["better-sqlite3"],
		},
	},
});
