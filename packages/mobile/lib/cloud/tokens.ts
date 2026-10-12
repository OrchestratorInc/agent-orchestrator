import * as SecureStore from "expo-secure-store";
import { CLOUD_DEPLOYMENT } from "./config";

/** Refresh this long before nominal expiry so a token cannot die mid-request. */
export const EXPIRY_SKEW_MS = 60_000;

function storageKey(deploymentKey: string): string {
	// SecureStore keys accept alphanumeric characters, dots, dashes and
	// underscores. This collision-free encoding is not a secret.
	return "ao.cloud.tokens.v2." + Array.from(deploymentKey).map((char) =>
		char.codePointAt(0)!.toString(16).padStart(6, "0")).join("");
}

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
export async function readTokens(deploymentKey = CLOUD_DEPLOYMENT.key): Promise<CloudTokens | null> {
	try {
		const raw = await SecureStore.getItemAsync(storageKey(deploymentKey));
		if (!raw) return null;
		const envelope = JSON.parse(raw) as { version?: number; deploymentKey?: string; tokens?: Partial<CloudTokens> };
		if (envelope.version !== 2 || envelope.deploymentKey !== deploymentKey || !envelope.tokens) return null;
		const parsed = envelope.tokens;
		if (typeof parsed.accessToken !== "string" || !parsed.accessToken || typeof parsed.expiresAt !== "number" || !Number.isFinite(parsed.expiresAt)) return null;
		return {
			accessToken: parsed.accessToken,
			refreshToken: typeof parsed.refreshToken === "string" ? parsed.refreshToken : undefined,
			expiresAt: parsed.expiresAt,
		};
	} catch {
		return null;
	}
}

export async function writeTokens(tokens: CloudTokens, deploymentKey = CLOUD_DEPLOYMENT.key): Promise<void> {
	await SecureStore.setItemAsync(storageKey(deploymentKey), JSON.stringify({ version: 2, deploymentKey, tokens }));
	await SecureStore.deleteItemAsync("ao.cloud.tokens");
}

export async function clearTokens(deploymentKey = CLOUD_DEPLOYMENT.key): Promise<void> {
	await SecureStore.deleteItemAsync(storageKey(deploymentKey));
	await SecureStore.deleteItemAsync("ao.cloud.tokens");
}

export function isExpired(tokens: CloudTokens, now: number): boolean {
	return tokens.expiresAt - EXPIRY_SKEW_MS <= now;
}
