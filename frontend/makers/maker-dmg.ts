import path from "node:path";
import { existsSync } from "node:fs";
import { execFile } from "node:child_process";
import { promisify } from "node:util";
import { MakerBase, type MakerOptions } from "@electron-forge/maker-base";
import type { ForgePlatform } from "@electron-forge/shared-types";
import { macDmgLayout } from "../scripts/build-mac-dmg.mjs";

const run = promisify(execFile);

export type MakerDMGConfig = {
	appId?: string;
	productName?: string;
	dmg?: Record<string, unknown>;
};

export default class MakerDMG extends MakerBase<MakerDMGConfig> {
	name = "dmg";
	defaultPlatforms: ForgePlatform[] = ["darwin"];

	isSupportedOnCurrentPlatform(): boolean {
		return process.platform === "darwin";
	}

	async make({ dir, makeDir, targetArch, appName }: MakerOptions): Promise<string[]> {
		const { buildForge } = await import("app-builder-lib");
		const cfg = this.config ?? {};
		const appPath = path.join(dir, `${appName}.app`);
		const artifacts = await buildForge(
			{ dir: appPath },
			{
				mac: [`dmg:${targetArch}`],
				config: {
					appId: cfg.appId,
					productName: cfg.productName ?? appName,
					directories: { output: makeDir },
					publish: null,
					mac: {
						identity: null,
					},
					dmg: {
						...macDmgLayout(),
						sign: false,
						writeUpdateInfo: false,
						...cfg.dmg,
					},
				},
			},
		);
		return artifacts.filter((artifact) => !artifact.endsWith(".dmg.blockmap"));
	}
}

function resolveSigningIdentity(env: NodeJS.ProcessEnv): string | undefined {
	if (env.APPLE_SIGNING_IDENTITY) return env.APPLE_SIGNING_IDENTITY;
	if (env.CSC_LINK) return "Developer ID Application";
	return undefined;
}

export function isSigningConfigured(env: NodeJS.ProcessEnv = process.env): boolean {
	return resolveSigningIdentity(env) !== undefined;
}

function resolveNotaryArgs(env: NodeJS.ProcessEnv): string[] | undefined {
	if (env.AO_NOTARY_PROFILE) return ["--keychain-profile", env.AO_NOTARY_PROFILE];
	if (env.APPLE_API_KEY && env.APPLE_API_KEY_ID && env.APPLE_API_ISSUER) {
		return ["--key", env.APPLE_API_KEY, "--key-id", env.APPLE_API_KEY_ID, "--issuer", env.APPLE_API_ISSUER];
	}
	return undefined;
}

function assertCompleteAppStoreConnectCredentials(env: NodeJS.ProcessEnv): void {
	const credentials = [env.APPLE_API_KEY, env.APPLE_API_KEY_ID, env.APPLE_API_ISSUER];
	const supplied = credentials.filter((credential) => credential !== undefined);
	if (supplied.length === 0) return;
	if (supplied.length === credentials.length && credentials.every((credential) => credential)) return;
	throw new Error(
		"[dmg] incomplete App Store Connect credentials; APPLE_API_KEY, APPLE_API_KEY_ID, and APPLE_API_ISSUER must " +
			"all be set and non-empty. Refusing to publish a dmg with partial notarization credentials.",
	);
}

export async function sealDmg(dmgPath: string, env: NodeJS.ProcessEnv = process.env): Promise<boolean> {
	const identity = resolveSigningIdentity(env);
	assertCompleteAppStoreConnectCredentials(env);
	const notaryArgs = resolveNotaryArgs(env);

	if (!identity && !notaryArgs) {
		console.warn(`[dmg] no signing material (APPLE_SIGNING_IDENTITY / CSC_LINK); leaving ${dmgPath} unsigned`);
		return false;
	}
	if (!identity) {
		throw new Error(
			`[dmg] notarization credentials are set but no signing identity is (APPLE_SIGNING_IDENTITY / CSC_LINK); ` +
				`refusing to publish an unsigned ${dmgPath}. Set both, or neither for an unsigned local build.`,
		);
	}
	if (!notaryArgs) {
		throw new Error(
			`[dmg] a signing identity is set but no notarization credentials are (AO_NOTARY_PROFILE, or the ` +
				`APPLE_API_KEY / APPLE_API_KEY_ID / APPLE_API_ISSUER trio); refusing to publish an unnotarized ` +
				`${dmgPath}. Set both, or neither for an unsigned local build.`,
		);
	}

	console.log(`[dmg] signing ${dmgPath}`);
	await run("codesign", ["--sign", identity, "--timestamp", "--force", dmgPath]);

	console.log(`[dmg] notarizing ${dmgPath} (this waits on Apple)`);
	await run("xcrun", ["notarytool", "submit", dmgPath, ...notaryArgs, "--wait"], {
		maxBuffer: 10 * 1024 * 1024,
	});

	console.log(`[dmg] stapling ${dmgPath}`);
	await run("xcrun", ["stapler", "staple", dmgPath]);
	return true;
}

const VERIFY_SCRIPT = "scripts/verify-mac-artifact.sh";

export async function verifyMacArtifact(
	artifactPath: string,
	scriptPath: string = path.resolve(VERIFY_SCRIPT),
): Promise<void> {
	if (!existsSync(scriptPath)) {
		throw new Error(
			`[dmg] cannot verify ${artifactPath}: ${scriptPath} not found (run electron-forge from frontend/)`,
		);
	}
	console.log(`[dmg] verifying ${artifactPath}`);
	const { stdout } = await run("bash", [scriptPath, artifactPath], { maxBuffer: 10 * 1024 * 1024 });
	if (stdout) console.log(stdout.trim());
}

export const verifyDmg = verifyMacArtifact;

export { MakerDMG };
