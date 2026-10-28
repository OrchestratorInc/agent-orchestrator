import { execFileSync } from "node:child_process";
import { existsSync, readFileSync, rmSync } from "node:fs";
import { createRequire } from "node:module";
import path from "node:path";
import { describe, expect, it, vi } from "vitest";
import {
	entitlementsPlist,
	macWebAuthnSigning,
	OSX_SIGN_DEFAULT_APP_ENTITLEMENTS,
	readProvisioningProfileEntitlements,
	writeEntitlementsPlist,
} from "./mac-webauthn-signing";

const PROFILE = "/secure/AO_Developer_ID.provisionprofile";
const BUNDLE_ID = "dev.agent-orchestrator.desktop";
const TEAM = "X9LNSP8MC2";
const GROUP = `${TEAM}.${BUNDLE_ID}.webauthn`;

// What Apple's Developer ID profiles grant: an explicit app identifier and a
// team-wide keychain group wildcard.
const DEVELOPER_ID_GRANTS = {
	"com.apple.developer.team-identifier": TEAM,
	"com.apple.application-identifier": `${TEAM}.${BUNDLE_ID}`,
	"keychain-access-groups": [`${TEAM}.*`],
};

describe("macWebAuthnSigning", () => {
	it("adds the passkey keychain group to osx-sign's default app entitlements", () => {
		const signing = macWebAuthnSigning(PROFILE, BUNDLE_ID, DEVELOPER_ID_GRANTS);

		expect(signing.provisioningProfile).toBe(PROFILE);
		expect(signing.keychainAccessGroup).toBe(GROUP);
		expect(signing.appEntitlements).toEqual({
			...Object.fromEntries(OSX_SIGN_DEFAULT_APP_ENTITLEMENTS.map((key) => [key, true])),
			"com.apple.application-identifier": `${TEAM}.${BUNDLE_ID}`,
			"com.apple.developer.team-identifier": TEAM,
			"keychain-access-groups": [GROUP],
		});
	});

	it("accepts a team-wide app identifier and an exact keychain group", () => {
		const signing = macWebAuthnSigning(PROFILE, BUNDLE_ID, {
			...DEVELOPER_ID_GRANTS,
			"com.apple.application-identifier": `${TEAM}.*`,
			"keychain-access-groups": [GROUP],
		});

		expect(signing.keychainAccessGroup).toBe(GROUP);
	});

	// Each of these would produce an app that macOS refuses to launch.
	it.each([
		["names no team", { ...DEVELOPER_ID_GRANTS, "com.apple.developer.team-identifier": undefined }, "does not name a team"],
		[
			"belongs to another app",
			{ ...DEVELOPER_ID_GRANTS, "com.apple.application-identifier": `${TEAM}.com.example.other` },
			"does not cover",
		],
		[
			"grants another team's keychain groups",
			{ ...DEVELOPER_ID_GRANTS, "keychain-access-groups": ["ABCDE12345.*"] },
			"does not grant keychain access group",
		],
		[
			"grants no keychain groups",
			{ ...DEVELOPER_ID_GRANTS, "keychain-access-groups": undefined },
			"does not grant keychain access group",
		],
	])("fails the build when the profile %s", (_name, grants, message) => {
		expect(() => macWebAuthnSigning(PROFILE, BUNDLE_ID, grants)).toThrow(message);
	});
});

describe("readProvisioningProfileEntitlements", () => {
	it("decodes the signed profile and extracts only its Entitlements", () => {
		const run = vi.fn((command: string, _args: string[]) =>
			command === "security"
				? { status: 0, stdout: "", stderr: "" }
				: { status: 0, stdout: JSON.stringify(DEVELOPER_ID_GRANTS), stderr: "" },
		);

		expect(readProvisioningProfileEntitlements(PROFILE, run)).toEqual(DEVELOPER_ID_GRANTS);
		const [decodeCall, extractCall] = run.mock.calls;
		expect(decodeCall?.[0]).toBe("security");
		expect(decodeCall?.[1].slice(0, 5)).toEqual(["cms", "-D", "-i", PROFILE, "-o"]);
		const decoded = decodeCall?.[1][5];
		expect(extractCall).toEqual(["plutil", ["-extract", "Entitlements", "json", "-o", "-", decoded], { encoding: "utf8" }]);
		// The decoded profile (with its certificates) does not outlive the read.
		expect(existsSync(path.dirname(decoded!))).toBe(false);
	});

	it("fails when the profile cannot be decoded", () => {
		const run = vi.fn(() => ({ status: 1, stdout: "", stderr: "security: unable to decode" }));

		expect(() => readProvisioningProfileEntitlements(PROFILE, run)).toThrow("could not decode provisioning profile");
	});

	it("fails when the profile has no usable Entitlements", () => {
		const run = vi.fn((command: string) =>
			command === "security" ? { status: 0, stdout: "", stderr: "" } : { status: 0, stdout: "[]", stderr: "" },
		);

		expect(() => readProvisioningProfileEntitlements(PROFILE, run)).toThrow("malformed Entitlements");
	});
});

describe("entitlementsPlist", () => {
	const signing = macWebAuthnSigning(PROFILE, BUNDLE_ID, DEVELOPER_ID_GRANTS);

	it("renders booleans, strings and the keychain group array", () => {
		const plist = entitlementsPlist(signing.appEntitlements);

		expect(plist).toContain("<key>com.apple.security.cs.allow-jit</key>\n\t\t<true/>");
		expect(plist).toContain(`<key>com.apple.application-identifier</key>\n\t\t<string>${TEAM}.${BUNDLE_ID}</string>`);
		expect(plist).toContain(`<key>keychain-access-groups</key>\n\t\t<array>\n\t\t\t<string>${GROUP}</string>\n\t\t</array>`);
		expect(entitlementsPlist({ "a&b": "<c>" })).toContain("<key>a&amp;b</key>\n\t\t<string>&lt;c&gt;</string>");
	});

	it.runIf(process.platform === "darwin")("is a property list codesign can read", () => {
		const filePath = writeEntitlementsPlist(signing.appEntitlements);
		try {
			execFileSync("plutil", ["-lint", filePath]);
			const parsed = JSON.parse(execFileSync("plutil", ["-convert", "json", "-o", "-", filePath], { encoding: "utf8" }));
			expect(parsed).toEqual(signing.appEntitlements);
		} finally {
			rmSync(path.dirname(filePath), { recursive: true, force: true });
		}
	});

	it("keeps every entitlement @electron/osx-sign gives the app by default", () => {
		const require = createRequire(import.meta.url);
		const osxSignEntry = require.resolve("@electron/osx-sign");
		const defaults = readFileSync(
			path.join(osxSignEntry.slice(0, osxSignEntry.lastIndexOf(`${path.sep}dist${path.sep}`)), "entitlements", "default.darwin.plist"),
			"utf8",
		);
		const keys = [...defaults.matchAll(/<key>([^<]+)<\/key>/g)].map((match) => match[1]);

		expect([...OSX_SIGN_DEFAULT_APP_ENTITLEMENTS].sort()).toEqual(keys.sort());
	});
});
