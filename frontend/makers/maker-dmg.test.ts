import { describe, expect, it, vi, beforeEach } from "vitest";
import { fileURLToPath } from "node:url";
import { existsSync } from "node:fs";
import path from "node:path";

const buildForge = vi.fn<(forge: { dir: string }, options: any) => Promise<string[]>>(async () => [
	"/out/make/Agent Orchestrator-0.10.3-arm64.dmg",
]);
vi.mock("app-builder-lib", () => ({ buildForge }));

const commands: Array<[string, string[]]> = [];
let nextError: Error | undefined;
vi.mock("node:child_process", async (importOriginal) => {
	const actual = await importOriginal<typeof import("node:child_process")>();
	const execFile = (cmd: string, args: string[], optsOrCb: unknown, maybeCb?: unknown) => {
		const done = (typeof optsOrCb === "function" ? optsOrCb : maybeCb) as (
			err: Error | null,
			stdout?: string,
			stderr?: string,
		) => void;
		commands.push([cmd, args]);
		if (nextError) {
			const err = nextError;
			nextError = undefined;
			done(err);
			return;
		}
		done(null, "", "");
	};
	return { ...actual, default: { ...actual, execFile }, execFile };
});

import MakerDMG, { sealDmg, verifyDmg } from "./maker-dmg";
import { BACKGROUND_IMAGE, VOLUME_ICON } from "../scripts/build-mac-dmg.mjs";

const makeOptions = {
	dir: "/tmp/app/Agent Orchestrator-darwin-arm64",
	makeDir: "/tmp/app/make",
	appName: "Agent Orchestrator",
	targetPlatform: "darwin" as const,
	targetArch: "arm64" as const,
	forgeConfig: {} as never,
	packageJSON: {},
};

const APP_PATH = "/tmp/app/Agent Orchestrator-darwin-arm64/Agent Orchestrator.app";

beforeEach(() => {
	buildForge.mockClear();
	commands.length = 0;
	nextError = undefined;
	vi.spyOn(console, "log").mockImplementation(() => undefined);
	vi.spyOn(console, "warn").mockImplementation(() => undefined);
});

describe("MakerDMG", () => {
	it("targets darwin and only builds on macOS (hdiutil is not portable)", () => {
		const maker = new MakerDMG();
		expect(maker.name).toBe("dmg");
		expect(maker.defaultPlatforms).toEqual(["darwin"]);
		expect(maker.isSupportedOnCurrentPlatform()).toBe(process.platform === "darwin");
	});

	it("builds a dmg target for the requested arch and forwards config", async () => {
		const maker = new MakerDMG({ appId: "dev.agent-orchestrator.desktop" }, ["darwin"]);
		await maker.prepareConfig(makeOptions.targetArch);
		const artifacts = await maker.make(makeOptions);

		expect(artifacts).toEqual(["/out/make/Agent Orchestrator-0.10.3-arm64.dmg"]);
		const [, options] = buildForge.mock.calls[0];
		expect(options.mac).toEqual(["dmg:arm64"]);
		expect(options.config.publish).toBeNull();
		expect(options.config.appId).toBe("dev.agent-orchestrator.desktop");
		expect(options.config.productName).toBe("Agent Orchestrator");
	});

	it("hands buildForge the .app inside the package dir, not the package dir", async () => {
		await new MakerDMG().make(makeOptions);
		const [forgeOptions] = buildForge.mock.calls.at(-1)!;
		expect(forgeOptions).toEqual({ dir: APP_PATH });
		expect(forgeOptions.dir).not.toBe(makeOptions.dir);
	});

	it("writes artifacts to Forge's makeDir, not a path derived from the .app", async () => {
		await new MakerDMG().make(makeOptions);
		const [, options] = buildForge.mock.calls.at(-1)!;
		expect(options.config.directories.output).toBe(makeOptions.makeDir);
	});

	it("never lets electron-builder re-sign the already notarized .app", () => {
		return new MakerDMG().make(makeOptions).then(() => {
			const [, options] = buildForge.mock.calls.at(-1)!;
			expect(options.config.mac.identity).toBeNull();
		});
	});

	it("leaves the dmg container unsigned for the postMake seal to handle", async () => {
		await new MakerDMG().make(makeOptions);
		const [, options] = buildForge.mock.calls.at(-1)!;
		expect(options.config.dmg.sign).toBe(false);
	});

	it("disables dmg blockmap generation", async () => {
		await new MakerDMG().make(makeOptions);
		const [, options] = buildForge.mock.calls.at(-1)!;
		expect(options.config.dmg.writeUpdateInfo).toBe(false);
	});

	it("never returns a dmg blockmap among the artifacts", async () => {
		buildForge.mockResolvedValueOnce([
			"/out/make/Agent Orchestrator-0.10.3-arm64.dmg",
			"/out/make/Agent Orchestrator-0.10.3-arm64.dmg.blockmap",
		]);
		expect(await new MakerDMG().make(makeOptions)).toEqual(["/out/make/Agent Orchestrator-0.10.3-arm64.dmg"]);
	});

	it("lays out the install window: branded background, large icons, app left of Applications", async () => {
		await new MakerDMG().make(makeOptions);
		const [, options] = buildForge.mock.calls.at(-1)!;
		const dmg = options.config.dmg;
		expect(dmg.title).toBe("Orchestrator");
		expect(dmg.iconSize).toBe(128);
		expect(dmg.background).toBe(BACKGROUND_IMAGE);
		expect(path.basename(BACKGROUND_IMAGE)).toBe("background.png");
		expect(existsSync(BACKGROUND_IMAGE)).toBe(true);
		expect(existsSync(BACKGROUND_IMAGE.replace(/\.png$/, "@2x.png"))).toBe(true);
		expect(dmg.icon).toBe(VOLUME_ICON);
		expect(existsSync(VOLUME_ICON)).toBe(true);
		expect(dmg.contents).toEqual([
			{ x: 170, y: 184, type: "file" },
			{ x: 490, y: 184, type: "link", path: "/Applications" },
		]);
	});

	it("keeps the signing-related dmg options when the layout is applied", async () => {
		const maker = new MakerDMG({ dmg: { iconSize: 100 } });
		await maker.prepareConfig(makeOptions.targetArch);
		await maker.make(makeOptions);
		const [, options] = buildForge.mock.calls.at(-1)!;
		expect(options.config.dmg).toMatchObject({ sign: false, writeUpdateInfo: false, iconSize: 100 });
		expect(options.config.dmg.artifactName).toBeUndefined();
	});
});

