import { Fragment, useId, useState, type ReactNode } from "react";
import { AnimatePresence, motion, useReducedMotion } from "motion/react";
import type { ExternalLinkComponent } from "./external-link";
import { ArrowUpRightIcon, CheckStateIcon, ChevronIcon, GitBranchIcon } from "./icons";
import { UserAvatar } from "./UserAvatar";
import { scmUserAvatarUrl } from "./scm-avatar";
import type {
	PRCardPresentation,
	PRCardStatus,
	PRCheckState,
	PRChecksTally,
	PRDisplayTone,
	PRNoun,
	PRSummaryLink,
	PRSummaryMetadata,
	PRSummaryPart,
	PRSummaryPartKey,
} from "./pull-request-models";
import { cn } from "./utils";

const toneClass: Record<PRDisplayTone, string> = {
	neutral: "text-muted-foreground",
	passive: "text-passive",
	success: "text-success",
	review: "text-status-in-review",
	warning: "text-warning",
	error: "text-error",
};

export type CountNounLabel = (count: number, noun: PRNoun) => string;

const defaultCountNounLabel: CountNounLabel = (count, noun) => `${count} ${noun}${count === 1 ? "" : "s"}`;

export function PRSummaryMeta({
	className,
	countNounLabel = defaultCountNounLabel,
	externalLink: ExternalLink,
	inline = false,
	leading,
	openedViaLabel = "via Orchestrator",
	pr,
}: {
	className?: string;
	countNounLabel?: CountNounLabel;
	externalLink: ExternalLinkComponent;
	/** The author and the diff stats share one line, as the PR view's card shows them. */
	inline?: boolean;
	leading?: string;
	/** Follows the person's name on a PR AO's GitHub App opened for them. */
	openedViaLabel?: string;
	pr: PRSummaryMetadata;
}) {
	if (inline) {
		return <PRCompactMeta className={className} countNounLabel={countNounLabel} externalLink={ExternalLink} leading={leading} openedViaLabel={openedViaLabel} pr={pr} />;
	}
	const branchRange = prBranchRange(pr);
	const hasDiff = hasDiffMetadata(pr);
	const authorHandle = pr.author?.replace(/^@/, "") ?? "";
	const primary: ReactNode[] = [leading, branchRange].filter(Boolean);
	let author: ReactNode = null;
	const opener = pr.openedBy;
	const openerLogin = opener?.githubLogin?.replace(/^@/, "").trim() ?? "";
	const openerName = openerLogin || opener?.name.trim() || "";
	if (openerName) {
		// AO's GitHub App opened this PR, so GitHub lists its bot as the author;
		// name the person whose session it was instead.
		const avatar = (
			<UserAvatar
				className="size-3"
				imageUrl={opener?.avatarUrl || (openerLogin ? scmUserAvatarUrl("github", "https://github.com", openerLogin) : undefined)}
				name={openerName}
			/>
		);
		author = (
			<span className="inline-flex min-w-0 flex-wrap items-center gap-1">
				{openerLogin && pr.provider === "github" ? (
					<ExternalLink
						className="inline-flex min-w-0 items-center gap-1 text-settings-label underline-offset-2 hover:underline focus-visible:rounded-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60"
						href={`https://github.com/${encodeURIComponent(openerLogin)}`}
					>
						{avatar}
						{openerLogin}
					</ExternalLink>
				) : (
					<span className="inline-flex min-w-0 items-center gap-1 text-settings-label">
						{avatar}
						{openerName}
					</span>
				)}
				<span className="text-passive">{openedViaLabel}</span>
			</span>
		);
	} else if (authorHandle) {
		author =
			pr.provider === "github" ? (
				<ExternalLink
					className="inline-flex min-w-0 items-center gap-1 text-settings-label underline-offset-2 hover:underline focus-visible:rounded-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60"
					href={`https://github.com/${encodeURIComponent(authorHandle)}`}
				>
					<UserAvatar
						className="size-3"
						imageUrl={pr.authorAvatarUrl || scmUserAvatarUrl("github", "https://github.com", authorHandle)}
						name={authorHandle}
					/>
					{authorHandle}
				</ExternalLink>
			) : (
				<span>{authorHandle}</span>
			);
	}
	if (primary.length === 0 && !hasDiff && !author) {
		return null;
	}
	return (
		<div className={cn("min-w-0 font-mono text-xs leading-4", className)}>
			{primary.length > 0 ? (
				<div className="flex min-w-0 flex-wrap items-center gap-x-1.5 gap-y-0.5 text-muted-foreground">
					{primary.map((part, index) => (
						<Fragment key={index}>
							{index > 0 ? <span className="shrink-0 text-passive">·</span> : null}
							<span className="min-w-0 break-words [overflow-wrap:anywhere]">{part}</span>
						</Fragment>
					))}
				</div>
			) : null}
			{author ? <div className="mt-0.5 min-w-0 break-words [overflow-wrap:anywhere] text-muted-foreground">{author}</div> : null}
			{hasDiff ? <PRDiffMeta countNounLabel={countNounLabel} pr={pr} /> : null}
		</div>
	);
}

