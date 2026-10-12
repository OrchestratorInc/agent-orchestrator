import { defineConfig } from "vite";

// better-sqlite3 is a native module rebuilt for Electron by Forge. Keep it out
// of Vite's bundle so the packaged app loads the rebuilt binary from
// node_modules (the auto-unpack plugin moves that binary outside app.asar).
export default defineConfig({
	define: { "import.meta.env.AO_BUILD_VARIANT": JSON.stringify(process.env.AO_BUILD_VARIANT ?? "") },
	build: {
		rollupOptions: {
			external: ["better-sqlite3"],
		},
	},
});
