// Production renderer fixtures for opt-in benchmarks, without packaging Electron.
// AO_PERF_BUILD_DIR can retain each revision's bundle for alternating A/B runs.
import { mkdtemp } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { build, loadConfigFromFile, mergeConfig, preview } from "vite";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");
process.chdir(root);
process.env.VITE_NO_ELECTRON = "1";
const loaded = await loadConfigFromFile(
	{ command: "build", mode: "production" },
	join(root, "vite.renderer.config.ts"),
);
if (!loaded) throw new Error("Renderer configuration did not load");
const outDir = process.env.AO_PERF_BUILD_DIR
	? resolve(process.env.AO_PERF_BUILD_DIR)
	: await mkdtemp(join(tmpdir(), "ao-renderer-performance-"));
const config = mergeConfig(loaded.config, {
	configFile: false,
	root,
	build: {
		outDir,
		rollupOptions: { input: join(root, process.env.AO_PERF_APP === "1" ? "index.html" : "e2e/performance/harness.html") },
	},
});
await build(config);
const server = await preview({
	...config,
	preview: { host: "127.0.0.1", port: Number(process.env.AO_E2E_PORT ?? 5173), strictPort: true },
});
server.printUrls();
