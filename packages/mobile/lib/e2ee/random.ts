let source: ((count: number) => Uint8Array) | undefined;

/** expo-crypto's random bytes, loaded on first use so that modules importing the transport still load under node. */
export async function loadRandomBytes(): Promise<(count: number) => Uint8Array> {
	source ??= (await import("expo-crypto")).getRandomBytes;
	return source;
}
