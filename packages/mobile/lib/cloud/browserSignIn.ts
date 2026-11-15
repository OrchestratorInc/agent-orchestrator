import * as Application from "expo-application";
import * as WebBrowser from "expo-web-browser";
import { NativeModules, Platform } from "react-native";
import { parseLoopbackCallback } from "./agentLogin";

// One-tap coding-agent sign-in: the provider's own page in the browser that
// already holds the person's sessions, finishing on a localhost redirect this
// phone catches itself.
//
// - iOS: ASWebAuthenticationSession (openAuthSessionAsync), which shares
//   Safari's cookies, passwords and passkeys. The loopback answers the redirect
//   with ao-app://agent-login, which is the session's callback, so the sheet
//   closes by itself.
// - Android: a Custom Tab (openBrowserAsync), which runs in the person's
//   Chrome profile. The loopback answers with an intent:// URL naming this
//   app's package, so Chrome returns to it without a chooser.

type LoopbackNative = {
	start(port: number, callbackPath: string, returnURL: string): Promise<number>;
	waitForCallback(): Promise<string>;
	stop(): void;
};

const native: LoopbackNative | undefined = NativeModules.AOOAuthLoopback;

/** Whether this build has the loopback listener (builds before it fall back to codes). */
export function canSignInWithBrowser(): boolean {
	return Boolean(native);
}

export const AGENT_LOGIN_RETURN = "ao-app://agent-login";

/** Where the loopback sends the browser once it has the code: back into this app. */
export function agentLoginReturnUrl(platform: string = Platform.OS, applicationId: string | null = Application.applicationId): string {
	if (platform === "android" && applicationId) return `intent://agent-login#Intent;scheme=ao-app;package=${applicationId};end`;
	return AGENT_LOGIN_RETURN;
}

export class BrowserSignInUnavailable extends Error {
	constructor(message: string, readonly reason: "unsupported" | "port-busy" | "failed") {
		super(message);
		this.name = "BrowserSignInUnavailable";
	}
}

const SIGN_IN_TIMEOUT_MS = 10 * 60_000;

export type BrowserSignIn<Login extends { state: string; authorizeUrl: string }> = {
	/** The listener's port, for the redirect the authorize URL names. */
	port: number;
	/** Opens the browser and resolves with the authorization code. */
	run(login: Login): Promise<string>;
	cancel(): void;
};

/**
 * Start listening for a sign-in redirect on `port` (0 for any free one).
 * Throws BrowserSignInUnavailable when the listener cannot run, so the caller
 * can offer the code-based fallback instead.
 */
export async function prepareBrowserSignIn<Login extends { state: string; authorizeUrl: string }>(port: number, callbackPath: string): Promise<BrowserSignIn<Login>> {
	if (!native) throw new BrowserSignInUnavailable("This build can't finish browser sign-in.", "unsupported");
	let bound: number;
	try {
		bound = await native.start(port, callbackPath, agentLoginReturnUrl());
	} catch (cause) {
		const code = (cause as { code?: string })?.code;
		throw new BrowserSignInUnavailable(code === "in_use" ? "The sign-in port on this phone is busy." : "Couldn't start the sign-in listener.", code === "in_use" ? "port-busy" : "failed");
	}
	let cancelled = false;
	let cancelWait: (() => void) | undefined;
	return {
		port: bound,
		async run(login) {
			const callback = native.waitForCallback();
			const stopped = new Promise<never>((_resolve, reject) => {
				cancelWait = () => reject(Object.assign(new Error("Sign-in cancelled."), { name: "AbortError" }));
			});
			const timeout = new Promise<never>((_resolve, reject) => {
				setTimeout(() => reject(new Error("Sign-in timed out. Start again.")), SIGN_IN_TIMEOUT_MS);
			});
			try {
				if (Platform.OS === "ios") {
					const browser = WebBrowser.openAuthSessionAsync(login.authorizeUrl, AGENT_LOGIN_RETURN).then((result) => ({ result }));
					const first = await Promise.race([callback.then((target) => ({ target })), browser, stopped, timeout]);
					if ("target" in first) return parseLoopbackCallback(first.target, login.state);
					// The sheet closed. With the redirect it closes itself, a beat after the
					// loopback answered; without it the person closed it.
					const target = await Promise.race([callback, new Promise<null>((resolve) => setTimeout(() => resolve(null), first.result.type === "success" ? 3_000 : 800))]);
					if (!target) throw Object.assign(new Error("Sign-in cancelled."), { name: "AbortError" });
					return parseLoopbackCallback(target, login.state);
				}
				await WebBrowser.openBrowserAsync(login.authorizeUrl, { showInRecents: true });
				const target = await Promise.race([callback, stopped, timeout]);
				return parseLoopbackCallback(target, login.state);
			} finally {
				native.stop();
				if (Platform.OS === "ios" && !cancelled) WebBrowser.dismissAuthSession();
			}
		},
		cancel() {
			cancelled = true;
			cancelWait?.();
			native.stop();
			if (Platform.OS === "ios") WebBrowser.dismissAuthSession();
		},
	};
}

/**
 * Open a provider page (Claude's code page, ChatGPT's device page, GitHub's
 * App install page) in the browser that holds the person's sessions, rather
 * than a private one, so they arrive signed in.
 *
 * Resolves "closed" on iOS, where the auth session returns once the person
 * closes it, and "opened" on Android, where the Custom Tab returns at once.
 */
export async function openSignedInBrowser(url: string): Promise<"closed" | "opened"> {
	if (Platform.OS === "ios") {
		await WebBrowser.openAuthSessionAsync(url, AGENT_LOGIN_RETURN);
		return "closed";
	}
	await WebBrowser.openBrowserAsync(url, { showInRecents: true });
	return "opened";
}
