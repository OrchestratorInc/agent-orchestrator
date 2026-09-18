import { createCloudClient } from "@aoagents/cloud-client";

/**
 * Proves the cloud client resolves and constructs inside the Expo bundle.
 * Carries no credentials and makes no request.
 */
export function cloudClientResolves(): boolean {
	const client = createCloudClient({
		baseUrl: "https://example.invalid",
		getAccessToken: async () => "unused",
	});
	return typeof client.getCurrentAccount === "function";
}
