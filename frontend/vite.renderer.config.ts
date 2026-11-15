import { execFileSync } from "node:child_process";
import { defineConfig } from "vitest/config";
import type { Plugin } from "vite";
import { fileURLToPath, URL } from "node:url";
import { TanStackRouterVite } from "@tanstack/router-plugin/vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import { ARTIFACT_SNAPSHOT_SCHEME } from "./src/shared/artifact-snapshot";
import { DEFAULT_POSTHOG_HOST } from "./src/shared/posthog-config";
import { parseTeamsFlag } from "./src/shared/teams-flag";
import { sentrySourceMaps } from "./vite.sentry";

const TEAMS_FLAG = parseTeamsFlag(process.env.VITE_AO_TEAMS);

const POSTHOG_ORIGINS = (() => {
	const configured = process.env.VITE_AO_POSTHOG_HOST?.trim() || DEFAULT_POSTHOG_HOST;
	if (!configured) return [];
	let url: URL;
	try {
		url = new URL(configured);
	} catch {
		return [];
	}
	const origins = [url.origin];
	if (/\.posthog\.com$/i.test(url.hostname)) {
		const assetsHost = url.hostname.replace(/^([^.]+)\./, "$1-assets.");
		if (assetsHost !== url.hostname) origins.push(`${url.protocol}//${assetsHost}`);
	}
	return origins;
})();

function gitValue(args: string[], fallback: string): string {
	try {
		const value = execFileSync("git", args, {
			cwd: fileURLToPath(new URL("../", import.meta.url)),
			encoding: "utf8",
			stdio: ["ignore", "pipe", "ignore"],
		}).trim();
		return value || fallback;
	} catch {
		return fallback;
	}
}

function gitIsDirty(): boolean {
	try {
		return execFileSync("git", ["status", "--porcelain", "--untracked-files=all"], {
			cwd: fileURLToPath(new URL("../", import.meta.url)),
			encoding: "utf8",
			stdio: ["ignore", "pipe", "ignore"],
		}).trim().length > 0;
	} catch {
		return false;
	}
}

const DEV_BUILD_INFO = {
	branch: gitValue(["branch", "--show-current"], "main"),
	commit: gitValue(["rev-parse", "--short", "HEAD"], "unknown"),
	worktree: gitValue(["rev-parse", "--show-toplevel"], "unknown"),
	isDirty: gitIsDirty(),
};

const CLOUD_CP_ORIGINS = (() => {
	const origins = ["https://staging-api.aoagents.dev", "https://api.aoagents.dev"];
	const override = process.env.AO_CLOUD_CONTROL_PLANE_URL?.trim();
	if (!override) return origins;
	let url: URL;
	try {
		url = new URL(override);
	} catch {
		return origins;
	}
	const origin = `${url.protocol === "http:" ? "http:" : "https:"}//${url.host}`;
	if (!origins.includes(origin)) origins.push(origin);
	return origins;
})();

const CLOUD_CP_WS_ORIGINS = CLOUD_CP_ORIGINS.map((origin) => origin.replace(/^http/, "ws"));

export function contentSecurityPolicy(mode: "build" | "serve"): string {
	return [
		"default-src 'self'",
		mode === "serve" ? "script-src 'self' 'wasm-unsafe-eval' 'unsafe-inline'" : "script-src 'self' 'wasm-unsafe-eval'",
		"style-src 'self' 'unsafe-inline'",
		`img-src 'self' data: blob: http://127.0.0.1:* http://*.localhost:* https: ${ARTIFACT_SNAPSHOT_SCHEME}:`,
		["media-src 'self' blob: http://127.0.0.1:* http://*.localhost:*", ...CLOUD_CP_ORIGINS].join(" "),
		"font-src 'self' data:",
		[
			"connect-src",
			"'self'",
			"data:",
			"blob:",
			"http://127.0.0.1:*",
			"ws://127.0.0.1:*",
			"http://*.localhost:*",
			mode === "serve" ? "ws://localhost:*" : "",
			...POSTHOG_ORIGINS,
			...CLOUD_CP_WS_ORIGINS,
		]
			.filter(Boolean)
			.join(" "),
		"object-src 'none'",
		"base-uri 'self'",
		["frame-src 'self' http://127.0.0.1:* http://*.localhost:*", ...CLOUD_CP_ORIGINS].join(" "),
	].join("; ");
}

