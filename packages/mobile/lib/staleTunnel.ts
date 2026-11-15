import type { Endpoint } from "./endpoints";

/**
 * Whether a failure to connect is likely a rotated tunnel hostname.
 *
 * A quick tunnel takes a new hostname every time the connector restarts. The
 * phone heals by re-reading the daemon's endpoint list after a successful
 * connect — which it cannot do when the stored tunnel is the dead one and
 * there is no LAN or tailnet address to reach the machine by instead. Measured:
 * the window between a connector restarting and its replacement becoming
 * advertisable is about 34 seconds, and a phone that is away for any of it
 * keeps a hostname that will never answer again.
 *
 * Nothing on the phone can recover from that: learning the new hostname
 * requires reaching the machine, which requires the hostname. What it can do is
 * stop reporting a bare "can't connect" and say the code needs rescanning,
 * which is the one action that actually works.
 *
 * A named tunnel's stable hostname removes the cause, so only an active quick
 * tunnel counts.
 */
export function tunnelMayHaveRotated(
	known: Endpoint[],
	active: { kind?: Endpoint["kind"]; host?: string },
	anyReachable: boolean,
): boolean {
	if (anyReachable) return false;
	// A tunnel elsewhere in the saved candidate list says nothing about why the
	// active LAN or tailnet address failed. This distinction matters when the
	// phone simply leaves Wi-Fi: the LAN endpoint becomes unreachable, but its
	// address did not rotate. Nor does a named tunnel's: one that fails is a
	// machine asleep or offline far more often than one linked to another
	// account, and telling every sleeping machine's phone to rescan is worse.
	return active.kind === "tunnel" && isQuickTunnelHost(active.host ?? "") && known.some((e) => e.kind === "tunnel");
}

/**
 * A Cloudflare quick tunnel: its hostname rotates on every connector restart,
 * and it forwards response bodies in ~128 KB chunks, so the event stream cannot
 * deliver over it. A named tunnel does neither (measured: each 30-byte stream
 * heartbeat arrived on time).
 */
export function isQuickTunnelHost(host: string): boolean {
	return host.toLowerCase().endsWith(".trycloudflare.com");
}