describe("sealDmg", () => {
	const dmg = "/out/make/app.dmg";

	it("does nothing when there is no signing material at all (unsigned local builds)", async () => {
		expect(await sealDmg(dmg, {})).toBe(false);
		expect(commands).toEqual([]);
	});

	it("throws when a signing identity is present but notary credentials are not", async () => {
		await expect(sealDmg(dmg, { APPLE_SIGNING_IDENTITY: "id" })).rejects.toThrow(/no notarization credentials/);
		expect(commands).toEqual([]);
	});

	it("throws when notary credentials are present but a signing identity is not", async () => {
		await expect(sealDmg(dmg, { AO_NOTARY_PROFILE: "ao" })).rejects.toThrow(/no signing identity/);
		expect(commands).toEqual([]);
	});

	it("throws on a partial App Store Connect key trio even without a signing identity", async () => {
		await expect(
			sealDmg(dmg, { APPLE_API_KEY: "/tmp/key.p8", APPLE_API_KEY_ID: "KEYID" }),
		).rejects.toThrow(/incomplete App Store Connect credentials/);
		expect(commands).toEqual([]);
	});

	it("signs, notarizes with a keychain profile, and staples", async () => {
		const sealed = await sealDmg(dmg, {
			APPLE_SIGNING_IDENTITY: "Developer ID Application: Someone (TEAMID)",
			AO_NOTARY_PROFILE: "ao",
		});

		expect(sealed).toBe(true);
		expect(commands[0]).toEqual([
			"codesign",
			["--sign", "Developer ID Application: Someone (TEAMID)", "--timestamp", "--force", dmg],
		]);
		expect(commands[1]).toEqual(["xcrun", ["notarytool", "submit", dmg, "--keychain-profile", "ao", "--wait"]]);
		expect(commands[2]).toEqual(["xcrun", ["stapler", "staple", dmg]]);
	});

	it("uses the App Store Connect API key trio when that is what CI provides", async () => {
		await sealDmg(dmg, {
			APPLE_SIGNING_IDENTITY: "id",
			APPLE_API_KEY: "/tmp/key.p8",
			APPLE_API_KEY_ID: "KEYID",
			APPLE_API_ISSUER: "ISSUER",
		});
		expect(commands[1][1]).toEqual([
			"notarytool",
			"submit",
			dmg,
			"--key",
			"/tmp/key.p8",
			"--key-id",
			"KEYID",
			"--issuer",
			"ISSUER",
			"--wait",
		]);
	});

	it("falls back to the keychain identity prefix when only CSC_LINK is set", async () => {
		await sealDmg(dmg, { CSC_LINK: "base64p12", AO_NOTARY_PROFILE: "ao" });
		expect(commands[0][1]).toEqual(["--sign", "Developer ID Application", "--timestamp", "--force", dmg]);
	});

	it("fails the build when signing fails and credentials were present", async () => {
		nextError = new Error("codesign: no identity found");
		await expect(sealDmg(dmg, { APPLE_SIGNING_IDENTITY: "id", AO_NOTARY_PROFILE: "ao" })).rejects.toThrow(
			/no identity found/,
		);
	});
});

describe("verifyDmg", () => {
	const dmg = "/out/make/app.dmg";
	const existingScript = fileURLToPath(import.meta.url);

	it("runs the canonical verifier script on the dmg", async () => {
		await verifyDmg(dmg, existingScript);
		expect(commands).toEqual([["bash", [existingScript, dmg]]]);
	});

	it("fails loudly rather than silently skipping when the script is missing", async () => {
		await expect(verifyDmg(dmg, "/no/such/verify-mac-artifact.sh")).rejects.toThrow(/not found/);
		expect(commands).toEqual([]);
	});
});
