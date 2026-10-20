import { buildPairingOffer, pairingCodeUrl } from "../../lib/pairing-payload";

/**
 * Decoy payloads for the placeholder shown while remote access comes up.
 *
 * The placeholder is a real QR, not a drawing of one. Standing the tunnel up
 * takes ~30s, and the earlier attempt — a hand-drawn grid of squares — could
 * never resolve into the finished code: it had its own module size, its own
 * corner markers and no logo, so the moment the real QR arrived one image was
 * swapped for a visibly different one.
 *
 * Encoding a payload of the same *shape and length* as a genuine offer gives
 * the same QR version, so the placeholder and the real code share a module
 * count, a dot size, corner markers and a logo cutout.
 * They differ only in which modules are dark, which is exactly what lets one
 * dissolve into the other.
 *
 * Nothing here is scannable in any useful sense: the hosts are reserved
 * documentation addresses and the token is decoy bytes.
 */

/** Matches the base ConnectMobileContent builds real codes on. */
export const PAIRING_LINK_BASE_FOR_SCRAMBLE = "aomobile://pair";

/** Deterministic, so a re-render reproduces the stack instead of reshuffling. */
function pseudoRandom(seed: number): () => number {
	let s = seed * 2654435761;
	return () => {
		s = (s * 1664525 + 1013904223) % 4294967296;
		return s / 4294967296;
	};
}

function hex(next: () => number, length: number): string {
	let out = "";
	while (out.length < length) out += Math.floor(next() * 16).toString(16);
	return out.slice(0, length);
}

/** Field lengths of the real offer the decoy stands in for. */
export interface ScrambleShape {
	/** Length of the daemon's host id (`h_` + uuid on a real machine). */
	hostIdLength: number;
	/** Length of the offer's `name` (the advertised host). */
	nameLength: number;
	/** Length of the connection password. */
	tokenLength: number;
}

/** A real machine: `h_` + uuid, a LAN address, a 16-char password. */
const DEFAULT_SHAPE: ScrambleShape = { hostIdLength: 38, nameLength: 13, tokenLength: 16 };

/**
 * One decoy offer. A LAN address plus a tunnel hostname is the everyday offer,
 * and the lengths of its variable fields are taken from the real one, because
 * the payload length is what sets the QR version.
 */
function scrambleCode(index: number, shape: ScrambleShape): string {
	const next = pseudoRandom(index + 1);
	const offer = buildPairingOffer({
		// 192.0.2.0/24 is reserved for documentation, so this cannot collide
		// with anything real on a user's network.
		endpoints: [
			{ kind: "lan", host: `192.0.2.${1 + Math.floor(next() * 250)}`, port: 3011, secure: false },
			{ kind: "tunnel", host: `${hex(next, 12)}-${hex(next, 10)}-${hex(next, 6)}.invalid`, port: 443, secure: true },
		],
		password: hex(next, shape.tokenLength),
		hostId: `h_${hex(next, Math.max(0, shape.hostIdLength - 2))}`,
		name: hex(next, shape.nameLength),
		platform: "",
	});
	return pairingCodeUrl(offer, PAIRING_LINK_BASE_FOR_SCRAMBLE);
}

export function scramblePairingCodes(count: number, shape: Partial<ScrambleShape> = {}): string[] {
	const full = { ...DEFAULT_SHAPE, ...shape };
	return Array.from({ length: count }, (_, i) => scrambleCode(i, full));
}
