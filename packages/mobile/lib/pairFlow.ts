import { configForEndpoint } from "./connect";
import type { ServerConfig } from "./config";
import type { Endpoint } from "./endpoints";
import type { Host } from "./hosts";
import { isLegacyPairingCode, parsePairingCode } from "./pairingCode";
import type { RaceOutcome } from "./race";

export type PairResult =
	| { ok: true; config: ServerConfig; host: Host }
	| {
			ok: false;
			reason: "not-ao-qr" | "outdated-desktop" | "no-candidates" | "none-reachable" | "incompatible" | "wrong-machine" | "cancelled" | "name-required";
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
	/** New pairings are not persisted until the user confirms a readable name. */
	chooseName: (suggested: string) => Promise<string | null>;
	saveHost: (host: Host) => Promise<void>;
	setActiveHost: (id: string) => Promise<void>;
};

export type PairOptions = {
	/** Existing identity and name when refreshing one saved pairing. */
	repairHost?: Pick<Host, "id" | "name" | "appearance">;
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
	if (options.repairHost && offer.hostId !== options.repairHost.id) {
		return { ok: false, reason: "wrong-machine" };
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

	let name = options.repairHost?.name.trim() ?? "";
	if (!name) {
		const chosen = await deps.chooseName(offer.name || outcome.endpoint.host);
		if (chosen === null) return { ok: false, reason: "cancelled" };
		name = chosen.trim();
		if (!name) return { ok: false, reason: "name-required" };
	}

	const host: Host = {
		// A v1 code carries no identity, but we have just spoken to the machine
		// and it told us who it is, so adopt that now rather than leaving it
		// unverified until the next connect.
		id: offer.hostId || outcome.hostId,
		name,
		...(options.repairHost?.appearance ? { appearance: options.repairHost.appearance } : {}),
		platform: offer.platform,
		endpoints: offer.endpoints,
		token: offer.token,
		lastConnected: Date.now(),
	};
	await deps.saveHost(host);
	await deps.setActiveHost(host.id);

	return { ok: true, config, host };
}
