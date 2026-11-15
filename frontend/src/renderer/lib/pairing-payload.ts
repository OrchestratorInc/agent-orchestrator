export type PairingEndpointKind = "lan" | "tailscale" | "tunnel" | "relay";

export type PairingEndpoint = {
	kind: string;
	host: string;
	port: number;
	secure: boolean;
};

export type PairingOffer = {
	v: 2;
	hostId: string;
	name: string;
	platform: string;
	endpoints: PairingEndpoint[];
	token: string;
};

export type PairingOfferInput = {
	endpoints: readonly PairingEndpoint[];
	password: string;
	hostId: string;
	name: string;
	platform: string;
};

export function buildPairingOffer(input: PairingOfferInput): PairingOffer {
	if (input.endpoints.length === 0) {
		throw new Error("cannot build a pairing offer with no endpoints");
	}
	return {
		v: 2,
		hostId: input.hostId,
		name: input.name,
		platform: input.platform,
		endpoints: [...input.endpoints],
		token: input.password,
	};
}

function toBase64Url(json: string): string {
	return btoa(json).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}

export function pairingCodeUrl(offer: PairingOffer, base: string): string {
	return `${base}#${toBase64Url(JSON.stringify(offer))}`;
}
