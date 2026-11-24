export type OpenURLPromptProps = {
	/** The prompt is up while this is true; it opens, empty, each time this turns true. */
	visible: boolean;
	/** The address entered, trimmed and never empty. Called after `onDismiss`. */
	onSubmit(url: string): void;
	/** The prompt has closed, by either button or by dismissing it. */
	onDismiss(): void;
	/** The browser the address opens in, as the prompt names it. */
	browserName?: "desktop browser" | "agent browser";
};

export const openURLPromptCopy = {
	title: "Open URL",
	message: (browserName: "desktop browser" | "agent browser" = "desktop browser") => `Opens in the ${browserName}'s current tab.`,
	placeholder: "example.com",
	open: "Open",
	cancel: "Cancel",
} as const;
