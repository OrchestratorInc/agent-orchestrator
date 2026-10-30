import { readAccountHostDirectory, saveAccountRemoteHost } from "./account-remote-hosts";
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
 * there, so it never starts a quick tunnel only to replace it.
 *
 * Returns the host id the account now lists, and whether this call added it,
 * so a caller whose next step fails can take back only what it added. The
 * host id is "" when nothing was linked:
 * without url on an AO Cloud with no fixed addresses (Allow access links again
 * with the quick tunnel's url once it has one), or with ifListed when the
 * account does not list this computer. With ifUnlisted, a listed computer is
 * left as it is and its host id returned. All are decided before the machine
 * moves its account token here, which cuts off an account that had it.
 */
export async function linkThisComputer(
	cloudBaseUrl: string,
	accountId: string,
	hostId: string,
	password: string,
	options: { url?: string; ifListed?: boolean; ifUnlisted?: boolean } = {},
): Promise<{ hostId: string; added: boolean }> {
	const directory = await readAccountHostDirectory(cloudBaseUrl);
	const listed = directory.hosts.some((host) => host.hostId === hostId);
	if (options.url === undefined && !directory.fixedAddresses) return { hostId: "", added: false };
	if (options.ifListed && !listed) return { hostId: "", added: false };
	// Already listed: nothing to do, and no new credential for the account.
	if (options.ifUnlisted && listed) return { hostId, added: false };
	const issued = await apiClient.POST("/api/v1/remote-host/account-token", {
		headers: { Authorization: `Bearer ${password}`, "X-AO-Account-ID": accountId },
	});
	if (issued.error || !issued.data) throw new Error(apiErrorMessage(issued.error));
	await saveAccountRemoteHost(cloudBaseUrl, {
		hostId: issued.data.hostId,
		label: fitLabel(await aoBridge.remotes.computerName()),
		url: options.url,
		token: issued.data.token,
	});
	return { hostId: issued.data.hostId, added: !listed };
}
