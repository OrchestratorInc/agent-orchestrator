import { isExpired, type CloudTokens } from "./tokens";

export type TokenProviderDeps = {
	read(): Promise<CloudTokens | null>;
	write(tokens: CloudTokens): Promise<void>;
	clear(): Promise<void>;
	refresh(refreshToken: string): Promise<CloudTokens>;
	now(): number;
};

export interface TokenProvider {
	/** The bearer token to send, or null when there is no usable session. */
	getToken(): Promise<string | null>;
	signOut(): Promise<void>;
}

/**
 * Refresh-aware access to the stored cloud session.
 *
 * Mirrors frontend/src/main/cloud-auth.ts: concurrent callers share one
 * refresh, and a generation counter means a refresh that resolves after
 * sign-out is discarded rather than resurrecting the session.
 */
export function createTokenProvider(deps: TokenProviderDeps): TokenProvider {
	let inFlight: Promise<CloudTokens | null> | null = null;
	let generation = 0;

	async function refreshOnce(tokens: CloudTokens, startedAt: number): Promise<CloudTokens | null> {
		if (!tokens.refreshToken) {
			await deps.clear();
			return null;
		}
		try {
			const next = await deps.refresh(tokens.refreshToken);
			// Sign-out happened while this was in flight; drop the result.
			if (startedAt !== generation) return null;
			await deps.write(next);
			return next;
		} catch {
			if (startedAt === generation) await deps.clear();
			return null;
		}
	}

	return {
		async getToken() {
			// Snapshot the generation before any await: sign-out may run its
			// synchronous body (bumping `generation`) while this call is
			// suspended on `deps.read()`, and any refresh kicked off below must
			// be judged against the generation that was current when this call
			// started, not whatever it is once execution resumes.
			const startedAt = generation;
			const tokens = await deps.read();
			if (!tokens) return null;
			if (!isExpired(tokens, deps.now())) return tokens.accessToken;
			if (startedAt !== generation) return null;
			if (inFlight === null) {
				inFlight = refreshOnce(tokens, startedAt).finally(() => { inFlight = null; });
			}
			return (await inFlight)?.accessToken ?? null;
		},
		async signOut() {
			generation += 1;
			inFlight = null;
			await deps.clear();
		},
	};
}
