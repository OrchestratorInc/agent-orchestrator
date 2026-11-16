/**
 * ⌘⇧M and `/model`: opens the chat composer's model menu, on a session's chat and
 * on the new chat page alike. `scope` limits the search to one composer, for a
 * surface that shows more than one. Reports whether a menu was found. Closing the menu hands focus back to the composer
 * (TurnSettingsBar's restoreComposerFocus reads the mark set here).
 */
export function openComposerModelPicker(scope: ParentNode = document): boolean {
	const picker = scope.querySelector<HTMLElement>("[data-shortcut-target='model-picker']:not([disabled])");
	if (!picker) return false;
	// Radix menu triggers open on pointerdown or Enter, never on a synthetic click.
	picker.dataset.shortcutOpened = "";
	picker.focus();
	picker.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true, cancelable: true }));
	return true;
}

/** Dictate (⌥D on macOS): starts or finishes dictation in the focused chat composer, else the first one on screen. */
export function toggleComposerDictation(): void {
	const buttons = Array.from(document.querySelectorAll<HTMLButtonElement>("[data-shortcut-target='dictate']:not([disabled])"));
	const active = document.activeElement;
	(buttons.find((button) => active && button.closest("form")?.contains(active)) ?? buttons[0])?.click();
}