/** A long head branch keeps its first and last segments: `ao/…/document-taskr-edit`. */
export function compactBranchName(name: string, maxLength = 26): string {
	if (name.length <= maxLength) return name;
	const segments = name.split("/");
	if (segments.length < 3) return name;
	return `${segments[0]}/…/${segments[segments.length - 1]}`;
}

const formatCount = (value: number) => value.toLocaleString("en-US");

/**
 * The PR view card's one metadata line: the head and base branches on the
 * left; the changed files, the diff and the author's avatar on the right.
 * The author's name is the avatar's tooltip and accessible name, since it is
 * almost always the person looking at the card. The head shrinks first: a
 * long name keeps its first and last segments, then truncates.
 */
function PRCompactMeta({
	className,
	countNounLabel,
	externalLink: ExternalLink,
	leading,
	openedViaLabel,
	pr,
}: {
	className?: string;
	countNounLabel: CountNounLabel;
	externalLink: ExternalLinkComponent;
	leading?: string;
	openedViaLabel: string;
	pr: PRSummaryMetadata;
}) {
	const head = pr.sourceBranch?.trim() ?? "";
	const base = pr.targetBranch?.trim() ?? "";
	// AO's GitHub App opened this PR, so GitHub lists its bot as the author; name the person instead.
	const openerLogin = pr.openedBy?.githubLogin?.replace(/^@/, "").trim() ?? "";
	const openerName = openerLogin || pr.openedBy?.name.trim() || "";
	const authorLogin = pr.author?.replace(/^@/, "").trim() ?? "";
	const person = openerName || authorLogin;
	const login = openerName ? openerLogin : authorLogin;
	const personLabel = openerName ? `${person} ${openedViaLabel}` : person;
	const avatarUrl = openerName
		? pr.openedBy?.avatarUrl || (openerLogin ? scmUserAvatarUrl("github", "https://github.com", openerLogin) : undefined)
		: pr.authorAvatarUrl || (authorLogin ? scmUserAvatarUrl("github", "https://github.com", authorLogin) : undefined);
	const hasDiff = hasDiffMetadata(pr);
	if (!head && !base && !hasDiff && !person && !leading) return null;
	const avatar = person ? <UserAvatar className="size-4" imageUrl={avatarUrl} name={person} /> : null;
	const changed = [pr.additions > 0 ? `${formatCount(pr.additions)} added` : "", pr.deletions > 0 ? `${formatCount(pr.deletions)} removed` : ""].filter(Boolean).join(", ");
	return (
		<div className={cn("flex min-w-0 items-center gap-2 text-xs leading-4 text-muted-foreground", className)} data-testid="pr-meta">
			<span className="flex min-w-0 flex-1 items-center gap-1.5">
				{head || base ? <GitBranchIcon aria-hidden="true" className="size-icon-2xs shrink-0" /> : null}
				{leading ? (
					<>
						<span className="shrink-0">{leading}</span>
						<span aria-hidden="true" className="shrink-0 text-passive">·</span>
					</>
				) : null}
				{head ? (
					<span className="min-w-0 truncate font-mono text-foreground" title={head}>
						<span aria-hidden="true">{compactBranchName(head)}</span>
						<span className="sr-only">{head}</span>
					</span>
				) : null}
				{head && base ? (
					<>
						<span aria-hidden="true" className="shrink-0">→</span>
						<span className="sr-only"> into </span>
					</>
				) : null}
				{base ? (
					<span className={cn("min-w-0 max-w-[45%] shrink-0 truncate font-mono", !head && "text-foreground")} title={base}>
						{base}
					</span>
				) : null}
			</span>
			{hasDiff || avatar ? (
				<span className="flex shrink-0 items-center gap-2 tabular-nums">
					{pr.changedFiles > 0 ? <span>{countNounLabel(pr.changedFiles, "file")}</span> : null}
					{changed ? (
						<span className="inline-flex items-center gap-1">
							{pr.additions > 0 ? <span aria-hidden="true" className="text-success">+{formatCount(pr.additions)}</span> : null}
							{pr.deletions > 0 ? <span aria-hidden="true" className="text-error">−{formatCount(pr.deletions)}</span> : null}
							<span className="sr-only">{changed}</span>
						</span>
					) : null}
					{avatar ? (
						login && pr.provider === "github" ? (
							<ExternalLink
								ariaLabel={personLabel}
								className="inline-flex shrink-0 rounded-full focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60"
								href={`https://github.com/${encodeURIComponent(login)}`}
								title={personLabel}
							>
								{avatar}
							</ExternalLink>
						) : (
							<span aria-label={personLabel} className="inline-flex shrink-0" role="img" title={personLabel}>
								{avatar}
							</span>
						)
					) : null}
				</span>
			) : null}
		</div>
	);
}

