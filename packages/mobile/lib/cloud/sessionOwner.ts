import * as Crypto from "expo-crypto";
import { CloudApiError } from "@aoagents/cloud-client";
import * as WebBrowser from "expo-web-browser";
import type { Account } from "../account";
import { createMobileCloudClient } from "./client";
import { CLOUD_CONFIGURATION_ERROR, CLOUD_DEPLOYMENT } from "./config";
import type { CloudDeployment } from "./deployment";
import { createPkcePair, toBase64Url } from "./pkce";
import { createTokenProvider, type TokenProvider } from "./session";
import { buildWorkOSAuthUrl, exchangeWorkOSCode, refreshWorkOSTokens } from "./signIn";
import { clearTokens, readTokens, writeTokens, type CloudTokens } from "./tokens";
import { runWorkOSSignIn } from "./workosFlow";

export interface AccountSessionSnapshot {
	account: Account | null;
	signedIn: boolean | null;
	epoch: number;
	busy: boolean;
	error: string | null;
}
export interface AccountSessionOwner {
	getSnapshot(): AccountSessionSnapshot;
	subscribe(listener: () => void): () => void;
	initialize(): Promise<void>;
	getToken(): Promise<string | null>;
	signIn(run: () => Promise<CloudTokens | null>): Promise<boolean>;
	signOut(): Promise<void>;
}

export function createAccountSessionOwner({ tokens, readAccount }: {
	tokens: TokenProvider; readAccount: () => Promise<Account>;
}): AccountSessionOwner {
	let snapshot: AccountSessionSnapshot = { account: null, signedIn: null, epoch: 0, busy: false, error: null };
	let initialization: { epoch: number; promise: Promise<void> } | null = null;
	const listeners = new Set<() => void>();
	const publish = (patch: Partial<AccountSessionSnapshot>) => {
		snapshot = { ...snapshot, ...patch };
		for (const listener of listeners) listener();
	};
	const message = (error: unknown) => error instanceof Error ? error.message : "Could not load your account.";
	async function rejectIdentity(error: unknown, epoch: number): Promise<boolean> {
		if (!(error instanceof CloudApiError && error.status === 401) || epoch !== snapshot.epoch) return false;
		publish({ epoch: epoch + 1, account: null, signedIn: false, busy: false, error: "Session expired. Sign in again." });
		await tokens.signOut();
		return true;
	}
	return {
		getSnapshot: () => snapshot,
		subscribe(listener) { listeners.add(listener); return () => { listeners.delete(listener); }; },
		initialize() {
			if (snapshot.busy || snapshot.account || snapshot.signedIn === false) return Promise.resolve();
			const epoch = snapshot.epoch;
			if (initialization?.epoch === epoch) return initialization.promise;
			let promise!: Promise<void>;
			promise = (async () => {
				try {
					const token = await tokens.getToken();
					if (epoch !== snapshot.epoch) return;
					publish({ signedIn: token !== null });
					if (!token) return;
					const account = await readAccount();
					if (epoch === snapshot.epoch) publish({ account, error: null });
				} catch (error) {
					if (!await rejectIdentity(error, epoch) && epoch === snapshot.epoch) publish({ signedIn: true, error: message(error) });
				} finally {
					if (initialization?.promise === promise) initialization = null;
				}
			})();
			initialization = { epoch, promise };
			return promise;
		},
		async getToken() {
			const epoch = snapshot.epoch;
			if (snapshot.busy || snapshot.signedIn === false) return null;
			const token = await tokens.getToken();
			if (epoch === snapshot.epoch && !token) publish({ account: null, signedIn: false });
			return epoch === snapshot.epoch ? token : null;
		},
		async signIn(run) {
			const previous = snapshot;
			const epoch = snapshot.epoch + 1;
			publish({ epoch, account: null, signedIn: null, busy: true, error: null });
			try {
				const issued = await run();
				if (epoch !== snapshot.epoch) return false;
				if (!issued) {
					publish({ account: previous.account, signedIn: previous.signedIn });
					return false;
				}
				await tokens.replace(issued);
				if (epoch !== snapshot.epoch) return false;
				publish({ signedIn: true });
				const account = await readAccount();
				if (epoch !== snapshot.epoch) return false;
				publish({ account, error: null });
				return true;
			} catch (error) {
				if (epoch !== snapshot.epoch) return false;
				if (await rejectIdentity(error, epoch)) throw error;
				publish({ error: message(error) });
				throw error;
			} finally {
				if (epoch === snapshot.epoch) publish({ busy: false });
			}
		},
		async signOut() {
			publish({ epoch: snapshot.epoch + 1, account: null, signedIn: false, busy: false, error: null });
			await tokens.signOut();
		},
	};
}

const owners = new Map<string, AccountSessionOwner>();
export function getAccountSessionOwner(deployment: CloudDeployment = CLOUD_DEPLOYMENT): AccountSessionOwner {
	const existing = owners.get(deployment.key);
	if (existing) return existing;
	const tokens = createTokenProvider({
		read: () => readTokens(deployment.key),
		write: (value) => writeTokens(value, deployment.key),
		clear: () => clearTokens(deployment.key),
		refresh: (refreshToken) => refreshWorkOSTokens({ clientId: deployment.clientId, refreshToken }),
		now: Date.now,
	});
	const owner = createAccountSessionOwner({
		tokens,
		readAccount: async () => {
			if (CLOUD_CONFIGURATION_ERROR && deployment.key === CLOUD_DEPLOYMENT.key) throw new Error(CLOUD_CONFIGURATION_ERROR);
			const { user } = await createMobileCloudClient({ baseUrl: deployment.baseUrl, tokens }).getCurrentAccount();
			return { id: user.id, email: user.email };
		},
	});
	owners.set(deployment.key, owner);
	return owner;
}

export function signInToDeployment(deployment: CloudDeployment = CLOUD_DEPLOYMENT, owner = getAccountSessionOwner(deployment)): Promise<boolean> {
	return owner.signIn(() => {
		if (CLOUD_CONFIGURATION_ERROR && deployment.key === CLOUD_DEPLOYMENT.key) throw new Error(CLOUD_CONFIGURATION_ERROR);
		return runWorkOSSignIn({
			createPkce: createPkcePair,
			makeState: () => toBase64Url(btoa(String.fromCharCode(...Crypto.getRandomBytes(32)))),
			authUrl: ({ challenge, state }) => buildWorkOSAuthUrl({ clientId: deployment.clientId, redirectUri: deployment.redirectUri, codeChallenge: challenge, state }),
			openAuth: (url) => WebBrowser.openAuthSessionAsync(url, deployment.redirectUri),
			exchange: ({ code, codeVerifier }) => exchangeWorkOSCode({ clientId: deployment.clientId, code, codeVerifier }),
		});
	});
}
