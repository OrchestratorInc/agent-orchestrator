export type OpenURLPromptProps = {
	/** The prompt is up while this is true; it opens, empty, each time this turns true. */
	visible: boolean;
	/** The address entered, trimmed and never empty. Called after `onDismiss`. */
	onSubmit(url: string): void;
	/** The prompt has closed, by either button or by dismissing it. */
	onDismiss(): void;
};

export const openURLPromptCopy = {
	title: "Open URL",
	message: "Opens in the desktop browser's current tab.",
	placeholder: "example.com",
	open: "Open",
	cancel: "Cancel",
} as const;
