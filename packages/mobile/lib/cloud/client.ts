import { createCloudClient, type CloudClient } from "@aoagents/cloud-client";
import type { TokenProvider } from "./session";

/**
 * The cloud client, bound to the device's token provider.
 *
 * Unlike the desktop renderer there is no proxy and no placeholder token:
 * React Native has no CORS restriction and no privileged main process, so the
 * real bearer goes on the request here. Never use `streamEvents` from this
 * client — it relies on `getReader()`/`TextDecoder`, which React Native
 * cannot support.
 */
export function createMobileCloudClient(input: {
	baseUrl: string;
	tokens: Pick<TokenProvider, "getToken">;
	isCurrent?: () => boolean;
}): CloudClient {
	return createCloudClient({
		baseUrl: input.baseUrl,
		requestTimeoutMs: 20_000,
		getAccessToken: async () => {
			if (input.isCurrent?.() === false) throw new Error("Cloud account changed. Try again.");
			const token = await input.tokens.getToken();
			if (input.isCurrent?.() === false) throw new Error("Cloud account changed. Try again.");
			return token;
		},
	});
}