function PRDiffMeta({
	countNounLabel,
	pr,
}: {
	countNounLabel: CountNounLabel;
	pr: PRSummaryMetadata;
}) {
	const parts: ReactNode[] = [];
	if (pr.changedFiles > 0) {
		parts.push(
			<span className="text-muted-foreground" key="files">
				{countNounLabel(pr.changedFiles, "file")}
			</span>,
		);
	}
	if (pr.additions > 0) {
		parts.push(
			<span className="text-success" key="additions">
				+{pr.additions}
			</span>,
		);
	}
	if (pr.deletions > 0) {
		parts.push(
			<span className="text-error" key="deletions">
				-{pr.deletions}
			</span>,
		);
	}
	return (
		<div className="flex min-w-0 flex-wrap items-center gap-x-1.5 text-muted-foreground">
			{parts.map((part, index) => (
				<Fragment key={index}>
					{index > 0 ? <span className="text-passive">·</span> : null}
					{part}
				</Fragment>
			))}
		</div>
	);
}

export function PRCardStatusSummary({
	action,
	className,
	compactReadiness = false,
	externalLink,
	omit,
	presentation,
	readinessDetails,
	reviewDetailsAction,
}: {
	action?: ReactNode;
	className?: string;
	/** One dropdown for the facts that decide the merge (`readinessRows`) instead of every status row. */
	compactReadiness?: boolean;
	externalLink: ExternalLinkComponent;
	omit?: PRSummaryPartKey[];
	presentation: PRCardPresentation;
	/** More under a readiness row, keyed by the row (such as the check list under "ci"). */
	readinessDetails?: Partial<Record<string, ReactNode>>;
	reviewDetailsAction?: ReactNode;
}) {
	const omitted = omit ? new Set(omit) : undefined;
	const supporting = presentation.supporting.filter(
		(status) => status.key === "lifecycle" || !omitted?.has(status.key),
	);
	const rows = presentation.statusRows?.filter((status) => status.key === "lifecycle" || !omitted?.has(status.key));
	if (presentation.readinessRows && presentation.readiness && compactReadiness) {
		return (
			<ReadinessFacts
				action={action}
				className={className}
				details={readinessDetails}
				explained={presentation.readinessExplained === true}
				externalLink={externalLink}
				readiness={presentation.readiness}
				reviewDetailsAction={reviewDetailsAction}
				rows={presentation.readinessRows}
			/>
		);
	}
	if (rows && presentation.readiness) {
		return (
			<div className={cn("border-t border-border pt-2", className)}>
				<div className="grid min-w-0 grid-cols-1 gap-y-1.5">
					{rows.map((status) => (
						<StatusRow dot externalLink={externalLink} key={status.key} reviewDetailsAction={reviewDetailsAction} status={status} />
					))}
				</div>
				<div className="mt-2 border-t border-border pt-2">
					<div className="flex min-w-0 items-center justify-between gap-3">
						<div className="min-w-0">
							<div className={cn("flex items-center gap-2 text-xs font-medium leading-4", toneClass[presentation.readiness.tone])}>
								<span aria-hidden="true" className="size-dot-sm shrink-0 rounded-full bg-current" />
								{presentation.readiness.label}
							</div>
							<div className="mt-0.5 min-w-0 break-words pl-4 text-xs leading-4 text-muted-foreground">{presentation.readiness.detail}</div>
						</div>
						{action ? <div className="shrink-0 self-center">{action}</div> : null}
					</div>
				</div>
			</div>
		);
	}
	return (
		<div className={cn("border-t border-border pt-2", className)}>
			<div className="flex min-w-0 items-center gap-3">
				<div className="flex min-w-0 flex-1 flex-col gap-1.5">
					<div className="flex min-w-0 items-start gap-2">
						<span
							aria-hidden="true"
							className={cn(
								"mt-1 size-dot-sm shrink-0 rounded-full bg-current",
								toneClass[presentation.primary.tone],
								presentation.primary.breathe && "animate-status-pulse",
							)}
						/>
						<div className="min-w-0 flex-1">
							<div
								className={cn(
									"text-xs font-semibold leading-4",
									toneClass[presentation.primary.tone],
								)}
							>
								<PRCardStatusLink externalLink={externalLink} status={presentation.primary} />
							</div>
							{presentation.primary.detail ? (
								<div className="mt-0.5 min-w-0 break-words text-xs leading-4 text-muted-foreground">
									{presentation.primary.detail}
								</div>
							) : null}
							{presentation.primary.links.length > 0 ? (
								<div className="mt-1 flex min-w-0 flex-wrap gap-x-1.5 gap-y-1 font-mono text-xs">
									{presentation.primary.links.slice(0, 3).map((link, index) => (
										<SummaryLink
											className={toneClass[presentation.primary.tone]}
											externalLink={externalLink}
											interactive
											key={`${presentation.primary.key}-${index}-${link.label}`}
											link={link}
										/>
									))}
								</div>
							) : null}
						</div>
					</div>
					{supporting.length > 0 ? (
						<div className="min-w-0 pl-4">
							<div className="flex min-w-0 flex-wrap gap-x-3 gap-y-1 font-mono text-xs">
								{supporting.map((status) => (
									<span
										className={cn("inline-flex items-center gap-1", toneClass[status.tone])}
										key={status.key}
									>
										<span
											aria-hidden="true"
											className={cn(
												"size-1 rounded-full bg-current",
												status.breathe && "animate-status-pulse",
											)}
										/>
										<PRCardStatusLink externalLink={externalLink} status={status} />
									</span>
								))}
							</div>
						</div>
					) : null}
				</div>
				{action ? <div className="shrink-0 self-center">{action}</div> : null}
			</div>
		</div>
	);
}


