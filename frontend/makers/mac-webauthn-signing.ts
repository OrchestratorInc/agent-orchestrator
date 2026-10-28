import { spawnSync } from "node:child_process";
import { mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { touchIdKeychainAccessGroup } from "../src/main/browser-webauthn";

// Touch ID passkeys (#6782) need the top-level app signed with a
// keychain-access-groups entitlement. macOS treats that entitlement as
// restricted: a Developer ID app carrying it without an embedded provisioning
// profile from the same team is killed before main, while codesign, spctl and
// notarization all still pass. So the entitlement is only ever added together
// with a profile that grants it, and a profile that does not is a build error.

// @electron/osx-sign's default.darwin.plist, which signs the top-level app
// when no override is given. An override replaces that file, so it carries
// every default forward (forge.config.test.ts pins this against the package).
export const OSX_SIGN_DEFAULT_APP_ENTITLEMENTS = [
	"com.apple.security.cs.allow-jit",
	"com.apple.security.device.audio-input",
	"com.apple.security.device.bluetooth",
	"com.apple.security.device.camera",
	"com.apple.security.device.print",
	"com.apple.security.device.usb",
	"com.apple.security.personal-information.location",
] as const;

export type AppEntitlements = Record<string, boolean | string | string[]>;

export type MacWebAuthnSigning = {
	provisioningProfile: string;
	keychainAccessGroup: string;
	appEntitlements: AppEntitlements;
};

type Run = (
	command: string,
	args: string[],
	options: { encoding: "utf8" },
) => { status: number | null; stdout: string; stderr: string; error?: Error };

/**
 * Returns the Entitlements dictionary a provisioning profile grants.
 * `security cms -D` decodes the signed profile; plutil extracts only that
 * dictionary, because the profile's certificate blobs have no JSON form.
 */
export function readProvisioningProfileEntitlements(
	profilePath: string,
	run: Run = spawnSync,
): Record<string, unknown> {
	const dir = mkdtempSync(path.join(tmpdir(), "ao-provisioning-profile-"));
	try {
		const decoded = path.join(dir, "profile.plist");
		const decode = run("security", ["cms", "-D", "-i", profilePath, "-o", decoded], { encoding: "utf8" });
		if (decode.error) throw decode.error;
		if (decode.status !== 0) {
			throw new Error(`could not decode provisioning profile ${profilePath}: ${decode.stderr.trim()}`);
		}
		const extract = run("plutil", ["-extract", "Entitlements", "json", "-o", "-", decoded], { encoding: "utf8" });
		if (extract.error) throw extract.error;
		if (extract.status !== 0) {
			throw new Error(`provisioning profile ${profilePath} has no Entitlements: ${extract.stderr.trim()}`);
		}
		const entitlements: unknown = JSON.parse(extract.stdout);
		if (!entitlements || typeof entitlements !== "object" || Array.isArray(entitlements)) {
			throw new Error(`provisioning profile ${profilePath} has malformed Entitlements`);
		}
		return entitlements as Record<string, unknown>;
	} finally {
		rmSync(dir, { recursive: true, force: true });
	}
}

// Profiles grant identifiers either exactly or with a trailing wildcard.
function grants(pattern: unknown, value: string): boolean {
	if (typeof pattern !== "string") return false;
	return pattern.endsWith("*") ? value.startsWith(pattern.slice(0, -1)) : pattern === value;
}

/**
 * Derives the passkey signing inputs from what the profile grants, failing
 * closed: entitlements the profile does not authorize would produce an app
 * that macOS refuses to launch.
 */
export function macWebAuthnSigning(
	profilePath: string,
	bundleId: string,
	profileEntitlements: Record<string, unknown>,
): MacWebAuthnSigning {
	const teamId = profileEntitlements["com.apple.developer.team-identifier"];
	if (typeof teamId !== "string" || !/^[A-Z0-9]{10}$/.test(teamId)) {
		throw new Error(`provisioning profile ${profilePath} does not name a team`);
	}
	const applicationIdentifier = `${teamId}.${bundleId}`;
	if (!grants(profileEntitlements["com.apple.application-identifier"], applicationIdentifier)) {
		throw new Error(`provisioning profile ${profilePath} does not cover ${applicationIdentifier}`);
	}
	const keychainAccessGroup = touchIdKeychainAccessGroup(teamId, bundleId);
	const groups = profileEntitlements["keychain-access-groups"];
	if (!Array.isArray(groups) || !groups.some((group) => grants(group, keychainAccessGroup))) {
		throw new Error(`provisioning profile ${profilePath} does not grant keychain access group ${keychainAccessGroup}`);
	}
	return {
		provisioningProfile: profilePath,
		keychainAccessGroup,
		appEntitlements: {
			...Object.fromEntries(OSX_SIGN_DEFAULT_APP_ENTITLEMENTS.map((key) => [key, true])),
			"com.apple.application-identifier": applicationIdentifier,
			"com.apple.developer.team-identifier": teamId,
			"keychain-access-groups": [keychainAccessGroup],
		},
	};
}

function escapeXml(value: string): string {
	return value.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
}

export function entitlementsPlist(entitlements: AppEntitlements): string {
	const entries = Object.entries(entitlements).map(([key, value]) => {
		const rendered =
			typeof value === "boolean"
				? `\t\t<${value}/>`
				: Array.isArray(value)
					? ["\t\t<array>", ...value.map((item) => `\t\t\t<string>${escapeXml(item)}</string>`), "\t\t</array>"].join("\n")
					: `\t\t<string>${escapeXml(value)}</string>`;
		return `\t\t<key>${escapeXml(key)}</key>\n${rendered}`;
	});
	return [
		'<?xml version="1.0" encoding="UTF-8"?>',
		'<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">',
		'<plist version="1.0">',
		"\t<dict>",
		...entries,
		"\t</dict>",
		"</plist>",
		"",
	].join("\n");
}

export function writeEntitlementsPlist(entitlements: AppEntitlements): string {
	const dir = mkdtempSync(path.join(tmpdir(), "ao-app-entitlements-"));
	const filePath = path.join(dir, "entitlements.plist");
	writeFileSync(filePath, entitlementsPlist(entitlements));
	return filePath;
}
