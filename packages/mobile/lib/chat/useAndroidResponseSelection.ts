import { useCallback, useEffect, useRef, useState } from "react";
import { DeviceEventEmitter, findNodeHandle, NativeModules, Platform, type LayoutChangeEvent } from "react-native";

/** Attach to RN's existing selectable TextView, preserving its text/link layout. */
export function useAndroidResponseSelection(onAddToChat: ((text: string) => void) | null) {
	const [tag, setTag] = useState<number>();
	const callback = useRef(onAddToChat);
	callback.current = onAddToChat;
	const enabled = Boolean(onAddToChat);
	useEffect(() => {
		const native = NativeModules.AOResponseSelection;
		if (Platform.OS !== "android" || !native || !tag || !enabled) return;
		const subscription = DeviceEventEmitter.addListener("AOAddSelectedTextToChat", (event: { tag: number; text: string }) => {
			if (event.tag === tag && typeof event.text === "string" && event.text.length) callback.current?.(event.text);
		});
		native.attach(tag);
		return () => { subscription.remove(); native.detach(tag); };
	}, [tag, enabled]);
	return useCallback((event: LayoutChangeEvent) => {
		// RN exposes a Fabric host instance here; its findNodeHandle typings still
		// describe the older component ref, though the runtime accepts both.
		if (Platform.OS === "android") setTag(findNodeHandle(event.currentTarget as unknown as Parameters<typeof findNodeHandle>[0]) ?? undefined);
	}, []);
}
