import { useEffect, type RefObject } from "react";
import { Keyboard, type TextInput } from "react-native";
import type { EffortSliderSwitch } from "./useEffortSlider";

/**
 * Brings the keyboard up with a composer picker (model, effort, permissions)
 * opened while it is down: the field takes the focus as the picker opens, so
 * every picker opens over the keys, in one place, whatever the keyboard was
 * doing. The chat's composer and New task's both use it.
 */
export function useKeysWithPickers(field: RefObject<TextInput | null>, switches: readonly EffortSliderSwitch[]): void {
	useEffect(() => {
		const unsubscribe = switches.map((picker) => {
			let wasOpen = picker.isOpen();
			return picker.subscribe(() => {
				const open = picker.isOpen();
				if (open && !wasOpen && !Keyboard.isVisible()) field.current?.focus();
				wasOpen = open;
			});
		});
		return () => { for (const off of unsubscribe) off(); };
	}, [field, ...switches]); // eslint-disable-line react-hooks/exhaustive-deps
}
