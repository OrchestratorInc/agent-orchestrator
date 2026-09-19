import type { CloudClient } from "@aoagents/cloud-client";
import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { createMobileCloudClient } from "./client";
import { CLOUD_BASE_URL } from "./config";
import { resolveOrg } from "./org";
import { registerWithLocalAuth, signInWithLocalAuth, type RegisterLocalAuthInput } from "./signIn";
import { createTokenProvider } from "./session";
import { clearTokens, readTokens, writeTokens } from "./tokens";
import { wrapUnauthorized } from "./unauthorized";

/**
 * The cloud environment's live auth state: whether a session is stored, and
 * which org it's scoped to.
 *
 * `signedIn` and `orgId` are both `null` until resolution finishes at least
 * once — distinct from `false`/no-org, the same "not known yet" shape
 * `OnboardingInput` and `resolveSessionSource`'s `CloudResolveInput` already
 * use. A consumer that treats `null` as `false` here would flash a signed-out
 * empty state on every cold start before the stored session has been read.
 */
export type CloudAuthState = {
	signedIn: boolean | null;
	/** The account's resolved org, or null while sign-in/org resolution is
	 *  still in flight — see resolveSessionSource's CloudResolveInput. */
	orgId: string | null;
	client: CloudClient;
	baseUrl: string;
	/** The last sign-in/registration failure, cleared on the next attempt. */
	error: string | null;
	busy: boolean;
	signInLocal(email: string, password: string): Promise<void>;
	registerLocal(input: RegisterLocalAuthInput): Promise<void>;
	signOut(): Promise<void>;
};

const CloudAuthContext = createContext<CloudAuthState | null>(null);

export function useCloudAuth(): CloudAuthState {
	const ctx = useContext(CloudAuthContext);
	if (!ctx) throw new Error("useCloudAuth must be used within <CloudAuthProvider>");
	return ctx;
}

export function CloudAuthProvider({
	children,
	baseUrl = CLOUD_BASE_URL,
}: {
	children: ReactNode;
	baseUrl?: string;
}) {
	const [signedIn, setSignedIn] = useState<boolean | null>(null);
	const [orgId, setOrgId] = useState<string | null>(null);
	const [error, setError] = useState<string | null>(null);
	const [busy, setBusy] = useState(false);
	// Bumped on sign-in/sign-out so a resolution kicked off before either one
	// (the mount-time read, or an org lookup still in flight) cannot land its
	// result after the state it was resolving has moved on — the same
	// generation-counter guard lib/cloud/session.ts uses for token refresh.
	const generationRef = useRef(0);

	const tokens = useMemo(
		() =>
			createTokenProvider({
				read: readTokens,
				write: writeTokens,
				clear: clearTokens,
				// WorkOS sign-in (the only kind that yields a refresh token) has no
				// screen wired up yet — see signIn.ts's exchangeWorkOSCode, which
				// nothing here calls. A local-auth session (the only kind this
				// provider issues) never carries a refreshToken, so
				// createTokenProvider's refreshOnce short-circuits to sign-out
				// before this ever runs. It exists only so a WorkOS token minted
				// some other way doesn't hang forever on first expiry.
				refresh: async () => {
					throw new Error("This session can't be refreshed. Sign in again.");
				},
				now: Date.now,
			}),
		[],
	);

	// Called from inside the wrapped client (see wrapUnauthorized) whenever any
	// cloud call 401s. A local-auth session has no refresh token, so an
	// expired one 401s forever otherwise: `signedIn` would stay `true` while
	// resolveSessionSource keeps handing out a live cloud source that can
	// never succeed, and CloudUnreadyState would claim "you're signed in"
	// with no way to recover short of reinstalling. Dropping to signed-out
	// here sends the user back to the sign-in prompt instead.
	const handleUnauthorized = useCallback(() => {
		generationRef.current += 1;
		void tokens.signOut();
		setSignedIn(false);
		setOrgId(null);
	}, [tokens]);

	const client = useMemo(
		() => wrapUnauthorized(createMobileCloudClient({ baseUrl, tokens }), handleUnauthorized),
		[baseUrl, tokens, handleUnauthorized],
	);

	const resolveSession = useCallback(
		async (generation: number) => {
			let signedInNow: boolean;
			try {
				const raw = await readTokens();
				if (generation !== generationRef.current) return;
				signedInNow = raw !== null;
				setSignedIn(signedInNow);
			} catch {
				if (generation !== generationRef.current) return;
				setSignedIn(false);
				setOrgId(null);
				return;
			}
			if (!signedInNow) {
				setOrgId(null);
				return;
			}
			try {
				const org = await resolveOrg(client);
				if (generation !== generationRef.current) return;
				setOrgId(org.id);
			} catch {
				if (generation !== generationRef.current) return;
				// Signed in but the org couldn't be resolved right now (offline,
				// control plane hiccup). Stay signed in rather than bouncing a real
				// session to the sign-in screen: resolveSessionSource treats a null
				// orgId as "not ready yet", and the board can retry.
				setOrgId(null);
			}
		},
		[client],
	);

	useEffect(() => {
		void resolveSession(generationRef.current);
		// Intentionally mount-only: signInLocal/registerLocal/signOut each start
		// their own resolution under a freshly bumped generation.
		// eslint-disable-next-line react-hooks/exhaustive-deps
	}, []);

	const signInLocal = useCallback(
		async (email: string, password: string) => {
			setBusy(true);
			setError(null);
			try {
				const issued = await signInWithLocalAuth(baseUrl, email, password);
				await writeTokens(issued);
				generationRef.current += 1;
				await resolveSession(generationRef.current);
			} catch (e) {
				const message = e instanceof Error ? e.message : "Sign-in failed.";
				setError(message);
				throw e;
			} finally {
				setBusy(false);
			}
		},
		[baseUrl, resolveSession],
	);

	const registerLocal = useCallback(
		async (input: RegisterLocalAuthInput) => {
			setBusy(true);
			setError(null);
			try {
				const issued = await registerWithLocalAuth(baseUrl, input);
				await writeTokens(issued);
				generationRef.current += 1;
				await resolveSession(generationRef.current);
			} catch (e) {
				const message = e instanceof Error ? e.message : "Registration failed.";
				setError(message);
				throw e;
			} finally {
				setBusy(false);
			}
		},
		[baseUrl, resolveSession],
	);

	const signOut = useCallback(async () => {
		generationRef.current += 1;
		await tokens.signOut();
		setSignedIn(false);
		setOrgId(null);
		setError(null);
	}, [tokens]);

	const value = useMemo<CloudAuthState>(
		() => ({ signedIn, orgId, client, baseUrl, error, busy, signInLocal, registerLocal, signOut }),
		[signedIn, orgId, client, baseUrl, error, busy, signInLocal, registerLocal, signOut],
	);

	return <CloudAuthContext.Provider value={value}>{children}</CloudAuthContext.Provider>;
}
