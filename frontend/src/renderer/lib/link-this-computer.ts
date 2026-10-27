import { saveAccountRemoteHost } from "./account-remote-hosts";
import { apiClient, apiErrorMessage } from "./api-client";
import { aoBridge } from "./bridge";

// AO Cloud rejects host labels longer than 64 bytes.
const LABEL_MAX_BYTES = 64;

export function fitLabel(name: string): string {
	let chars = Array.from(name.trim());
	while (new TextEncoder().encode(chars.join("")).length > LABEL_MAX_BYTES) chars = chars.slice(0, -1);
	return chars.join("").trim();
}

/**
 * Links this computer to the signed-in AO account, so it appears on the
 * account's devices without a password. Without url it is done before the
 * tunnel starts: AO Cloud makes this computer's fixed address and lists it
 * there, so it never starts a quick tunnel only to replace it. An AO Cloud
 * that has no fixed addresses refuses that. Allow access then links again with
 * the quick tunnel's url once it has one; Connect Mobile leaves the computer
 * unlisted, as it did before fixed addresses.
 * Returns the host id the account now lists.
 */
export async function linkThisComputer(cloudBaseUrl: string, accountId: string, password: string, url?: string): Promise<string> {
	const issued = await apiClient.POST("/api/v1/remote-host/account-token", {
		headers: { Authorization: `Bearer ${password}`, "X-AO-Account-ID": accountId },
	});
	if (issued.error || !issued.data) throw new Error(apiErrorMessage(issued.error));
	await saveAccountRemoteHost(cloudBaseUrl, {
		hostId: issued.data.hostId,
		label: fitLabel(await aoBridge.remotes.computerName()),
		url,
		token: issued.data.token,
	});
	return issued.data.hostId;
}
