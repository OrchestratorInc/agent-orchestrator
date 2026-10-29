import { configForEndpoint } from "./connect";
import type { ServerConfig } from "./config";
import type { Endpoint } from "./endpoints";
import type { Host } from "./hosts";
import { machineAppearance, type MachineAppearance } from "./machineAppearance";
import { isLegacyPairingCode, parsePairingCode } from "./pairingCode";
import type { RaceOutcome } from "./race";

type SavedHost = Pick<Host, "id" | "name" | "appearance">;

export type PairSuccess = {
	ok: true;
	config: ServerConfig;
	host: Host;
	/** How the code related to what the phone already had saved. */
	kind: "added" | "reconnected" | "existing";
	/** The machine the user set out to reconnect, when the code named another. */
	instead?: Pick<Host, "id" | "name">;
};

export type PairResult =
	| PairSuccess
	| {
			ok: false;
			reason: "not-ao-qr" | "outdated-desktop" | "no-candidates" | "none-reachable" | "incompatible" | "cancelled" | "name-required";
		}
	| {
			ok: false;
			reason: "verify-failed";
			/** The endpoint that won the race: verification reached it, so it is
			 * the address to name, not whichever came first in the code. */
			endpoint: Endpoint;
			/** What the daemon answered, or undefined when it never did. Kept so a
			 * rejected password is not reported as an unreachable machine. */
			status?: number;
		};

export type PairDeps = {
	/** Probes the code's endpoints and returns the best that answers. */
	race: (endpoints: Endpoint[], expectedHostId: string) => Promise<RaceOutcome>;
	/** Proves the winning endpoint actually serves us, with our credential. */
	verify: (config: ServerConfig) => Promise<void>;
	/** New pairings are not persisted until the user confirms a readable name.
	 * The icon is offered alongside it, starting from the machine's default. */
	chooseName: (suggested: string, appearance: MachineAppearance, hostId: string) => Promise<MachineNameChoice | null>;
	/** The saved pairing for a machine, if this phone has one. */
	findHost: (id: string) => Promise<SavedHost | null>;
	saveHost: (host: Host) => Promise<void>;
	setActiveHost: (id: string) => Promise<void>;
};

export type MachineNameChoice = { name: string; appearance: MachineAppearance };

export type PairOptions = {
	/** The saved pairing the user chose to reconnect. A code for any other
	 * machine still pairs, as that machine, and leaves this one untouched. */
	repairHost?: SavedHost;
};

/**
 * Turns a scanned or pasted pairing code into a stored machine.
 *
 * Lives here rather than in the pair screen because `packages/mobile` has no
 * screen tests: logic left in a component is unreachable by the suite.
 *
 * Order matters. The endpoints are raced first, so what gets stored is a
 * machine we have actually reached, and verification runs before anything is
 * written — a pairing saved without being proven leaves the app looking
 * connected to something it has never spoken to.
 */
export async function pairFromCode(raw: string, deps: PairDeps, options: PairOptions = {}): Promise<PairResult> {
	const offer = parsePairingCode(raw);
	// Distinguished from unrecognised input so the reason does not depend on
	// which caller reached here: a v1 code is a real AO code from a desktop too
	// old to pair with, and saying so is more useful than "not a pairing code".
	if (!offer) {
		return { ok: false, reason: isLegacyPairingCode(raw) ? "outdated-desktop" : "not-ao-qr" };
	}
	const outcome = await deps.race(offer.endpoints, offer.hostId);
	if (!outcome.ok) return { ok: false, reason: outcome.reason };

	const config = configForEndpoint(outcome.endpoint, offer.token, offer.hostId || outcome.hostId);
	try {
		await deps.verify(config);
	} catch (error) {
		const status = typeof error === "object" && error !== null && typeof (error as { status?: unknown }).status === "number"
			? (error as { status: number }).status
			: undefined;
		return { ok: false, reason: "verify-failed", endpoint: outcome.endpoint, ...(status === undefined ? {} : { status }) };
	}

	const hostId = offer.hostId || outcome.hostId;
	// The pairing to refresh is chosen by the identity the code carries, never
	// by where the scan started: a code for another machine pointed at the
	// reconnect screen is still what the user wants connected now, and saving
	// it under the machine they started from would hand that entry B's token.
	// Compared with the id being saved, not the code's own field: an id-less
	// legacy pairing and an id-less code would otherwise "match" on "" while
	// the machine is saved under the id it reported (#7346).
	const repair = options.repairHost;
	const repairing = repair !== undefined && repair.id === hostId;
	const saved = repairing ? repair : hostId ? await deps.findHost(hostId) : null;
	let name = saved?.name.trim() ?? "";
	let appearance = saved?.appearance;
	if (!name) {
		const suggested = offer.name || outcome.endpoint.host;
		const chosen = await deps.chooseName(suggested, appearance ?? machineAppearance({ id: hostId, name: suggested }), hostId);
		if (chosen === null) return { ok: false, reason: "cancelled" };
		name = chosen.name.trim();
		if (!name) return { ok: false, reason: "name-required" };
		appearance = chosen.appearance;
	}

	const host: Host = {
		// A v1 code carries no identity, but we have just spoken to the machine
		// and it told us who it is, so adopt that now rather than leaving it
		// unverified until the next connect.
		id: hostId,
		name,
		...(appearance ? { appearance } : {}),
		platform: offer.platform,
		endpoints: offer.endpoints,
		token: offer.token,
		lastConnected: Date.now(),
	};
	await deps.saveHost(host);
	await deps.setActiveHost(host.id);

	return {
		ok: true,
		config,
		host,
		kind: repairing ? "reconnected" : saved ? "existing" : "added",
		...(repair && !repairing ? { instead: { id: repair.id, name: repair.name } } : {}),
	};
}

export type PairedNotice = { title: string; message?: string };

/**
 * What to tell the user once a scan has paired, or null when nothing needs
 * saying. A machine they have just named needs no confirmation; the other
 * outcomes otherwise look identical to it, since every one closes the scanner.
 */
export function pairedNotice(result: PairSuccess): PairedNotice | null {
	const name = result.host.name;
	if (result.instead) {
		return { title: `Connected to ${name}`, message: `That code was for ${name}. ${result.instead.name} still needs a new code.` };
	}
	if (result.kind === "reconnected") return { title: `Reconnected ${name}` };
	// Not "switched": the machine may already have been the active one.
	if (result.kind === "existing") return { title: `${name} is already paired`, message: "Connection details refreshed." };
	return null;
}
