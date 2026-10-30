export type AddFabProps = {
	/** What the button does. It shows only a glyph, so this is what screen readers say. */
	label: string;
	onPress: () => void;
	/**
	 * What the button has to stay clear of at the bottom of the page: the
	 * gesture bar on a full-screen page, nothing on a board tab, whose page
	 * already ends at the navigation bar.
	 */
	bottomInset: number;
	testID?: string;
};
