import type { Endpoint, EndpointKind } from "./endpoints";

/**
 * Fold a freshly advertised endpoint list into the stored one.
 *
 * The daemon's list is authoritative for every kind it mentions — a rotated
 * tunnel hostname or a changed DHCP lease must replace the old value, which is
 * what makes a pairing self-healing. The one exception is a LAN name (see
 * isName below), which outlives the addresses the daemon lists.
 *
 * But a kind the daemon omits entirely is treated as *unknown*, not as
 * *absent*. Measured on device: a quick tunnel takes ~34s to restart and
 * settle, and throughout that window the daemon advertises no tunnel at all,
 * so a refresh landing inside it used to erase the tunnel from the stored
 * list — and nothing refreshes again while the current endpoint answers.
 *
 * Be clear about what this does and does not buy. The daemon drops the tunnel
 * only while the connector process is down (TunnelRuntime.Endpoint), and a
 * quick tunnel that restarts always comes back on a *different* hostname, so
 * the entry preserved here is in practice a dead one. It does not restore a
 * route home after a rotation; nothing on the phone can, because a phone with
 * no working path cannot learn the new hostname. That needs a stable hostname
 * — see docs/adr/0004-cloudflare-tunnel-for-remote-mobile-access.md.
 *
 * What it does buy is that the list stays structurally correct, at the cost of
 * one failed probe in a race that runs its candidates concurrently. A kind is
 * never silently lost because of when a refresh happened to land, and the
 * entry is replaced the moment the daemon advertises that kind again.
 */
export function mergeEndpoints(stored: Endpoint[], fresh: Endpoint[]): Endpoint[] {
	const refreshed = new Set<EndpointKind>(fresh.map((e) => e.kind));
	const freshHosts = new Set(fresh.map((e) => e.host.toLowerCase()));
	return [...fresh, ...stored.filter((e) =>
		!refreshed.has(e.kind) || (e.kind === "lan" && isName(e.host) && !freshHosts.has(e.host.toLowerCase())))];
}

/**
 * A LAN endpoint named rather than numbered — the Mac's mDNS name
 * (studio.local) or a name typed by hand — outlives the address it resolves
 * to, so the advertised list never replaces it. The daemon advertises the
 * machine's current IPs, which a DHCP lease change makes dead; without a kept
 * name, a phone with no other usable route (an iPhone cannot use plain-HTTP
 * Tailscale) never reaches the machine again to learn the new address. A name
 * the refresh lists itself, on whatever port, is taken from the refresh.
 */
function isName(host: string): boolean {
	return !/^\d{1,3}(\.\d{1,3}){3}$/.test(host) && !host.includes(":");
}
