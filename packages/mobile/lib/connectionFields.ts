/** Validation shared by the forms that take a machine's address by hand. */
import { isLocalNetworkHost, isTailscaleHost } from "./connectionError";
import type { EndpointKind } from "./endpoints";
import { isQuickTunnelHost } from "./staleTunnel";

export function isValidPort(port: string): boolean {
	return /^\d+$/.test(port) && Number(port) >= 1 && Number(port) <= 65535;
}

export const PORT_ERROR = "Enter a port from 1 to 65535.";

/** The Connect Mobile listener's port when nothing else is in the way. */
const BRIDGE_PORT = "3011";
const TLS_PORT = "443";

/**
 * The port's error, said only once something is typed: an empty port just
 * keeps the form from submitting.
 */
export function portError(port: string): string | undefined {
	return port.trim() && !isValidPort(port) ? PORT_ERROR : undefined;
}

/**
 * A Cloudflare tunnel in front of a machine: a quick tunnel's name, or AO
 * Cloud's fixed address, h-<20 hex>.<zone>. Both are only ever served over
 * TLS on 443.
 */
export function isTunnelHost(host: string): boolean {
	const name = host.trim().toLowerCase();
	return isQuickTunnelHost(name) || /^h-[0-9a-f]{20}\.[^.]+\.[^.]+/.test(name);
}

/** Loopback, link-local and unique-local IPv6, bracketed or not. */
function isPrivateIPv6(host: string): boolean {
	const h = host.replace(/^\[|\]$/g, "").toLowerCase();
	if (!h.includes(":")) return false;
	return h === "::1" || /^fe[89ab][0-9a-f]?:/.test(h) || /^f[cd][0-9a-f]{0,2}:/.test(h);
}

/** Any spelling an address parser reads as an IPv4 number: 134744072, 0x8080808, 10.1, 010.0.0.1. */
const IPV4_LITERAL = /^(0x[0-9a-f]*|\d+)(\.(0x[0-9a-f]*|\d+)){0,3}\.?$/i;
/** Four decimal octets without leading zeros: the one spelling every parser reads alike. */
const CANONICAL_IPV4 = /^(25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)(\.(25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)){3}$/;

/**
 * An address that only means something on the network the phone is on: a
 * private or tailnet IP, a .local name, or a bare machine name. Nothing in
 * front of the daemon gives these a certificate, so they speak plain HTTP.
 *
 * A number is never a machine name: 134744072 and 0x8080808 are 8.8.8.8, and
 * leading zeros are octal to some parsers, so an IPv4 literal is private only
 * as a canonical dotted quad.
 */
function isPrivateHost(host: string): boolean {
	const h = host.trim().toLowerCase();
	if (IPV4_LITERAL.test(h)) return CANONICAL_IPV4.test(h) && (isLocalNetworkHost(h) || isTailscaleHost(h));
	if (isLocalNetworkHost(h) || isTailscaleHost(h) || isPrivateIPv6(h)) return true;
	return !h.includes(".") && !h.includes(":") && !h.startsWith("[");
}

/** What kind of route an address is, from its name: what the endpoint race ranks it by. */
export function endpointKindForHost(host: string, secure: boolean): EndpointKind {
	const h = host.trim().toLowerCase();
	if (h.endsWith(".ts.net") || isTailscaleHost(h)) return "tailscale";
	if (secure && isTunnelHost(h)) return "tunnel";
	return "lan";
}

/** The route a machine is saved with, which an unchanged address keeps. */
export type SavedRoute = { host: string; port: number; secure: boolean };

export type ManualAddress =
	| { ok: true; host: string; port: string; secure: boolean }
	| { ok: false; incomplete: true }
	| { ok: false; incomplete?: false; error: string };

