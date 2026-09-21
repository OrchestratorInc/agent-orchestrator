import type { CloudClient } from "@aoagents/cloud-client";
import * as Crypto from "expo-crypto";
import * as WebBrowser from "expo-web-browser";
import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { AppState } from "react-native";
import { createMobileCloudClient } from "./client";
import { CLOUD_BASE_URL, WORKOS_CLIENT_ID, WORKOS_REDIRECT_URI } from "./config";
import { resolveOrg } from "./org";
import { createPkcePair, toBase64Url } from "./pkce";
import {
	buildWorkOSAuthUrl,
	exchangeWorkOSCode,
	refreshWorkOSTokens,
	registerWithLocalAuth,
	signInWithLocalAuth,
	type RegisterLocalAuthInput,
} from "./signIn";
import { createTokenProvider } from "./session";
import { clearTokens, readTokens, writeTokens, type CloudTokens } from "./tokens";
import { wrapUnauthorized } from "./unauthorized";
import { runWorkOSSignIn } from "./workosFlow";

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
	orgLoading: boolean;
	orgError: string | null;
	retryOrgResolution(): Promise<void>;
	/** Increments whenever the authenticated account session changes. */
	sessionEpoch: number;
	client: CloudClient;
	baseUrl: string;
	/** The last sign-in/registration failure, cleared on the next attempt. */
	error: string | null;
	busy: boolean;
	/** True after a completed sign-in; false when the auth browser is dismissed. */
	signInWithWorkOS(): Promise<boolean>;
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
	const [orgLoading, setOrgLoading] = useState(false);
	const [orgError, setOrgError] = useState<string | null>(null);
	const orgResolutionRef = useRef<{ generation: number; promise: Promise<void> } | null>(null);
	const [error, setError] = useState<string | null>(null);
	const [busy, setBusy] = useState(false);
	const [sessionEpoch, setSessionEpoch] = useState(0);
	// Bumped on sign-in/sign-out so a resolution kicked off before either one
	// (the mount-time read, or an org lookup still in flight) cannot land its
	// result after the state it was resolving has moved on — the same
	// generation-counter guard lib/cloud/session.ts uses for token refresh.
	const generationRef = useRef(0);
	const advanceSessionEpoch = useCallback(() => {
		generationRef.current += 1;
		setSessionEpoch(generationRef.current);
		setOrgLoading(false);
		setOrgError(null);
		return generationRef.current;
	}, []);

	const tokens = useMemo(
		() =>
			createTokenProvider({
				read: readTokens,
				write: writeTokens,
				clear: clearTokens,
				refresh: (refreshToken) => refreshWorkOSTokens({ clientId: WORKOS_CLIENT_ID, refreshToken }),
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
	const handleUnauthorized = useCallback((generation: number) => {
		if (generation !== generationRef.current) return;
		advanceSessionEpoch();
		void tokens.signOut();
		setSignedIn(false);
		setOrgId(null);
	}, [advanceSessionEpoch, tokens]);

	const client = useMemo(
		() => wrapUnauthorized(createMobileCloudClient({ baseUrl, tokens }), handleUnauthorized, () => generationRef.current),
		[baseUrl, tokens, handleUnauthorized],
	);

	const resolveSession = useCallback(
		(generation: number): Promise<void> => {
			if (generation !== generationRef.current) return Promise.resolve();
			if (orgResolutionRef.current?.generation === generation) return orgResolutionRef.current.promise;
			setOrgLoading(true);
			setOrgError(null);
			const promise = (async () => {
				try {
					const raw = await readTokens();
					if (generation !== generationRef.current) return;
					setSignedIn(raw !== null);
					if (!raw) {
						setOrgId(null);
						return;
					}
					const org = await resolveOrg(client, () => generation === generationRef.current);
					if (generation !== generationRef.current) return;
					setOrgId(org.id);
				} catch (cause) {
					if (generation !== generationRef.current) return;
					setOrgId(null);
					setOrgError(cause instanceof Error ? cause.message : "Could not load your cloud workspace.");
				} finally {
					if (generation === generationRef.current) setOrgLoading(false);
					if (orgResolutionRef.current?.generation === generation) orgResolutionRef.current = null;
				}
			})();
			orgResolutionRef.current = { generation, promise };
			return promise;
		},
		[client],
	);
	const retryOrgResolution = useCallback(() => resolveSession(generationRef.current), [resolveSession]);

	useEffect(() => {
		const listener = AppState.addEventListener("change", (state) => {
			if (state === "active" && signedIn && !orgId) void retryOrgResolution();
		});
		return () => listener.remove();
	}, [signedIn, orgId, retryOrgResolution]);

	useEffect(() => {
		void resolveSession(generationRef.current);
		// Intentionally mount-only: signInLocal/registerLocal/signOut each start
		// their own resolution under a freshly bumped generation.
		// eslint-disable-next-line react-hooks/exhaustive-deps
	}, []);

	const acceptSession = useCallback(async (issued: CloudTokens) => {
		const generation = advanceSessionEpoch();
		setOrgId(null);
		await tokens.replace(issued);
		await resolveSession(generation);
	}, [advanceSessionEpoch, resolveSession, tokens]);

	const signInLocal = useCallback(
		async (email: string, password: string) => {
			setBusy(true);
			setError(null);
			try {
				const issued = await signInWithLocalAuth(baseUrl, email, password);
				await acceptSession(issued);
			} catch (e) {
				const message = e instanceof Error ? e.message : "Sign-in failed.";
				setError(message);
				throw e;
			} finally {
				setBusy(false);
			}
		},
		[acceptSession, baseUrl],
	);

	const signInWithWorkOS = useCallback(async (): Promise<boolean> => {
		setBusy(true);
		setError(null);
		try {
			const issued = await runWorkOSSignIn({
				createPkce: createPkcePair,
				makeState: () =>
					toBase64Url(btoa(String.fromCharCode(...Crypto.getRandomBytes(32)))),
				authUrl: ({ challenge, state }) =>
					buildWorkOSAuthUrl({
						clientId: WORKOS_CLIENT_ID,
						redirectUri: WORKOS_REDIRECT_URI,
						codeChallenge: challenge,
						state,
					}),
				openAuth: (url) => WebBrowser.openAuthSessionAsync(url, WORKOS_REDIRECT_URI),
				exchange: ({ code, codeVerifier }) =>
					exchangeWorkOSCode({ clientId: WORKOS_CLIENT_ID, code, codeVerifier }),
			});
			if (!issued) return false;
			await acceptSession(issued);
			return true;
		} catch (e) {
			const message = e instanceof Error ? e.message : "Sign-in failed.";
			setError(message);
			throw e;
		} finally {
			setBusy(false);
		}
	}, [acceptSession]);

	const registerLocal = useCallback(
		async (input: RegisterLocalAuthInput) => {
			setBusy(true);
			setError(null);
			try {
				const issued = await registerWithLocalAuth(baseUrl, input);
				await acceptSession(issued);
			} catch (e) {
				const message = e instanceof Error ? e.message : "Registration failed.";
				setError(message);
				throw e;
			} finally {
				setBusy(false);
			}
		},
		[acceptSession, baseUrl],
	);

	const signOut = useCallback(async () => {
		advanceSessionEpoch();
		await tokens.signOut();
		setSignedIn(false);
		setOrgId(null);
		setError(null);
	}, [advanceSessionEpoch, tokens]);

	const value = useMemo<CloudAuthState>(
		() => ({ signedIn, orgId, orgLoading, orgError, retryOrgResolution, sessionEpoch, client, baseUrl, error, busy, signInWithWorkOS, signInLocal, registerLocal, signOut }),
		[signedIn, orgId, orgLoading, orgError, retryOrgResolution, sessionEpoch, client, baseUrl, error, busy, signInWithWorkOS, signInLocal, registerLocal, signOut],
	);

	return <CloudAuthContext.Provider value={value}>{children}</CloudAuthContext.Provider>;
}
