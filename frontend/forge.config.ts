import type { ForgeConfig } from "@electron-forge/shared-types";
import { AutoUnpackNativesPlugin } from "@electron-forge/plugin-auto-unpack-natives";
import { VitePlugin } from "@electron-forge/plugin-vite";
import { FusesPlugin } from "@electron-forge/plugin-fuses";
import { FuseV1Options, FuseVersion } from "@electron/fuses";
import { rebuild } from "@electron/rebuild";
import electronPackage from "electron/package.json";
import appPackage from "./package.json";
import MakerNSIS from "./makers/maker-nsis";
import MakerDMG, { isSigningConfigured, sealDmg, verifyDmg, verifyMacArtifact } from "./makers/maker-dmg";
import { machoHasX86_64Slice } from "./makers/macho-archs";
import MakerAppImage from "./makers/maker-appimage";
import { existsSync, readFileSync, readdirSync, rmSync, writeFileSync } from "node:fs";
import { spawnSync } from "node:child_process";
import { tmpdir } from "node:os";
import path from "node:path";
import { BUNDLED_TMUX_VERSION } from "./scripts/tmux-version.mjs";
import { checkMacDmgLayout } from "./scripts/build-mac-dmg.mjs";

const DEFAULT_RELEASE_REPO = "Untrivial-ai/agent-orchestrator";

const EXECUTABLE_NAME = "agent-orchestrator";
export const WINDOWS_DISPLAY_NAME = "Orchestrator.inc";
const AUTH_PROTOCOL = {
	name: "Orchestrator authentication callback",
	schemes: ["ao-app"],
};
const AUTH_PROTOCOL_MIME_TYPE = "x-scheme-handler/ao-app";
const PACKAGED_EXTERNAL_DEPENDENCIES = [
	"/node_modules/better-sqlite3",
	"/node_modules/bindings",
	"/node_modules/file-uri-to-path",
];

function ignoreFromVitePackage(file: string): boolean {
	if (!file) return false;
	if (file.startsWith("/.vite")) return file.endsWith(".map");
	if (file === "/node_modules") return false;
	return !PACKAGED_EXTERNAL_DEPENDENCIES.some(
		(dependency) => file === dependency || file.startsWith(`${dependency}/`),
	);
}

async function prepareNativeDependencies(platform: NodeJS.Platform, arch: string): Promise<void> {
	await rebuild({
		buildPath: process.cwd(),
		electronVersion: electronPackage.version,
		platform,
		arch,
		onlyModules: ["better-sqlite3"],
		force: true,
	});
}

const PACKAGER_NAME = "Agent Orchestrator";

export function labelMacBundle(
	buildPath: string,
	setDisplayName: (plistPath: string, name: string) => void = setPlistDisplayName,
): void {
	const contents = path.join(buildPath, `${PACKAGER_NAME}.app`, "Contents");
	setDisplayName(path.join(contents, "Info.plist"), PACKAGER_NAME);
	const resources = path.join(contents, "Resources");
	const label = readFileSync(path.join(resources, "en.lproj", "InfoPlist.strings"));
	for (const locale of readdirSync(resources).filter((entry) => entry.endsWith(".lproj"))) {
		writeFileSync(path.join(resources, locale, "InfoPlist.strings"), label);
	}
}

function setPlistDisplayName(plistPath: string, name: string): void {
	const result = spawnSync("plutil", ["-replace", "CFBundleDisplayName", "-string", name, plistPath], {
		encoding: "utf8",
	});
	if (result.error) throw result.error;
	if (result.status !== 0) throw new Error(`Setting macOS display name failed: ${result.stderr}`);
}

export function extraResourcesForPlatform(platform: NodeJS.Platform): string[] {
	return [
		"daemon",
		"browser-extension",
		"agent-browser",
		"resources/acp-runtime",
		...(platform === "darwin" ? ["update-helper", "wake-helper", "dictation-helper", "cloudflared", "assets/mac/en.lproj"] : []),
		...(platform === "darwin" ? ["assets/Assets.car"] : []),
		...(platform === "darwin" || platform === "linux" ? ["tmux"] : []),
		"assets/icon.png",
		"assets/icon.ico",
		"assets/trayIconTemplate.png",
		"assets/trayIconTemplate@2x.png",
		"assets/trayAwakeIconTemplate.png",
		"assets/trayAwakeIconTemplate@2x.png",
		"assets/trayAwakeIcon.LICENSE",
		"app-update.yml",
	];
}

