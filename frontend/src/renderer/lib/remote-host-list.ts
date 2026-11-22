import { AoError } from "../../../../packages/shared/telemetry/ao-error";
import { aoBridge } from "./bridge";

export async function listRemoteHosts() {
	const result = await aoBridge.remotes.list();
	if (Array.isArray(result)) return result;
	throw new AoError(result.code, "Error invoking remote method 'remotes:list': " + result.message);
}
