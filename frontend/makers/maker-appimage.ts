import { copyFile, mkdtemp, rm, stat } from "node:fs/promises";
import { tmpdir } from "node:os";
import path from "node:path";
import { MakerBase, type MakerOptions } from "@electron-forge/maker-base";
import type { ForgePlatform } from "@electron-forge/shared-types";

// Electron Forge has no first-party AppImage maker, so we bridge to
// electron-builder's `buildForge`, exactly as makers/maker-nsis.ts does for the
// Windows NSIS installer. AppImage is the Linux fetch-and-run artifact for the
// `ao start` bootstrapper: a single self-contained executable the Go agent can
// download from releases/latest/download and run directly, with no system
// package manager. The deb/rpm makers stay for users who want a system package.
//
// `buildForge` speaks Forge's legacy v5 function API, which Forge 7's class-based
// maker loader cannot resolve, so this thin MakerBase subclass adapts it.

export type MakerAppImageConfig = {
	// electron-builder appId; required for a well-formed AppImage.
	appId?: string;
	// Display name for the app. Defaults to appName.
	productName?: string;
	// Path to the PNG icon used for the app and desktop entry.
	icon?: string;
	// URL schemes written to the AppImage desktop entry.
	protocols?: Array<{ name: string; schemes: string[] }>;
	// Any extra electron-builder `appImage` options, merged over our defaults.
	appImage?: Record<string, unknown>;
};

type PreparedLinuxIcon = { path: string; owned: boolean };

// app-builder-lib 26.15.3 installs a bare PNG file at its pixel size only. A
// directory whose sole PNG is icon.png goes through its icons tool instead and
// emits 16, 24, 32, 48, 64, 128, 256 and 512, which the AppImage copies to
// usr/share/icons/hicolor/<N>x<N>/apps/. So a single PNG is wrapped in a temp
// directory. A caller-supplied directory is passed through and never removed.
async function linuxIconDir(icon: string | undefined): Promise<PreparedLinuxIcon | undefined> {
	if (!icon) return undefined;
	const resolved = path.resolve(icon);
	if ((await stat(resolved)).isDirectory()) return { path: resolved, owned: false };
	const dir = await mkdtemp(path.join(tmpdir(), "ao-linux-icons-"));
	try {
		await copyFile(resolved, path.join(dir, "icon.png"));
	} catch (error) {
		await rm(dir, { recursive: true, force: true });
		throw error;
	}
	return { path: dir, owned: true };
}

export default class MakerAppImage extends MakerBase<MakerAppImageConfig> {
	name = "appimage";
	defaultPlatforms: ForgePlatform[] = ["linux"];

	isSupportedOnCurrentPlatform(): boolean {
		return true;
	}

	async make({ dir, targetArch, appName }: MakerOptions): Promise<string[]> {
		const { buildForge } = await import("app-builder-lib");
		const cfg = this.config ?? {};
		// Mirror buildForge's own output layout (<dir>/../make) so artifacts land
		// where Forge's publisher expects them.
		const output = path.join(path.dirname(path.resolve(dir)), "make");
		const icon = await linuxIconDir(cfg.icon);
		try {
			return await buildForge(
				{ dir },
				{
					linux: [`appImage:${targetArch}`],
					config: {
						appId: cfg.appId,
						productName: cfg.productName ?? appName,
						protocols: cfg.protocols,
						directories: { output },
						// A missing `toolsets.appimage` means "0.0.0" to app-builder-lib, which
						// builds with the AppImageKit runtime that dlopens libfuse.so.2 at
						// startup. Distros shipping only fuse3 (Arch, and any install without
						// the fuse2 compat package) cannot launch that artifact at all — it
						// exits 1 with "dlopen(): error loading libfuse.so.2" before Electron
						// starts, which also breaks `ao start`, since that downloads and runs
						// this same AppImage (#4006). Pinning a non-legacy toolset moves the
						// published artifact onto a runtime with no libfuse2 dependency.
						// `1.0.3` is the same Runtime 20251108 as `1.0.2`, re-released with an
						// upstream fix, so it is the only sensible choice of the two.
						toolsets: { appimage: "1.0.3" },
						// Forge owns publishing (the workflow uploads via `gh release`).
						// `null` stops electron-builder from inferring a GitHub publish
						// target from package.json `repository` and trying to upload,
						// which fails in CI with no GH_TOKEN set.
						publish: null,
						linux: {
							...(icon ? { icon: icon.path } : {}),
						},
						appImage: {
							...cfg.appImage,
						},
					},
				},
			);
		} finally {
			// Remove only the wrapper this call created, once buildForge has settled.
			if (icon?.owned) await rm(icon.path, { recursive: true, force: true });
		}
	}
}

export { MakerAppImage };