const ACP_RUNTIME_NODE_PATH = "/Contents/Resources/acp-runtime/node/bin/node";
const ACP_RUNTIME_NODE_ENTITLEMENTS = [
	"com.apple.security.cs.allow-jit",
	"com.apple.security.cs.allow-unsigned-executable-memory",
];

const DICTATION_HELPER_PATHS = [
	"/Contents/Resources/dictation-helper/Orchestrator.app",
	"/Contents/Resources/dictation-helper/Orchestrator.app/Contents/MacOS/Orchestrator",
];
const DICTATION_HELPER_ENTITLEMENTS = ["com.apple.security.device.audio-input"];

export function macSignOptionsForFile(filePath: string): { entitlements?: string[] } {
	if (DICTATION_HELPER_PATHS.some((helper) => filePath.endsWith(helper))) return { entitlements: DICTATION_HELPER_ENTITLEMENTS };
	if (!filePath.endsWith(ACP_RUNTIME_NODE_PATH)) return {};
	return machoHasX86_64Slice(filePath) ? { entitlements: ACP_RUNTIME_NODE_ENTITLEMENTS } : {};
}

function parseReleaseRepo(value: string | undefined): { owner: string; name: string } {
	const [owner, name] = (value || DEFAULT_RELEASE_REPO).split("/");
	if (!owner || !name) {
		const [defOwner, defName] = DEFAULT_RELEASE_REPO.split("/");
		return { owner: defOwner, name: defName };
	}
	return { owner, name };
}

