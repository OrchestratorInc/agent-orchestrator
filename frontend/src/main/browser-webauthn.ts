import type { App, MessageBoxOptions, MessageBoxReturnValue, Session, WebAuthnAccount } from "electron";
import { readFileSync } from "node:fs";
import path from "node:path";

// forge.config.ts writes this resource before signing, and only when it signs
// the app with the matching keychain-access-groups entitlement and an embedded
// provisioning profile. Its presence is what enables Touch ID at startup.
export const WEBAUTHN_CONFIG_RESOURCE = "webauthn.json";

export type WebAuthnConfig = { touchIDKeychainAccessGroup: string };

// Apple keychain access groups are "<TEAM_ID>.<identifier>".
const KEYCHAIN_ACCESS_GROUP = /^[A-Z0-9]{10}\.[A-Za-z0-9][A-Za-z0-9.-]*$/;

export function touchIdKeychainAccessGroup(teamId: string, bundleId: string): string {
	return `${teamId}.${bundleId}.webauthn`;
}

export function parseWebAuthnConfig(text: string): WebAuthnConfig | null {
	let value: unknown;
	try {
		value = JSON.parse(text);
	} catch {
		return null;
	}
	const group = (value as { touchIDKeychainAccessGroup?: unknown } | null)?.touchIDKeychainAccessGroup;
	return typeof group === "string" && KEYCHAIN_ACCESS_GROUP.test(group) ? { touchIDKeychainAccessGroup: group } : null;
}

type ConfigureBrowserWebAuthnOptions = {
	app: Pick<App, "configureWebAuthn" | "isPackaged">;
	platform: NodeJS.Platform;
	resourcesPath: string;
	readFile?: (filePath: string) => string;
	log?: (message: string, error?: unknown) => void;
};

/**
 * Enables the Touch ID platform authenticator for AO's Browser tabs. Electron
 * leaves passkey requests unserviced until this is called, and Chromium keeps
 * them pending for minutes to hours with no prompt (#6782).
 *
 * Only a packaged macOS build that ships the signing-time resource qualifies.
 * Without the entitlement Chromium reports Touch ID unavailable, so the
 * resource check keeps unsigned and dev builds from even asking.
 */
export function configureBrowserWebAuthn({
	app,
	platform,
	resourcesPath,
	readFile = (filePath) => readFileSync(filePath, "utf8"),
	log = (message, error) => console.warn(message, error ?? ""),
}: ConfigureBrowserWebAuthnOptions): boolean {
	if (platform !== "darwin" || !app.isPackaged) return false;
	let text: string;
	try {
		text = readFile(path.join(resourcesPath, WEBAUTHN_CONFIG_RESOURCE));
	} catch {
		// Not signed with the passkey entitlement: keep today's behavior.
		return false;
	}
	const config = parseWebAuthnConfig(text);
	if (!config) {
		log(`[webauthn] ignoring malformed ${WEBAUTHN_CONFIG_RESOURCE}`);
		return false;
	}
	try {
		app.configureWebAuthn({ touchID: { keychainAccessGroup: config.touchIDKeychainAccessGroup } });
	} catch (error) {
		log("[webauthn] could not enable Touch ID passkeys", error);
		return false;
	}
	return true;
}

type WebAuthnSessionLike = Pick<Session, "on" | "removeListener">;
type SelectAccountDetails = { relyingPartyId: string; accounts: WebAuthnAccount[] };
type SelectAccountCallback = (credentialId?: string) => void;
type SelectAccountListener = (event: unknown, details: SelectAccountDetails, callback: SelectAccountCallback) => void;

export type WebAuthnAccountChooser = {
	attach(session: WebAuthnSessionLike | undefined): void;
	dispose(): void;
};

type WebAuthnAccountChooserOptions = {
	showMessageBox: (options: MessageBoxOptions) => Promise<Pick<MessageBoxReturnValue, "response">>;
	log?: (message: string, error?: unknown) => void;
};

function accountLabel(account: WebAuthnAccount, index: number): string {
	const name = account.name?.trim();
	const displayName = account.displayName?.trim();
	if (displayName && name && displayName !== name) return `${displayName} (${name})`;
	return name || displayName || `Passkey ${index + 1}`;
}

/**
 * Lets the user pick a passkey when a site's request matches several. Electron
 * cancels the request when no listener handles the event, and it fires for the
 * Touch ID authenticator and for a USB security key holding several passkeys.
 *
 * Electron reuses a persistent partition's Session across windows, so the host
 * disposes the chooser with its window; otherwise a recreated window would add
 * a second listener and answer the same request twice.
 */
export function createWebAuthnAccountChooser({
	showMessageBox,
	log = (message, error) => console.warn(message, error ?? ""),
}: WebAuthnAccountChooserOptions): WebAuthnAccountChooser {
	const attachedSessions = new Map<WebAuthnSessionLike, SelectAccountListener>();
	let disposed = false;

	const choose = async (details: SelectAccountDetails, callback: SelectAccountCallback): Promise<void> => {
		let credentialId: string | undefined;
		try {
			const labels = details.accounts.map(accountLabel);
			const { response } = await showMessageBox({
				type: "question",
				title: "Choose a passkey",
				message: `Choose a passkey for ${details.relyingPartyId}`,
				buttons: [...labels, "Cancel"],
				defaultId: 0,
				cancelId: labels.length,
				noLink: true,
			});
			credentialId = details.accounts[response]?.credentialId;
		} catch (error) {
			log("[webauthn] passkey chooser failed", error);
		} finally {
			// The request stays pending until the callback runs; with no
			// credential it is cancelled and the page receives NotAllowedError.
			if (credentialId) callback(credentialId);
			else callback();
		}
	};

	return {
		attach(session) {
			if (!session || disposed || attachedSessions.has(session)) return;
			const listener: SelectAccountListener = (_event, details, callback) => {
				void choose(details, callback);
			};
			attachedSessions.set(session, listener);
			session.on("select-webauthn-account", listener);
		},
		dispose() {
			if (disposed) return;
			disposed = true;
			for (const [session, listener] of attachedSessions) {
				session.removeListener("select-webauthn-account", listener);
			}
			attachedSessions.clear();
		},
	};
}