function StatusRow({
	details,
	dot = false,
	externalLink: ExternalLink,
	reviewDetailsAction,
	status,
}: {
	/** What opens from this row (such as which checks failed). */
	details?: ReactNode;
	/** A coloured dot before the label. The PR view's rows go without: the label's colour already says it. */
	dot?: boolean;
	externalLink: ExternalLinkComponent;
	reviewDetailsAction?: ReactNode;
	status: PRCardStatus;
}) {
	const [open, setOpen] = useState(false);
	const regionId = useId();
	const reduceMotion = useReducedMotion();
	// The checks row says its counts instead of a verdict; their glyphs carry the state.
	const tally = status.checks && status.checks.counts.length > 0 ? status.checks : undefined;
	// Without a dot, a review or conflicts row reads like the checks row: what it
	// is about, then a glyph and where it stands.
	const fact = !dot && !tally ? status.fact : undefined;
	const plain = tally || fact;
	const line = tally
		? <ChecksTally tally={tally} />
		: fact
			? (
				<span className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1" data-testid="pr-status-fact">
					<span>{fact.title}</span>
					<span className="inline-flex min-w-0 items-center gap-1" data-state={fact.state}>
						<CheckStateGlyph className={toneClass[status.tone]} state={fact.state} />
						<span className="min-w-0 truncate">{fact.value}</span>
					</span>
				</span>
			)
			: undefined;
	return (
		<div className="min-w-0">
			<div className={cn("flex min-w-0 items-center gap-2 text-xs font-medium leading-4", plain ? "text-foreground" : toneClass[status.tone])}>
				{dot && !tally ? <span aria-hidden="true" className={cn("size-dot-sm shrink-0 rounded-full bg-current", status.breathe && "animate-status-pulse")} /> : null}
				{details ? (
					<>
						<button
							aria-controls={open ? regionId : undefined}
							aria-expanded={open}
							className={cn(
								"inline-flex min-w-0 items-center gap-1 rounded-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60",
								plain ? "flex-1 gap-3 text-left" : "underline-offset-2 hover:underline",
							)}
							onClick={() => setOpen((value) => !value)}
							type="button"
						>
							{line ?? <span className="min-w-0 truncate">{status.label}</span>}
							<ChevronIcon aria-hidden="true" className={cn("size-icon-2xs shrink-0", plain && "ml-auto text-muted-foreground")} direction={open ? "up" : "down"} />
						</button>
						{status.href ? (
							<ExternalLink
								ariaLabel={status.label}
								className="-ml-1 inline-flex shrink-0 items-center rounded-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60"
								href={status.href}
							>
								<ArrowUpRightIcon className="size-icon-2xs shrink-0" />
							</ExternalLink>
						) : null}
					</>
				) : line ? (
					line
				) : (
					<PRCardStatusLink externalLink={ExternalLink} status={status} />
				)}
			</div>
			{status.detail || (status.key === "review" && reviewDetailsAction) ? (
				<div className={cn("mt-0.5 flex min-w-0 items-baseline gap-2 text-xs leading-4", dot && "pl-4")}>
					{status.detail ? <span className="text-muted-foreground">{status.detail}</span> : null}
					{status.key === "review" && reviewDetailsAction ? reviewDetailsAction : null}
				</div>
			) : null}
			<AnimatePresence initial={false}>
				{details && open ? (
					<motion.div
						animate={{ height: "auto", opacity: 1, transitionEnd: { overflow: "visible" } }}
						className="min-w-0"
						exit={{ height: 0, opacity: 0, overflow: "hidden" }}
						id={regionId}
						initial={{ height: 0, opacity: 0, overflow: "hidden" }}
						transition={reduceMotion ? { duration: 0 } : { duration: 0.2, ease: [0.4, 0, 0.2, 1] }}
					>
						<div className="min-w-0 pt-1">{details}</div>
					</motion.div>
				) : null}
			</AnimatePresence>
		</div>
	);
}

