/** Validation shared by the forms that take a machine's address by hand. */

export function isValidPort(port: string): boolean {
	return /^\d+$/.test(port) && Number(port) >= 1 && Number(port) <= 65535;
}

export const PORT_ERROR = "Enter a port from 1 to 65535.";

/**
 * The port's error, said only once something is typed: an empty port just
 * keeps the form from submitting.
 */
export function portError(port: string): string | undefined {
	return port.trim() && !isValidPort(port) ? PORT_ERROR : undefined;
}

export type ManualAddress =
	| { ok: true; host: string; port: string; secure: boolean }
	| { ok: false; incomplete: true }
	| { ok: false; incomplete?: false; error: string };

export function resolveManualAddress(hostInput: string, portInput: string, secure: boolean | undefined): ManualAddress {
	const tls = secure === true;
	let rest = hostInput.trim();
	const port = portInput.trim();

	const scheme = rest.match(/^([a-z][a-z0-9+.-]*):\/\//i);
	if (scheme) {
		rest = rest.slice(scheme[0].length);
		const name = scheme[1].toLowerCase();
		if (name === "https" && !tls) return { ok: false, error: "The host starts with https://. Turn on Use TLS, or remove https://." };
		if (name === "http" && tls) return { ok: false, error: "The host starts with http://. Turn off Use TLS, or remove http://." };
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

	const resolved = port || embedded || (tls ? "443" : "");
	if (!resolved) return { ok: false, incomplete: true };
	return { ok: true, host, port: String(Number(resolved)), secure: tls };
}

export function manualAddressError(hostInput: string, portInput: string, secure: boolean | undefined): string | undefined {
	if (!hostInput.trim()) return portError(portInput);
	const address = resolveManualAddress(hostInput, portInput, secure);
	return !address.ok && !address.incomplete ? address.error : undefined;
}
