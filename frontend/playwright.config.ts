import { defineConfig } from "@playwright/test";

const port = Number(process.env.AO_E2E_PORT ?? 5173);

export default defineConfig({
	testDir: "e2e",
	retries: process.env.CI ? 1 : 0,
	reporter: process.env.CI ? [["list"], ["github"]] : "list",
	use: {
		baseURL: `http://127.0.0.1:${port}`,
	},
	webServer: {
		command: process.env.AO_PERF_PRODUCTION === "1"
			? "node scripts/performance-web.mjs"
			: `npm run dev:web -- --port ${port} --host 127.0.0.1`,
		port,
		reuseExistingServer: !process.env.CI,
	},
});