const checkStateClass: Record<PRCheckState, string> = {
	failed: "text-error",
	running: "text-status-working",
	pending: "text-muted-foreground",
	passed: "text-success",
	skipped: "text-passive",
};

/** A check state's glyph in its colour; a running check's spins unless motion is reduced. */
export function CheckStateGlyph({ className, state }: { className?: string; state: PRCheckState }) {
	return (
		<CheckStateIcon
			className={cn("size-icon-xs shrink-0", checkStateClass[state], state === "running" && "motion-safe:animate-spin", className)}
			state={state}
		/>
	);
}

/**
 * "Checks", then how many checks stand at each state, worst first. The first
 * count leads in full weight; the rest are quieter.
 */
function ChecksTally({ tally }: { tally: PRChecksTally }) {
	return (
		<span className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1" data-testid="pr-checks-tally">
			<span>{tally.title}</span>
			{tally.counts.map((count, index) => (
				<span
					className={cn("inline-flex items-center gap-1 tabular-nums", index === 0 ? "text-foreground" : "font-normal text-muted-foreground")}
					data-state={count.state}
					key={count.state}
				>
					<CheckStateGlyph state={count.state} />
					{count.label}
				</span>
			))}
		</span>
	);
}

/** The fact worth a whole line: what's in the way, else where the checks stand. */
function leadingFact(facts: PRCardStatus[]): PRCardStatus | undefined {
	const tones: PRDisplayTone[] = ["error", "warning", "review"];
	for (const tone of tones) {
		const fact = facts.find((status) => status.tone === tone);
		if (fact) return fact;
	}
	return facts.find((status) => status.key === "ci") ?? facts[0];
}

