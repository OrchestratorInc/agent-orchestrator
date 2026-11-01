export type ComposerSearchFieldProps = {
	/** What the field opens with: the text typed after the composer's `/` or `@`. */
	initialQuery: string;
	placeholder: string;
	/** Called with the query on every change; must be stable, as it feeds the header. */
	onQuery(query: string): void;
};
