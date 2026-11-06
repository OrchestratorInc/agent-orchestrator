export type LoadingBarProps = {
	/** The bar shows only while this is true. */
	loading: boolean;
	/** How far the load has come, from 0 to 1. Leave it out for a load that reports no progress. */
	progress?: number;
	/** What is loading, for screen readers: "Loading app preview". */
	accessibilityLabel: string;
};