const config: ForgeConfig = {
	packagerConfig: {
		asar: true,
		ignore: ignoreFromVitePackage,
		appBundleId: "dev.agent-orchestrator.desktop",
		name: PACKAGER_NAME,
		executableName: EXECUTABLE_NAME,
		win32metadata: {
			FileDescription: WINDOWS_DISPLAY_NAME,
			InternalName: WINDOWS_DISPLAY_NAME,
			ProductName: WINDOWS_DISPLAY_NAME,
		},
		extendInfo: {
			CFBundleIconName: "AppIcon",
			LSHasLocalizedDisplayName: true,
			CFBundleDevelopmentRegion: "en",
			NSSpeechRecognitionUsageDescription: "Orchestrator turns your speech into message text.",
			NSMicrophoneUsageDescription: "Orchestrator uses the microphone for dictation and for tools you run in it.",
		},
		protocols: [AUTH_PROTOCOL],
		appCategoryType: "public.app-category.developer-tools",
		icon: "assets/icon",
		extraResource: extraResourcesForPlatform(process.platform),
		afterCopyExtraResources: [(buildPath, _version, platform, _arch, callback) => {
			if (platform !== "darwin") return callback();
			try {
				labelMacBundle(buildPath);
				callback();
			} catch (error) {
				callback(error as Error);
			}
		}],
		osxSign: process.env.APPLE_SIGNING_IDENTITY
			? {
					identity: process.env.APPLE_SIGNING_IDENTITY,
					optionsForFile: macSignOptionsForFile,
				}
			: process.env.CSC_LINK
				? { optionsForFile: macSignOptionsForFile }
				: undefined,
		osxNotarize: process.env.AO_NOTARY_PROFILE
			? { keychainProfile: process.env.AO_NOTARY_PROFILE }
			: process.env.APPLE_API_KEY
				? {
						appleApiKey: process.env.APPLE_API_KEY,
						appleApiKeyId: process.env.APPLE_API_KEY_ID!,
						appleApiIssuer: process.env.APPLE_API_ISSUER!,
					}
				: undefined,
	},
	hooks: {
		prePackage: async (_forgeConfig, platform, arch) => {
			await prepareNativeDependencies(platform as NodeJS.Platform, arch);
			if (platform === "darwin") {
				const helperBuild = spawnSync(process.execPath, [path.resolve("scripts/build-update-helper.mjs"), "--arch", arch], { stdio: "inherit" });
				if (helperBuild.error) throw helperBuild.error;
				if (helperBuild.status !== 0) throw new Error("macOS update helper build failed");
                const wakeBuild = spawnSync(process.execPath, [path.resolve("scripts/build-wake-helper.mjs"), "--arch", arch], { stdio: "inherit" });
                if (wakeBuild.error) throw wakeBuild.error;
                if (wakeBuild.status !== 0) throw new Error("macOS wake helper build failed");
				const dictationBuild = spawnSync(process.execPath, [path.resolve("scripts/build-dictation-helper.mjs"), "--arch", arch], { stdio: "inherit" });
				if (dictationBuild.error) throw dictationBuild.error;
				if (dictationBuild.status !== 0) throw new Error("macOS dictation helper build failed");
			}
			const { owner, name } = parseReleaseRepo(process.env.AO_RELEASE_REPO);
			const yml = [
				"provider: github",
				`owner: ${owner}`,
				`repo: ${name}`,
				"updaterCacheDirName: agent-orchestrator-updater",
				"",
			].join("\n");
			writeFileSync("app-update.yml", yml);
		},
		packageAfterPrune: async (_forgeConfig, buildPath) => {
			const nativeModule = path.join(
				buildPath,
				"node_modules",
				"better-sqlite3",
				"build",
				"Release",
				"better_sqlite3.node",
			);
			if (!existsSync(nativeModule)) {
				throw new Error("Packaged app is missing the better-sqlite3 native runtime");
			}
		},
		postPackage: async (_forgeConfig, packageResult) => {
			if (packageResult.platform !== "darwin" && packageResult.platform !== "linux") return;
			for (const outputPath of packageResult.outputPaths) {
				let resourcesPath = path.join(outputPath, "resources");
				if (packageResult.platform === "darwin") {
					const appBundle = readdirSync(outputPath).find((entry) => entry.endsWith(".app"));
					if (!appBundle) throw new Error(`packaged macOS app bundle missing from ${outputPath}`);
					resourcesPath = path.join(outputPath, appBundle, "Contents", "Resources");
					const helper = path.join(resourcesPath, "update-helper", "ao-update-progress");
					if (!existsSync(helper)) throw new Error(`packaged macOS update helper missing from ${helper}`);
					if (!existsSync(path.join(resourcesPath, "wake-helper", "ao-wake"))) throw new Error(`packaged macOS wake helper missing from ${resourcesPath}`);
					if (!existsSync(path.join(resourcesPath, "Assets.car"))) throw new Error(`packaged macOS app icon catalog missing from ${resourcesPath}`);
					if (!existsSync(path.join(resourcesPath, "dictation-helper", "Orchestrator.app", "Contents", "MacOS", "Orchestrator"))) throw new Error(`packaged macOS dictation helper missing from ${resourcesPath}`);
					const cloudflared = path.join(resourcesPath, "cloudflared", "cloudflared");
					if (!existsSync(cloudflared)) throw new Error(`packaged cloudflared missing from ${cloudflared}`);
					const cloudflaredVersion = spawnSync(cloudflared, ["--version"], { encoding: "utf8", timeout: 10_000 });
					if (cloudflaredVersion.status !== 0) throw new Error(`packaged cloudflared failed to run at ${cloudflared}: ${cloudflaredVersion.error?.message || cloudflaredVersion.stderr}`);
				}
				const binary = path.join(resourcesPath, "tmux", "bin", "tmux");
				if (!existsSync(binary)) throw new Error(`packaged tmux missing from ${binary}`);
				const version = spawnSync(binary, ["-V"], { encoding: "utf8" });
				if (version.status !== 0 || version.stdout.trim() !== `tmux ${BUNDLED_TMUX_VERSION}`) {
					throw new Error(`packaged tmux failed verification at ${binary}: ${version.stderr || version.stdout}`);
				}
				const socket = path.join(tmpdir(), `ao-tmux-smoke-${process.pid}.sock`);
				const smoke = spawnSync(binary, ["-S", socket, "-f", "/dev/null", "new-session", "-d", "true"], {
					encoding: "utf8",
					timeout: 5_000,
				});
				try {
					if (smoke.error || smoke.status !== 0) {
						const detail = smoke.error?.message || smoke.stderr || smoke.stdout;
						throw new Error(`packaged tmux could not create a session at ${binary}: ${detail}`);
					}
				} finally {
					spawnSync(binary, ["-S", socket, "kill-server"], { stdio: "ignore" });
					rmSync(socket, { force: true });
				}
			}
		},
		postMake: async (_forgeConfig, makeResults) => {
			for (const result of makeResults) {
				if (result.platform !== "darwin") continue;
				for (const artifact of result.artifacts) {
					if (artifact.endsWith(".dmg")) {
						await checkMacDmgLayout(artifact);
						if (await sealDmg(artifact)) await verifyDmg(artifact);
					} else if (artifact.endsWith(".zip") && isSigningConfigured()) {
						await verifyMacArtifact(artifact);
					}
				}
			}
			return makeResults;
		},
	},
	rebuildConfig: {},
	makers: [
		new MakerNSIS(
			{
				appId: "dev.agent-orchestrator.desktop",
				productName: WINDOWS_DISPLAY_NAME,
				executableName: EXECUTABLE_NAME,
				icon: "assets/icon.ico",
				nsis: { artifactName: "Agent Orchestrator Setup ${version}.${ext}" },
			},
			["win32"],
		),
		{ name: "@electron-forge/maker-zip", platforms: ["darwin"], config: {} },
		new MakerDMG(
			{
				appId: "dev.agent-orchestrator.desktop",
				productName: "Agent Orchestrator",
			},
			["darwin"],
		),
		new MakerAppImage(
			{
				appId: "dev.agent-orchestrator.desktop",
				productName: "Orchestrator",
				appImage: { artifactName: "Agent Orchestrator-${version}.${ext}" },
				icon: "assets/icon.png",
				protocols: [AUTH_PROTOCOL],
			},
			["linux"],
		),
		{
			name: "@electron-forge/maker-deb",
			config: {
				options: {
					bin: EXECUTABLE_NAME,
					icon: "assets/icon.png",
					maintainer: appPackage.author,
					homepage: "https://github.com/aoagents/agent-orchestrator",
					mimeType: [AUTH_PROTOCOL_MIME_TYPE],
				},
			},
		},
		{
			name: "@electron-forge/maker-rpm",
			config: {
				options: {
					icon: "assets/icon.png",
					license: "MIT",
					homepage: "https://github.com/aoagents/agent-orchestrator",
					mimeType: [AUTH_PROTOCOL_MIME_TYPE],
				},
			},
		},
	],
	publishers: [
		{
			name: "@electron-forge/publisher-github",
			config: {
				repository: parseReleaseRepo(process.env.AO_RELEASE_REPO),
				prerelease: process.env.AO_RELEASE_PRERELEASE === "true",
				draft: false,
				generateReleaseNotes: true,
			},
		},
	],
	plugins: [
		new AutoUnpackNativesPlugin({}),
		new VitePlugin({
			build: [
				{ entry: "src/main.ts", config: "vite.main.config.ts", target: "main" },
				{ entry: "src/preload.ts", config: "vite.preload.config.ts", target: "preload" },
				{ entry: "src/annotate-preload.ts", config: "vite.preload.config.ts", target: "preload" },
				{ entry: "src/privacy-guide-preload.ts", config: "vite.preload.config.ts", target: "preload" },
			],
			renderer: [{ name: "main_window", config: "vite.renderer.config.ts" }],
		}),
		new FusesPlugin({
			version: FuseVersion.V1,
			[FuseV1Options.RunAsNode]: false,
			[FuseV1Options.EnableNodeOptionsEnvironmentVariable]: true,
			[FuseV1Options.EnableNodeCliInspectArguments]: false,
			[FuseV1Options.EnableEmbeddedAsarIntegrityValidation]: true,
			[FuseV1Options.OnlyLoadAppFromAsar]: true,
			[FuseV1Options.GrantFileProtocolExtraPrivileges]: false,
		}),
	],
};

export default config;
