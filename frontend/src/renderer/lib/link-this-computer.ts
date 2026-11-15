import { apiErrorException } from "./api-error-exception";
import { readAccountHostDirectory, saveAccountRemoteHost } from "./account-remote-hosts";
import { apiClient } from "./api-client";
import { aoBridge } from "./bridge";

const LABEL_MAX_BYTES = 64;

export function fitLabel(name: string): string {
	let chars = Array.from(name.trim());
	while (new TextEncoder().encode(chars.join("")).length > LABEL_MAX_BYTES) chars = chars.slice(0, -1);
	return chars.join("").trim();
}

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
	if (options.ifUnlisted && listed) return { hostId, added: false };
	const issued = await apiClient.POST("/api/v1/remote-host/account-token", {
		headers: { Authorization: `Bearer ${password}`, "X-AO-Account-ID": accountId },
	});
	if (issued.error || !issued.data) throw apiErrorException(issued.error);
	await saveAccountRemoteHost(cloudBaseUrl, {
		hostId: issued.data.hostId,
		label: fitLabel(await aoBridge.remotes.computerName()),
		url: options.url,
		token: issued.data.token,
		hostKey: directory.hostKeys ? issued.data.hostKey : undefined,
	});
	return { hostId: issued.data.hostId, added: !listed };
}
