import { useCallback, useEffect, useRef, useSyncExternalStore, type Ref } from "react";
import { TextInput, type NativeSyntheticEvent, type TextInputProps, type TextInputSelectionChangeEventData } from "react-native";
import { textSelectionColors } from "./textSelection";
import { useTheme } from "./ThemeProvider";
import type { EffortSliderSwitch } from "./useEffortSlider";

/** Whether any of a composer's overlays is open, re-rendering the caller only when that changes. */
function useAnyOverlayOpen(overlays: readonly EffortSliderSwitch[]): boolean {
	const subscribe = useCallback((listener: () => void) => {
		const offs = overlays.map((overlay) => overlay.subscribe(listener));
		return () => { for (const off of offs) off(); };
	}, [overlays]);
	const open = useCallback(() => overlays.some((overlay) => overlay.isOpen()), [overlays]);
	return useSyncExternalStore(subscribe, open);
}

/**
 * A composer's field that keeps its caret out of the way of the composer's
 * overlays (the effort slider, the permission picker). The field stays
 * focused under them, keyboard up, but its caret and the handles Android draws
 * under it are in a window of their own above the app, which no overlay can
 * cover: the blue drop sat on top of the slider. While an overlay is open the
 * caret is hidden, which takes the drop with it, and a selection is collapsed
 * to its end, which takes its two handles and its menu. The caret comes back
 * when the overlay closes.
 *
 * A key typed while an overlay is up over the keyboard puts it away, and the
 * key still types: the keys are under it, and the app hears of a key only as
 * a change to the text.
 *
 * Its own component, so only the field re-renders as an overlay opens and
 * closes, not the composer around it (see EffortSliderSwitch).
 */
export function ComposerField({ overlays, caretHidden, onSelectionChange, onChangeText, ref, ...props }: TextInputProps & {
	overlays: readonly EffortSliderSwitch[];
	ref?: Ref<TextInput>;
}) {
	const t = useTheme();
	const covered = useAnyOverlayOpen(overlays);
	const field = useRef<TextInput | null>(null);
	const selection = useRef({ start: 0, end: 0 });
	const attach = useCallback((instance: TextInput | null) => {
		field.current = instance;
		if (typeof ref === "function") ref(instance);
		else if (ref) (ref as { current: TextInput | null }).current = instance;
	}, [ref]);
	useEffect(() => {
		if (!covered) return;
		const { start, end } = selection.current;
		if (start !== end) field.current?.setSelection?.(end, end);
	}, [covered]);
	return (
		<TextInput
			{...textSelectionColors(t)}
			{...props}
			ref={attach}
			caretHidden={covered || caretHidden}
			onChangeText={(value: string) => {
				for (const overlay of overlays) overlay.setOpen(false);
				onChangeText?.(value);
			}}
			onSelectionChange={(event: NativeSyntheticEvent<TextInputSelectionChangeEventData>) => {
				selection.current = event.nativeEvent.selection;
				onSelectionChange?.(event);
			}}
		/>
	);
}
