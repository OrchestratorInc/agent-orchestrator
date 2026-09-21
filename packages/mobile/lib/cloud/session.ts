import { isExpired, type CloudTokens } from "./tokens";
import { RefreshRejectedError } from "./signIn";

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
	/** Invalidates pending reads/refreshes before storing a new login. */
	replace(tokens: CloudTokens): Promise<void>;
	signOut(): Promise<void>;
}

/**
 * Refresh-aware access to the stored cloud session.
 *
 * Concurrent callers share one refresh. Session replacement and sign-out
 * invalidate pending reads/refreshes, and storage mutations remain ordered.
 */
export function createTokenProvider(deps: TokenProviderDeps): TokenProvider {
	let inFlight: Promise<CloudTokens | null> | null = null;
	let generation = 0;
	let storageWrite: Promise<void> | null = null;

	// SecureStore operations are asynchronous. Serialize mutations so a late
	// refresh write cannot overwrite a newer login or a sign-out clear.
	function mutateStorage(operation: () => Promise<void>): Promise<void> {
		const next = storageWrite ? storageWrite.then(operation, operation) : operation();
		storageWrite = next;
		const finish = () => { if (storageWrite === next) storageWrite = null; };
		void next.then(finish, finish);
		return next;
	}

	async function refreshOnce(tokens: CloudTokens, startedAt: number): Promise<CloudTokens | null> {
		if (!tokens.refreshToken) {
			await mutateStorage(async () => { if (startedAt === generation) await deps.clear(); });
			return null;
		}
		try {
			const next = await deps.refresh(tokens.refreshToken);
			// Sign-out happened while this was in flight; drop the result.
			if (startedAt !== generation) return null;
			await mutateStorage(async () => { if (startedAt === generation) await deps.write(next); });
			if (startedAt !== generation) return null;
			return next;
		} catch (error) {
			if (startedAt !== generation) return null;
			if (error instanceof RefreshRejectedError) {
				await mutateStorage(async () => { if (startedAt === generation) await deps.clear(); });
				return null;
			}
			throw error;
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
			if (storageWrite) await storageWrite;
			if (startedAt !== generation) return null;
			const tokens = await deps.read();
			if (startedAt !== generation) return null;
			if (!tokens) return null;
			if (!isExpired(tokens, deps.now())) return tokens.accessToken;
			if (inFlight === null) {
				const refresh = refreshOnce(tokens, startedAt).finally(() => {
					if (inFlight === refresh) inFlight = null;
				});
				inFlight = refresh;
			}
			const refreshed = await inFlight;
			return startedAt === generation ? refreshed?.accessToken ?? null : null;
		},
		async replace(tokens) {
			generation += 1;
			inFlight = null;
			await mutateStorage(() => deps.write(tokens));
		},
		async signOut() {
			generation += 1;
			inFlight = null;
			await mutateStorage(() => deps.clear());
		},
	};
}
