import * as Crypto from "expo-crypto";

/**
 * base64 -> base64url: `+` and `/` are not safe in a URL query parameter, and
 * the `=` padding is omitted by the spec.
 */
export function toBase64Url(input: string): string {
	return input.replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/g, "");
}

/**
 * One PKCE pair.
 *
 * The verifier is a high-entropy random string the app keeps; the challenge is
 * its SHA-256, base64url encoded, and is the only half that travels in the
 * authorization URL. `buildWorkOSAuthUrl` sends `code_challenge_method: S256`,
 * so this must be a real digest — sending the verifier itself would defeat the
 * exchange's proof entirely.
 */
export async function createPkcePair(): Promise<{ verifier: string; challenge: string }> {
	const bytes = await Crypto.getRandomBytesAsync(32);
	const verifier = toBase64Url(
		// btoa over the raw bytes; RN provides btoa globally (see lib/mux.ts).
		btoa(String.fromCharCode(...bytes)),
	);
	const digest = await Crypto.digestStringAsync(Crypto.CryptoDigestAlgorithm.SHA256, verifier, {
		encoding: Crypto.CryptoEncoding.BASE64,
	});
	return { verifier, challenge: toBase64Url(digest) };
}