const injectCspMeta: Plugin = {
	name: "inject-csp-meta",
	apply: "build",
	transformIndexHtml() {
		return [
			{
				tag: "meta",
				attrs: { "http-equiv": "Content-Security-Policy", content: contentSecurityPolicy("build") },
				injectTo: "head-prepend",
			},
		];
	},
};

const productUiReactBoundary: Plugin = {
	name: "product-ui-react-boundary",
	enforce: "pre",
	async resolveId(source, importer) {
		if (!importer?.includes("/packages/product-ui/")) {
			return null;
		}
		const remap =
			source === "react" ||
			source.startsWith("react/") ||
			source === "react-dom" ||
			source.startsWith("react-dom/") ||
			source === "motion" ||
			source.startsWith("motion/");
		if (!remap) {
			return null;
		}
		return this.resolve(
			source,
			fileURLToPath(new URL("./src/renderer/main.tsx", import.meta.url)),
			{ skipSelf: true },
		);
	},
};

const sentry = sentrySourceMaps();

export default defineConfig({
	define: {
		"import.meta.env.VITE_AO_GIT_BRANCH": JSON.stringify(DEV_BUILD_INFO.branch),
		"import.meta.env.VITE_AO_GIT_COMMIT": JSON.stringify(DEV_BUILD_INFO.commit),
		"import.meta.env.VITE_AO_GIT_WORKTREE": JSON.stringify(DEV_BUILD_INFO.worktree),
		"import.meta.env.VITE_AO_GIT_DIRTY": JSON.stringify(String(DEV_BUILD_INFO.isDirty)),
		"import.meta.env.VITE_AO_TEAMS": JSON.stringify(TEAMS_FLAG),
	},
	resolve: {
		alias: {
			"@": fileURLToPath(new URL("./src/renderer", import.meta.url)),
			"@aoagents/product-ui": fileURLToPath(
				new URL("../packages/product-ui/src/index.ts", import.meta.url),
			),
			clsx: fileURLToPath(new URL("./node_modules/clsx", import.meta.url)),
			"tailwind-merge": fileURLToPath(
				new URL("./node_modules/tailwind-merge", import.meta.url),
			),
		},
	},
	build: {
		sourcemap: sentry.sourcemap,
		rollupOptions: {
			input: {
				index: fileURLToPath(new URL("./index.html", import.meta.url)),
				"artifact-snapshot": fileURLToPath(new URL("./artifact-snapshot.html", import.meta.url)),
			},
		},
	},
	server: {
		proxy: {
			"/api": {
				target: process.env.AO_DEV_API_TARGET ?? "http://127.0.0.1:3001",
				changeOrigin: false,
			},
			"/mux": {
				target: process.env.AO_DEV_API_TARGET ?? "http://127.0.0.1:3001",
				changeOrigin: false,
				ws: true,
			},
		},
	},
	plugins: [
		TanStackRouterVite({
			routesDirectory: "./src/renderer/routes",
			generatedRouteTree: "./src/renderer/routeTree.gen.ts",
			target: "react",
			autoCodeSplitting: true,
		}),
		productUiReactBoundary,
		react(),
		tailwindcss(),
		injectCspMeta,
		...sentry.plugins,
	],
	test: {
		environment: "jsdom",
		silent: "passed-only",
		testTimeout: 20_000,
		exclude: ["**/node_modules/**", "dist/**", "dist-electron/**", "e2e/**", "src/docs/**"],
		globals: true,
		setupFiles: "./src/renderer/test/setup.ts",
	},
});
