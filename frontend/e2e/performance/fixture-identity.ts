import type { ConversationSnapshot } from "../../src/renderer/types/conversation";

/** Compute outside the measured interval. Output contains no message bodies. */
export async function fixtureIdentity(snapshot: ConversationSnapshot, apiCompactOption: boolean | null = null) {
	const bytes = new TextEncoder().encode(JSON.stringify(snapshot));
	const digest = await crypto.subtle.digest("SHA-256", bytes);
	const activities = snapshot.items.filter((item) => item.kind === "activity");
	return {
		checksumAlgorithm: "sha256-json-utf8",
		fixtureChecksum: Array.from(new Uint8Array(digest), (byte) => byte.toString(16).padStart(2, "0")).join(""),
		serializedBytes: bytes.byteLength,
		turnCount: snapshot.turns.length,
		itemCount: snapshot.items.length,
		toolRowCount: activities.length,
		// These are observed row flags, not proof of an API request's compact option.
		compactFlags: {
			apiCompactOption,
			detailOmittedRows: activities.filter((item) => (item as unknown as Record<string, unknown>).detailOmitted === true).length,
		},
	};
}
