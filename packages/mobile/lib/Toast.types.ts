/**
 * A transient message: "Path copied.", "Comment added to the review." It
 * confirms what the user just did, or says why it only partly worked, and
 * leaves on its own.
 *
 * Android draws it as an M3 Snackbar; iOS, which has no system equivalent,
 * as a small HUD that fades in and out. Both are announced to screen readers.
 */

/** How long a message stays: `short` for a confirmation, `long` for a sentence that needs reading. */
export type ToastDuration = "short" | "long";

/**
 * What the message says about the action that raised it. iOS leads with a
 * glyph for `success` and `warning`; an M3 snackbar is text only.
 */
export type ToastTone = "neutral" | "success" | "warning";

export type ToastOptions = { duration?: ToastDuration; tone?: ToastTone };

export type ToastMessage = {
	/** New for every `show`, so the same words shown twice still count as two messages. */
	id: number;
	text: string;
	duration: ToastDuration;
	tone: ToastTone;
};

export type ToastController = {
	/** The message on screen, if any. */
	current: ToastMessage | undefined;
	/** Shows a message in place of the one on screen. */
	show(text: string, options?: ToastOptions): void;
	/** Takes the message down. With an id, only if that message is still the one showing. */
	hide(id?: number): void;
};

export type ToastHostProps = {
	/** From `useToast()`. */
	toast: ToastController;
	/**
	 * Distance from the bottom of the screen. Defaults to the bottom safe area
	 * plus a margin; a screen with a dock or toolbar there passes its height.
	 */
	bottomOffset?: number;
};
