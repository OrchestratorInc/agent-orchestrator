import { httpBase, machineIdentity, type ServerConfig } from "./config";

/**
 * Credentials a daemon has accepted on /sessions since the connection to it
 * last broke. While a credential is marked, the board poll sends its requests
 * in one batch; otherwise it probes /sessions alone first, so a password the
 * daemon no longer knows costs one failed attempt rather than four.
 *
 * The mark is dropped whenever the connection breaks: a failed poll, an event
 * stream that ends or errors, and a new endpoint race (which every resume from
 * the background and every re-race runs). A daemon restart, the one case where
 * it forgets its retired passwords, always breaks the stream and the poll, so
 * the first tick after it probes alone, also against older daemons.
 */
const accepted = new Map<string, string>();

function credentialOf(cfg: ServerConfig): string {
	return `${httpBase(cfg)}|${cfg.password ?? ""}`;
}

export function markCredentialAccepted(cfg: ServerConfig): void {
	accepted.set(machineIdentity(cfg), credentialOf(cfg));
}

export function isCredentialAccepted(cfg: ServerConfig): boolean {
	return accepted.get(machineIdentity(cfg)) === credentialOf(cfg);
}

/** The connection to this machine broke; probe before batching again. */
export function forgetAcceptedCredential(cfg: ServerConfig): void {
	accepted.delete(machineIdentity(cfg));
}

/** For tests. */
export function resetAcceptedCredentials(): void {
	accepted.clear();
}
