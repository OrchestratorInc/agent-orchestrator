export function keepDaemonAlive(env: { AO_KEEP_DAEMON?: string }): boolean {
	const raw = env.AO_KEEP_DAEMON?.trim().toLowerCase();
	return raw === "1" || raw === "true" || raw === "yes" || raw === "on";
}

export function shouldLinkOnAttach(owner: string | undefined): boolean {
	return owner === "app";
}