/**
 * One dropdown for what decides the merge. Its line is the fact that matters
 * most: a failing check already says the PR can't merge, so the verdict only
 * takes the line when no fact explains it. One click shows everything else.
 */
function ReadinessFacts({
	action,
	className,
	details,
	explained,
	externalLink,
	readiness,
	reviewDetailsAction,
	rows,
}: {
	action?: ReactNode;
	className?: string;
	details?: Partial<Record<string, ReactNode>>;
	explained: boolean;
	externalLink: ExternalLinkComponent;
	readiness: NonNullable<PRCardPresentation["readiness"]>;
	reviewDetailsAction?: ReactNode;
	rows: PRCardStatus[];
}) {
	// A passive row ("No reviews yet") decides nothing.
	const facts = rows.filter((status) => status.tone !== "passive");
	const lead = explained ? leadingFact(facts) : undefined;
	const others = facts.filter((status) => status !== lead);
	const leadMore = lead ? details?.[lead.key] : readiness.detail;
	const more = leadMore || others.length > 0 ? (
		<div className="grid min-w-0 grid-cols-1 gap-y-1.5">
			{leadMore ? <div className="min-w-0 text-xs leading-4 text-muted-foreground">{leadMore}</div> : null}
			{others.map((status) => (
				<div className="min-w-0" key={status.key}>
					<StatusRow externalLink={externalLink} reviewDetailsAction={reviewDetailsAction} status={status} />
					{details?.[status.key] ? <div className="mt-1 min-w-0">{details[status.key]}</div> : null}
				</div>
			))}
		</div>
	) : undefined;
	return (
		<div className={cn("flex min-w-0 items-start justify-between gap-3 border-t border-border pt-2", className)}>
			<div className="min-w-0 flex-1">
				<StatusRow
					details={more}
					externalLink={externalLink}
					reviewDetailsAction={reviewDetailsAction}
					status={lead ?? { key: "merge", label: readiness.label, links: [], tone: readiness.tone }}
				/>
			</div>
			{action ? <div className="shrink-0">{action}</div> : null}
		</div>
	);
}

function PRCardStatusLink({
	externalLink: ExternalLink,
	status,
}: {
	externalLink: ExternalLinkComponent;
	status: PRCardStatus;
}) {
	if (!status.href) {
		return status.label;
	}
	return (
		<ExternalLink
			className="inline-flex items-center gap-0.5 underline-offset-2 hover:underline focus-visible:rounded-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60"
			href={status.href}
		>
			{status.label}
			<ArrowUpRightIcon className="size-icon-2xs shrink-0" />
		</ExternalLink>
	);
}

