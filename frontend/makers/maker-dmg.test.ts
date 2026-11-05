import { describe, expect, it, vi, beforeEach } from "vitest";
import { fileURLToPath } from "node:url";
import { existsSync } from "node:fs";
import path from "node:path";

// Capture buildForge's args without pulling in electron-builder's real machinery.
const buildForge = vi.fn<(forge: { dir: string }, options: any) => Promise<string[]>>(async () => [
	"/out/make/Agent Orchestrator-0.10.3-arm64.dmg",
]);
vi.mock("app-builder-lib", () => ({ buildForge }));

// Capture the commands sealDmg would run, without ever spawning codesign or
// xcrun. maker-dmg promisifies execFile at module load, so the stub has to keep
// the callback shape promisify expects.
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

// What Forge actually hands the maker: `dir` is the PACKAGE directory and the
// bundle sits inside it. electron-builder's mac path treats buildForge's `dir`
// as the .app itself, so this is the only correct value to pass through.
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
		// electron-builder must not try to publish; the workflow does that.
		expect(options.config.publish).toBeNull();
		expect(options.config.appId).toBe("dev.agent-orchestrator.desktop");
		expect(options.config.productName).toBe("Agent Orchestrator");
	});

	// The layout check. buildForge sets `prepackaged: resolve(dir)`, and
	// macPackager.packMacTargets uses prepackaged AS the app path
	// (`appPath = prepackaged ?? join(computeAppOutDir(...), "<name>.app")`), then
	// dmg-builder copies appPath into the image renamed to "<name>.app". Passing
	// Forge's package directory here nests the whole directory under that name and
	// yields "Agent Orchestrator.app/Agent Orchestrator.app", an outer bundle with
	// no Contents that cannot launch. Verified against app-builder-lib 26.15.3.
	it("hands buildForge the .app inside the package dir, not the package dir", async () => {
		await new MakerDMG().make(makeOptions);
		const [forgeOptions] = buildForge.mock.calls.at(-1)!;
		expect(forgeOptions).toEqual({ dir: APP_PATH });
		expect(forgeOptions.dir).not.toBe(makeOptions.dir);
	});

	// buildForge's own default output is dirname(dir)/make, which was correct only
	// while `dir` was the package directory. Now that we pass the .app, that
	// default would land in <packageDir>/make, so the explicit override has to be
	// Forge's own makeDir.
	it("writes artifacts to Forge's makeDir, not a path derived from the .app", async () => {
		await new MakerDMG().make(makeOptions);
		const [, options] = buildForge.mock.calls.at(-1)!;
		expect(options.config.directories.output).toBe(makeOptions.makeDir);
	});

	it("never lets electron-builder re-sign the already notarized .app", () => {
		// packagerConfig.osxSign/osxNotarize seal the bundle before any maker runs.
		// identity: null is what keeps this maker from touching that seal.
		return new MakerDMG().make(makeOptions).then(() => {
			const [, options] = buildForge.mock.calls.at(-1)!;
			expect(options.config.mac.identity).toBeNull();
		});
	});

	it("leaves the dmg container unsigned for the postMake seal to handle", async () => {
		await new MakerDMG().make(makeOptions);
		const [, options] = buildForge.mock.calls.at(-1)!;
		// electron-builder's docs warn that dmg.sign conflicts with notarization;
		// sealDmg signs + notarizes + staples the container instead.
		expect(options.config.dmg.sign).toBe(false);
	});

	// dmg-builder 26.15.3 defaults writeUpdateInfo ON, and DmgTarget.build then
	// calls createBlockmap BEFORE postMake's sealDmg rewrites the dmg bytes. macOS
	// ships full-download-only permanently (#3151, #3267 decision 4), so the
	// sidecar is both against policy and stale on arrival.
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
		// This array is exactly what Forge hands its publisher, so filtering here is
		// what keeps a sidecar off the release regardless of how it got generated.
		expect(await new MakerDMG().make(makeOptions)).toEqual(["/out/make/Agent Orchestrator-0.10.3-arm64.dmg"]);
	});

	// Without these options electron-builder still writes a .DS_Store, but with
	// its generic template background, 80pt icons and a versioned volume name.
	it("lays out the install window: branded background, large icons, app left of Applications", async () => {
		await new MakerDMG().make(makeOptions);
		const [, options] = buildForge.mock.calls.at(-1)!;
		const dmg = options.config.dmg;
		expect(dmg.title).toBe("Orchestrator");
		expect(dmg.iconSize).toBe(128);
		expect(dmg.background).toBe(BACKGROUND_IMAGE);
		expect(path.basename(BACKGROUND_IMAGE)).toBe("background.png");
		// electron-builder only merges in the Retina art if it sits beside the 1x file.
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
		// The layout must not drop the volume or artifact naming either.
		expect(options.config.dmg.artifactName).toBeUndefined();
	});
});

describe("sealDmg", () => {
	const dmg = "/out/make/app.dmg";

	it("does nothing when there is no signing material at all (unsigned local builds)", async () => {
		// The ONLY tolerated incomplete state: no identity and no notary creds.
		// desktop-testing.yml and a plain local `npm run make` rely on this.
		expect(await sealDmg(dmg, {})).toBe(false);
		expect(commands).toEqual([]);
	});

	// Partial credentials used to fail OPEN in both directions: an identity with
	// no notary creds warned and returned success (publishing a signed but
	// unnotarized dmg), and notary creds with no identity returned before doing
	// anything. Both now fail the build.
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

		// The return value gates postMake's verify step: only a sealed dmg can pass
		// verify-mac-artifact.sh, so an unsigned local build must report false.
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
	// Any file that exists; verifyDmg only shells out to it, and execFile is stubbed.
	const existingScript = fileURLToPath(import.meta.url);

	it("runs the canonical verifier script on the dmg", async () => {
		// Sealing proves three commands exited 0 here; only the canonical script
		// proves Gatekeeper accepts the bytes with a stapled ticket (#3288 ws 1/2).
		await verifyDmg(dmg, existingScript);
		expect(commands).toEqual([["bash", [existingScript, dmg]]]);
	});

	it("fails loudly rather than silently skipping when the script is missing", async () => {
		await expect(verifyDmg(dmg, "/no/such/verify-mac-artifact.sh")).rejects.toThrow(/not found/);
		expect(commands).toEqual([]);
	});
});
