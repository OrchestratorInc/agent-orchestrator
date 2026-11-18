import type { PRListFilter } from "./prView";

export type PRFilterControlProps = {
	filter: PRListFilter;
	/** Each filter's count, shown in its segment. */
	counts: Record<PRListFilter, number>;
	onChange: (filter: PRListFilter) => void;
};