export function PRSummaryParts({
	className,
	countNounLabel = defaultCountNounLabel,
	externalLink,
	interactiveLinks = true,
	maxLinks = 3,
	omit,
	parts,
	variant = "compact",
}: {
	className?: string;
	countNounLabel?: CountNounLabel;
	externalLink: ExternalLinkComponent;
	interactiveLinks?: boolean;
	maxLinks?: number;
	omit?: PRSummaryPartKey[];
	parts: PRSummaryPart[];
	variant?: "compact" | "stacked";
}) {
	const omitted = omit ? new Set(omit) : undefined;
	const visibleParts = parts.filter((part) => !omitted?.has(part.key));
	const stacked = variant === "stacked";
	return (
		<div
			className={cn(
				stacked
					? "flex flex-col gap-1.5 font-mono text-2xs leading-4"
					: "flex flex-wrap gap-x-3 gap-y-1 font-mono text-2xs",
				className,
			)}
		>
			{visibleParts.map((part) => {
				const links = part.links.slice(0, maxLinks);
				const extra = (part.linkTotal ?? part.links.length) - links.length;
				const overflowLabel =
					extra > 0
						? part.overflowNoun
							? `+${countNounLabel(extra, part.overflowNoun)}`
							: `+${extra}`
						: undefined;
				return (
					<div
						className={cn(
							"min-w-0",
							stacked ? "flex flex-col" : "inline-flex flex-wrap gap-x-1",
						)}
						key={part.key}
					>
						<div className="min-w-0 truncate">
							<span className="text-passive">{part.label}</span>{" "}
							<span className={cn("font-medium", toneClass[part.tone])}>{part.status}</span>
							{part.summary ? <span className="text-passive"> · {part.summary}</span> : null}
						</div>
						{links.length > 0 || overflowLabel ? (
							<div
								className={cn(
									"flex min-w-0 flex-wrap gap-x-1.5 gap-y-1",
									stacked ? "mt-0.5" : "",
								)}
							>
								{links.map((link, index) => (
									<SummaryLink
										externalLink={externalLink}
										interactive={interactiveLinks}
										key={`${part.key}-${index}-${link.label}`}
										link={link}
									/>
								))}
								{overflowLabel ? <span className="text-passive">{overflowLabel}</span> : null}
							</div>
						) : null}
					</div>
				);
			})}
		</div>
	);
}

function SummaryLink({
	className,
	externalLink: ExternalLink,
	interactive,
	link,
}: {
	className?: string;
	externalLink: ExternalLinkComponent;
	interactive: boolean;
	link: PRSummaryLink;
}) {
	if (interactive && link.href) {
		return (
			<ExternalLink
				className={cn(
					"inline-flex max-w-full min-w-0 items-center gap-0.5 text-settings-label underline-offset-2 hover:underline focus-visible:rounded-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60",
					className,
				)}
				href={link.href}
				stopPropagation
				title={link.title}
			>
				<span className="truncate">{link.label}</span>
				<ArrowUpRightIcon className="h-2.5 w-2.5 shrink-0" />
			</ExternalLink>
		);
	}
	return (
		<span className="max-w-full truncate text-muted-foreground" title={link.title}>
			{link.label}
		</span>
	);
}

export function prBranchRange(
	pr: Pick<PRSummaryMetadata, "sourceBranch" | "targetBranch">,
): string | undefined {
	if (pr.sourceBranch && pr.targetBranch) {
		return `${pr.sourceBranch} → ${pr.targetBranch}`;
	}
	if (pr.sourceBranch) {
		return pr.sourceBranch;
	}
	if (pr.targetBranch) {
		return `→ ${pr.targetBranch}`;
	}
	return undefined;
}

export function hasDiffMetadata(
	pr: Pick<PRSummaryMetadata, "changedFiles" | "additions" | "deletions">,
): boolean {
	return pr.changedFiles > 0 || pr.additions > 0 || pr.deletions > 0;
}
