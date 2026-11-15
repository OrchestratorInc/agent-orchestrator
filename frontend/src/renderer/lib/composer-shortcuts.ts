export function openComposerModelPicker(): void {
	const picker = document.querySelector<HTMLElement>("[data-shortcut-target='model-picker']:not([disabled])");
	if (!picker) return;
	picker.dataset.shortcutOpened = "";
	picker.focus();
	picker.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true, cancelable: true }));
}

export function toggleComposerDictation(): void {
	const buttons = Array.from(document.querySelectorAll<HTMLButtonElement>("[data-shortcut-target='dictate']:not([disabled])"));
	const active = document.activeElement;
	(buttons.find((button) => active && button.closest("form")?.contains(active)) ?? buttons[0])?.click();
}
