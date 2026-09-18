import * as SecureStore from "expo-secure-store";

/** Refresh this long before nominal expiry so a token cannot die mid-request. */
export const EXPIRY_SKEW_MS = 60_000;

const KEY = "ao.cloud.tokens";

export type CloudTokens = {
	accessToken: string;
	/** WorkOS sessions rotate a refresh token; local opaque-token sessions have none. */
	refreshToken?: string;
	/** Epoch milliseconds. */
	expiresAt: number;
};

/**
 * Cloud bearer tokens live only in the device keystore, never in AsyncStorage —
 * the same rule lib/config.ts applies to the daemon connection password.
 */
export async function readTokens(): Promise<CloudTokens | null> {
	try {
		const raw = await SecureStore.getItemAsync(KEY);
		if (!raw) return null;
		const parsed = JSON.parse(raw) as Partial<CloudTokens>;
		if (typeof parsed.accessToken !== "string" || typeof parsed.expiresAt !== "number") return null;
		return {
			accessToken: parsed.accessToken,
			refreshToken: typeof parsed.refreshToken === "string" ? parsed.refreshToken : undefined,
			expiresAt: parsed.expiresAt,
		};
	} catch {
		return null;
	}
}

export async function writeTokens(tokens: CloudTokens): Promise<void> {
	await SecureStore.setItemAsync(KEY, JSON.stringify(tokens));
}

export async function clearTokens(): Promise<void> {
	await SecureStore.deleteItemAsync(KEY);
}

export function isExpired(tokens: CloudTokens, now: number): boolean {
	return tokens.expiresAt - EXPIRY_SKEW_MS <= now;
}
