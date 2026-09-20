import type { CloudTokens } from "./tokens";

/**
 * The browser result shape this flow needs. Matches
 * `WebBrowser.openAuthSessionAsync`, narrowed to what is actually read so the
 * flow stays unit-testable without a native module.
 */
export type AuthBrowserResult = { type: "success"; url: string } | { type: string };

export type WorkOSSignInDeps = {
	createPkce(): Promise<{ verifier: string; challenge: string }>;
	makeState(): string;
	/** Builds the hosted AuthKit URL. Injected so this module imports no config. */
	authUrl(input: { challenge: string; state: string }): string;
	openAuth(authUrl: string): Promise<AuthBrowserResult>;
	exchange(input: { code: string; codeVerifier: string }): Promise<CloudTokens>;
};

/**
 * Runs one hosted-AuthKit sign-in attempt.
 *
 * Returns null when the user dismissed the browser — a cancel is an ordinary
 * outcome, not an error, and must not surface as a failure banner. Anything
 * else that goes wrong throws.
 */
export async function runWorkOSSignIn(deps: WorkOSSignInDeps): Promise<CloudTokens | null> {
	const { verifier, challenge } = await deps.createPkce();
	const state = deps.makeState();
	const result = await deps.openAuth(deps.authUrl({ challenge, state }));
	if (result.type !== "success" || typeof (result as { url?: unknown }).url !== "string") return null;

	const url = new URL((result as { url: string }).url);
	const returnedState = url.searchParams.get("state");
	if (returnedState !== state) {
		throw new Error("Sign-in could not be completed: the response state did not match this request.");
	}
	const error = url.searchParams.get("error");
	if (error) throw new Error(error);
	const code = url.searchParams.get("code");
	if (!code) throw new Error("Sign-in could not be completed: no authorization code was returned.");

	return await deps.exchange({ code, codeVerifier: verifier });
}