/**
 * Reads a hand-typed address, deciding whether it speaks TLS.
 *
 * Whether to use TLS is never asked. A typed scheme decides it; otherwise
 * only an address that exists on the local network or tailnet alone is plain
 * HTTP, and every other one is TLS. That includes custom domains and public
 * IPs: a plain-HTTP fallback there would let anyone on the path block TLS and
 * collect the password or pairing code the next request carries, so a public
 * name over plain HTTP needs http:// typed. An empty port means 443 for TLS
 * and the Connect Mobile port otherwise.
 *
 * `saved` is the route an edited machine already has. An address that still
 * matches it keeps its TLS choice: a working pairing is not guessed again.
 */
export function resolveManualAddress(hostInput: string, portInput: string, saved?: SavedRoute): ManualAddress {
	let rest = hostInput.trim();
	const port = portInput.trim();

	// An untouched saved route is kept exactly as saved, before any parsing or
	// check below: one saved before they existed (a legacy host may even end in
	// a slash), or advertised by the machine, must never block a rename.
	if (saved && rest === saved.host && (!port || (isValidPort(port) && Number(port) === saved.port))) {
		return { ok: true, host: saved.host, port: String(saved.port), secure: saved.secure };
	}

	let typedTls: boolean | undefined;
	const scheme = rest.match(/^([a-z][a-z0-9+.-]*):\/\//i);
	if (scheme) {
		rest = rest.slice(scheme[0].length);
		const name = scheme[1].toLowerCase();
		if (name === "https" || name === "wss") typedTls = true;
		if (name === "http" || name === "ws") typedTls = false;
	}
	rest = rest.replace(/\/+$/, "");
	if (!rest) return { ok: false, incomplete: true };

	let host = rest;
	let embedded = "";
	const bracketed = rest.match(/^(\[[^\]]+\])(?::(.*))?$/);
	if (bracketed) {
		host = bracketed[1];
		if (bracketed[2] !== undefined) embedded = bracketed[2] || "-";
	} else if (rest.indexOf(":") !== -1 && rest.indexOf(":") === rest.lastIndexOf(":")) {
		const at = rest.indexOf(":");
		host = rest.slice(0, at);
		embedded = rest.slice(at + 1) || "-";
		if (!host) return { ok: false, error: "Enter a host before the port." };
	}

	if (embedded && !isValidPort(embedded)) return { ok: false, error: "The port in the host must be from 1 to 65535." };
	if (port && !isValidPort(port)) return { ok: false, error: PORT_ERROR };
	if (port && embedded && Number(port) !== Number(embedded)) {
		return { ok: false, error: `The host says port ${Number(embedded)} but Port is ${Number(port)}. Make them match, or clear one.` };
	}
	const typedPort = port || embedded ? String(Number(port || embedded)) : "";
	const resolved = (secure: boolean): ManualAddress => ({ ok: true, host, port: typedPort || (secure ? TLS_PORT : BRIDGE_PORT), secure });

	// Only a bare name or IP: TLS is decided from the name, so a path, query or
	// user-info must not let "public.example/x.local" pass as a local address.
	if (!/^[a-z0-9._-]+$/i.test(host) && !/^(\[[0-9a-f:.%]+\]|[0-9a-f:.%]+)$/i.test(host)) {
		return { ok: false, error: "Enter only a host name or IP address, without a path." };
	}
	// The saved route again, now that the host is a valid name, whose case does
	// not matter, or with its port written into Host.
	if (typedTls === undefined && saved && saved.host.toLowerCase() === host.toLowerCase() && (!typedPort || Number(typedPort) === saved.port)) {
		return { ok: true, host, port: String(saved.port), secure: saved.secure };
	}
	if (typedTls !== undefined) return resolved(typedTls);
	if (isTunnelHost(host) && typedPort && typedPort !== TLS_PORT) {
		return { ok: false, error: "Tunnel addresses use port 443. Clear Port or enter 443." };
	}
	return resolved(typedPort === TLS_PORT || !isPrivateHost(host));
}

export function manualAddressError(hostInput: string, portInput: string, saved?: SavedRoute): string | undefined {
	if (!hostInput.trim()) return portError(portInput);
	const address = resolveManualAddress(hostInput, portInput, saved);
	return !address.ok && !address.incomplete ? address.error : undefined;
}
