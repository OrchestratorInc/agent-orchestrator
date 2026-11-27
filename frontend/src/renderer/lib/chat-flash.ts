/**
 * The one "flash the message we scrolled to" treatment: opening a mention and
 * a comment's "Jump to message" both tint the message for a moment, so the eye
 * lands on it (styles.css, `[data-chat-flash]`).
 */

/** How long the tint lasts; the CSS animation runs for the same time. */
export const CHAT_FLASH_MS = 1600;

/** The surface the tint paints: a message's bubble (or a tagged message's dashed box) when it has one, else the message. */
const BUBBLE_SELECTOR = ".cursor-chat-human-message, [data-chat-focus-bubble], [data-chat-aside-box]";

let current: { surface: HTMLElement; timer: number } | undefined;

/** The element a flash of `target` tints. */
export function chatFlashSurface(target: HTMLElement): HTMLElement {
	return target.querySelector<HTMLElement>(BUBBLE_SELECTOR) ?? target;
}

/** Stops the flash on screen, if any. */
export function clearChatFlash(): void {
	if (!current) return;
	window.clearTimeout(current.timer);
	delete current.surface.dataset.chatFlash;
	current = undefined;
}

/**
 * Tints the message `target` for CHAT_FLASH_MS. Only one message flashes at a
 * time: a new flash ends the one before it, and replaying on the same message
 * restarts its animation.
 */
export function flashChatMessage(target: HTMLElement): void {
	clearChatFlash();
	const surface = chatFlashSurface(target);
	// Reading layout between removing and setting the attribute restarts the animation.
	void surface.offsetWidth;
	surface.dataset.chatFlash = "";
	const timer = window.setTimeout(() => {
		if (current?.surface !== surface) return;
		delete surface.dataset.chatFlash;
		current = undefined;
	}, CHAT_FLASH_MS);
	current = { surface, timer };
}
