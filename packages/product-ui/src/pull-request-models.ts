export type PRDisplayTone = "neutral" | "passive" | "success" | "review" | "warning" | "error";

export type PRSummaryPartKey = "ci" | "review" | "merge";
export type PRNoun = "check" | "comment" | "file" | "line" | "reason" | "reviewer";

export type PRStatusRow = {
	key: PRSummaryPartKey;
	label: string;
	value: string;
	detail?: string;
	tone: PRDisplayTone;
};

export type PRSummaryLink = {
	label: string;
	href?: string;
	title?: string;
};

export type PRSummaryPart = {
	key: PRSummaryPartKey;
	label: string;
	status: string;
	summary?: string;
	links: PRSummaryLink[];
	linkTotal?: number;
	overflowLabel?: string;
	overflowNoun?: PRNoun;
	tone: PRDisplayTone;
};

export type PRCardStatus = {
	key: "ci" | "merge" | "review" | "lifecycle";
	label: string;
	detail?: string;
	href?: string;
	breathe?: boolean;
	links: PRSummaryLink[];
	tone: PRDisplayTone;
	/** How the checks stand: the checks row shows these counts in place of its label. */
	checks?: PRChecksTally;
	/**
	 * The same fact as the label, split the way the checks row reads: what it is
	 * about ("Review"), then where it stands ("Required") behind a state glyph.
	 * Both already translated.
	 */
	fact?: PRCardFact;
};

export type PRCardFact = {
	title: string;
	value: string;
	state: PRCheckState;
};

/** Where a check stands, from worst to best. */
export type PRCheckState = "failed" | "running" | "pending" | "passed" | "skipped";

/** How many checks stand at one state, with the count already in words. */
export type PRChecksCount = {
	state: PRCheckState;
	count: number;
	label: string;
};

/**
 * The checks row's line: its title, then a count for each state some check
 * is in, worst first (a cancelled run counts as failed). Both already translated.
 */
export type PRChecksTally = {
	title: string;
	counts: PRChecksCount[];
};

export type PRCardPresentation = {
	primary: PRCardStatus;
	supporting: PRCardStatus[];
	/** Compact, priority-ordered rows used by pull-request cards. */
	statusRows?: PRCardStatus[];
	/** The facts that decide the merge: conflicts, checks and review. */
	readinessRows?: PRCardStatus[];
	/** The rows already say what `readiness` would, so its verdict can stay out of the way. */
	readinessExplained?: boolean;
	readiness?: {
		label: string;
		detail: string;
		tone: PRDisplayTone;
	};
};

/** The person behind a pull request AO opened through its GitHub App. */
export type PROpener = {
	name: string;
	githubLogin?: string;
	avatarUrl?: string;
};

export type PRSummaryMetadata = {
	provider: string;
	author?: string;
	authorAvatarUrl?: string;
	/** Set when AO's GitHub App opened the PR, so `author` is its bot account. */
	openedBy?: PROpener;
	sourceBranch?: string;
	targetBranch?: string;
	changedFiles: number;
	additions: number;
	deletions: number;
};
