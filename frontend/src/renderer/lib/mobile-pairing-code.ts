import { apiErrorException } from "./api-error-exception";
import { apiClient } from "./api-client";

export const mobilePairingCodeQueryKey = ["mobile-pairing-code"] as const;

/** How often a shown pairing code is replaced: before the host's 10 minutes run out. */
export const PAIRING_CODE_REFRESH_MS = 9 * 60_000;

export type PairingCode = { code: string; expiresAt: number };

export async function fetchPairingCode(): Promise<PairingCode> {
	const { data, error } = await apiClient.POST("/api/v1/mobile/pairing-code");
	if (error || !data?.code) throw apiErrorException(error);
	return { code: data.code, expiresAt: Date.parse(data.expiresAt) };
}
