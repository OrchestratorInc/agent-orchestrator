import { useCallback, useEffect, useRef, useState } from "react";
import { findNodeHandle, NativeEventEmitter, NativeModules, type LayoutChangeEvent } from "react-native";
import type { ComposerImageAsset, PasteOverflow } from "./composerImages";

type PastedImage = { mimeType?: string; bytes?: number; data?: string };

/** Maps the native paste event onto picker assets; images without data were too large or unreadable. */
export function pastedImageAssets(tag: number, images: readonly PastedImage[], now = Date.now()): ComposerImageAsset[] {
	return images.map((image, index) => ({
		uri: `paste-${tag}-${now}-${index}`,
		fileName: "Pasted image",
		mimeType: image.mimeType,
		fileSize: image.bytes || undefined,
		base64: image.data,
	}));
}

/**
 * Lets the composer's own TextInput take images from Paste (and, on Android, the
 * keyboard's image insertion). Attach the returned handler to the field's onLayout.
 */
export function useImagePaste(onPaste: ((assets: ComposerImageAsset[], overflow?: PasteOverflow) => void) | null) {
	const [tag, setTag] = useState<number>();
	const callback = useRef(onPaste);
	callback.current = onPaste;
	const enabled = Boolean(onPaste);
	useEffect(() => {
		const native = NativeModules.AOImagePaste;
		if (!native || !tag || !enabled) return;
		const subscription = new NativeEventEmitter(native).addListener("AOImagePaste", (event: { tag: number; images?: PastedImage[]; overflow?: string }) => {
			// Native stops at the composer's batch limits and says which one it hit.
			const overflow = event.overflow === "count" || event.overflow === "size" ? event.overflow : undefined;
			if (event.tag === tag && Array.isArray(event.images) && event.images.length) callback.current?.(pastedImageAssets(tag, event.images), overflow);
		});
		native.attach(tag);
		return () => { subscription.remove(); native.detach(tag); };
	}, [tag, enabled]);
	return useCallback((event: LayoutChangeEvent) => {
		// RN exposes a Fabric host instance here; its findNodeHandle typings still
		// describe the older component ref, though the runtime accepts both.
		setTag(findNodeHandle(event.currentTarget as unknown as Parameters<typeof findNodeHandle>[0]) ?? undefined);
	}, []);
}
