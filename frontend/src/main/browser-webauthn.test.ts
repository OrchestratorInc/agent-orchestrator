import { EventEmitter } from "node:events";
import path from "node:path";
import { describe, expect, it, vi } from "vitest";
import {
	configureBrowserWebAuthn,
	createWebAuthnAccountChooser,
	parseWebAuthnConfig,
	touchIdKeychainAccessGroup,
	WEBAUTHN_CONFIG_RESOURCE,
} from "./browser-webauthn";

const GROUP = "X9LNSP8MC2.dev.agent-orchestrator.desktop.webauthn";
const RESOURCES = "/Applications/Agent Orchestrator.app/Contents/Resources";

describe("parseWebAuthnConfig", () => {
	it("reads the keychain access group written at signing time", () => {
		expect(parseWebAuthnConfig(JSON.stringify({ touchIDKeychainAccessGroup: GROUP }))).toEqual({
			touchIDKeychainAccessGroup: GROUP,
		});
		expect(touchIdKeychainAccessGroup("X9LNSP8MC2", "dev.agent-orchestrator.desktop")).toBe(GROUP);
	});

	it.each([
		["malformed JSON", "{"],
		["no group", "{}"],
		["a group without a team prefix", JSON.stringify({ touchIDKeychainAccessGroup: "dev.agent-orchestrator.desktop.webauthn" })],
		["a non-string group", JSON.stringify({ touchIDKeychainAccessGroup: 7 })],
		["null", "null"],
	])("rejects %s", (_name, text) => {
		expect(parseWebAuthnConfig(text)).toBeNull();
	});
});

describe("configureBrowserWebAuthn", () => {
	function setup({
		platform = "darwin" as NodeJS.Platform,
		isPackaged = true,
		readFile = vi.fn((_filePath: string) => JSON.stringify({ touchIDKeychainAccessGroup: GROUP })),
		configureWebAuthn = vi.fn(),
	} = {}) {
		const log = vi.fn();
		const enabled = configureBrowserWebAuthn({
			app: { isPackaged, configureWebAuthn },
			platform,
			resourcesPath: RESOURCES,
			readFile,
			log,
		});
		return { enabled, configureWebAuthn, readFile, log };
	}

	it("enables the Touch ID authenticator in a packaged macOS build signed for passkeys", () => {
		const { enabled, configureWebAuthn, readFile } = setup();

		expect(enabled).toBe(true);
		expect(readFile).toHaveBeenCalledWith(path.join(RESOURCES, WEBAUTHN_CONFIG_RESOURCE));
		expect(configureWebAuthn).toHaveBeenCalledWith({ touchID: { keychainAccessGroup: GROUP } });
	});

	it("keeps today's behavior when the build was not signed for passkeys", () => {
		const { enabled, configureWebAuthn, log } = setup({
			readFile: vi.fn(() => {
				throw Object.assign(new Error("ENOENT"), { code: "ENOENT" });
			}),
		});

		expect(enabled).toBe(false);
		expect(configureWebAuthn).not.toHaveBeenCalled();
		expect(log).not.toHaveBeenCalled();
	});

	it.each([
		["Windows", { platform: "win32" as NodeJS.Platform }],
		["Linux", { platform: "linux" as NodeJS.Platform }],
		["an unpackaged dev build", { isPackaged: false }],
	])("never asks on %s", (_name, options) => {
		const { enabled, configureWebAuthn, readFile } = setup(options);

		expect(enabled).toBe(false);
		expect(readFile).not.toHaveBeenCalled();
		expect(configureWebAuthn).not.toHaveBeenCalled();
	});

	it("ignores a malformed resource instead of guessing a group", () => {
		const { enabled, configureWebAuthn, log } = setup({ readFile: vi.fn(() => "not json") });

		expect(enabled).toBe(false);
		expect(configureWebAuthn).not.toHaveBeenCalled();
		expect(log).toHaveBeenCalledWith(expect.stringContaining("malformed"));
	});

	it("does not let an Electron rejection abort startup", () => {
		const error = new Error("invalid keychainAccessGroup");
		const { enabled, log } = setup({
			configureWebAuthn: vi.fn(() => {
				throw error;
			}),
		});

		expect(enabled).toBe(false);
		expect(log).toHaveBeenCalledWith(expect.stringContaining("Touch ID"), error);
	});
});

describe("createWebAuthnAccountChooser", () => {
	const ACCOUNTS = [
		{ credentialId: "cred-1", name: "jane@example.com", displayName: "Jane Doe" },
		{ credentialId: "cred-2", name: "work@example.com" },
		{ credentialId: "cred-3" },
	];

	function setup(showMessageBox = vi.fn(async (_options: Electron.MessageBoxOptions) => ({ response: 0 }))) {
		const log = vi.fn();
		const chooser = createWebAuthnAccountChooser({ showMessageBox, log });
		const session = new EventEmitter();
		chooser.attach(session as never);
		const select = async (accounts = ACCOUNTS) => {
			const callback = vi.fn();
			session.emit("select-webauthn-account", {}, { relyingPartyId: "accounts.google.com", accounts, frame: null }, callback);
			await vi.waitFor(() => expect(callback).toHaveBeenCalled());
			return callback;
		};
		return { chooser, session, select, showMessageBox, log };
	}

	it("asks which passkey to use and returns the chosen credential", async () => {
		const { select, showMessageBox } = setup(vi.fn(async () => ({ response: 1 })));

		const callback = await select();

		expect(callback).toHaveBeenCalledExactlyOnceWith("cred-2");
		expect(showMessageBox).toHaveBeenCalledWith(
			expect.objectContaining({
				message: "Choose a passkey for accounts.google.com",
				buttons: ["Jane Doe (jane@example.com)", "work@example.com", "Passkey 3", "Cancel"],
				defaultId: 0,
				cancelId: 3,
			}),
		);
	});

	it("cancels the request when the user dismisses the chooser", async () => {
		const { select } = setup(vi.fn(async () => ({ response: 3 })));

		const callback = await select();

		// No argument: Electron cancels and the page receives NotAllowedError.
		expect(callback).toHaveBeenCalledExactlyOnceWith();
	});

	it("still settles the request exactly once when the dialog fails", async () => {
		const error = new Error("window destroyed");
		const { select, log } = setup(vi.fn(async () => Promise.reject(error)));

		const callback = await select();

		expect(callback).toHaveBeenCalledExactlyOnceWith();
		expect(log).toHaveBeenCalledWith(expect.stringContaining("chooser"), error);
	});

	it("listens once per session, however many tabs share it", () => {
		const { chooser, session } = setup();

		chooser.attach(session as never);
		chooser.attach(undefined);

		expect(session.listenerCount("select-webauthn-account")).toBe(1);
	});

	it("stops listening when disposed, so a window's replacement chooser is the only one", () => {
		const { chooser, session } = setup();

		chooser.dispose();
		chooser.attach(session as never);
		expect(session.listenerCount("select-webauthn-account")).toBe(0);

		createWebAuthnAccountChooser({ showMessageBox: vi.fn() }).attach(session as never);
		expect(session.listenerCount("select-webauthn-account")).toBe(1);
	});
});
