import { authHeaders, type ServerConfig } from "./config";

/**
 * An image on the machine for React Native's Image. For a machine with a pinned
 * key the source carries no credential, so the native loader cannot send it in
 * plaintext; useHostImage reads it through the encrypted channel instead.
 */
export function hostImageSource(cfg: ServerConfig, uri: string): { uri: string; headers: Record<string, string> } {
	return cfg.hostKey ? Object.assign({ uri, headers: {} }, { via: { config: cfg, headers: authHeaders(cfg) } }) : { uri, headers: authHeaders(cfg) };
}
