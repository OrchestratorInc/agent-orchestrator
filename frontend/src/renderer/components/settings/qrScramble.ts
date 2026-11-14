import { buildPairingOffer, pairingCodeUrl } from "../../lib/pairing-payload";

export const PAIRING_LINK_BASE_FOR_SCRAMBLE = "aomobile://pair";

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

export interface ScrambleShape {
	hostIdLength: number;
	nameLength: number;
	tokenLength: number;
}

const DEFAULT_SHAPE: ScrambleShape = { hostIdLength: 38, nameLength: 13, tokenLength: 16 };

function scrambleCode(index: number, shape: ScrambleShape): string {
	const next = pseudoRandom(index + 1);
	const offer = buildPairingOffer({
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
