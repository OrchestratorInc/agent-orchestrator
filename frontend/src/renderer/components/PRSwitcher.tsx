import { ChevronDown, GitMerge, GitPullRequest, GitPullRequestClosed, GitPullRequestDraft, type LucideIcon } from "lucide-react";
import { Fragment } from "react";
import { useTranslation } from "react-i18next";
import type { SessionPRSummary } from "../hooks/useSessionScmSummary";
import { cn } from "../lib/utils";
import {
	DropdownMenu,
	DropdownMenuContent,
	DropdownMenuLabel,
	DropdownMenuRadioGroup,
	DropdownMenuRadioItem,
	DropdownMenuSeparator,
	DropdownMenuTrigger,
} from "./ui/dropdown-menu";

const stateIcon: Record<SessionPRSummary["state"], { icon: LucideIcon; className: string }> = {
	open: { icon: GitPullRequest, className: "text-success" },
	draft: { icon: GitPullRequestDraft, className: "text-passive" },
	merged: { icon: GitMerge, className: "text-status-merged" },
	closed: { icon: GitPullRequestClosed, className: "text-error" },
};

const stateLabelKey = {
	open: "pr.state.open",
	draft: "pr.state.draft",
	merged: "pr.state.merged",
	closed: "pr.state.closed",
} as const;

/** A PR's identity in a session: its URL, or its number for PRs without one. */
export function prFocusKey(pr: SessionPRSummary): string {
	return pr.url || pr.htmlUrl || String(pr.number);
}

/** Open PRs, then drafts, newest first in each; merged and closed ones after, as "Earlier". */
export function switcherGroups(prs: SessionPRSummary[]): { active: SessionPRSummary[]; earlier: SessionPRSummary[] } {
	const byNewest = [...prs].sort((a, b) => b.number - a.number);
	return {
		active: [...byNewest.filter((pr) => pr.state === "open"), ...byNewest.filter((pr) => pr.state === "draft")],
		earlier: byNewest.filter((pr) => pr.state === "merged" || pr.state === "closed"),
	};
}

/**
 * The session's other PRs, behind one quiet control in the PR card's header:
 * the card shows one PR at a time, and picking another here shows that one.
 */
export function PRSwitcher({ focused, onFocus, prs, showRepository }: {
	focused: SessionPRSummary;
	onFocus: (pr: SessionPRSummary) => void;
	prs: SessionPRSummary[];
	showRepository?: boolean;
}) {
	const { t } = useTranslation();
	const { active, earlier } = switcherGroups(prs);
	const others = prs.length - 1;
	const item = (pr: SessionPRSummary) => {
		const state = stateIcon[pr.state];
		return (
			<DropdownMenuRadioItem className="min-w-0 gap-2" key={prFocusKey(pr)} value={prFocusKey(pr)}>
				<state.icon aria-hidden="true" className={cn("size-icon-sm shrink-0", state.className)} />
				<span className="min-w-0 flex-1 truncate">
					<span className="font-medium">{showRepository && pr.repo ? `${pr.repo} ` : ""}#{pr.number}</span>
					{pr.title ? <span className="text-muted-foreground"> {pr.title}</span> : null}
				</span>
				<span className="shrink-0 text-micro text-passive">{t(stateLabelKey[pr.state])}</span>
			</DropdownMenuRadioItem>
		);
	};
	return (
		<DropdownMenu>
			<DropdownMenuTrigger asChild>
				<button
					aria-label={t("pr.switcher.label", { count: others })}
					className="flex min-h-8 shrink-0 items-center gap-0.5 rounded-md px-1.5 text-xs text-passive transition-colors hover:bg-muted/60 hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60"
					data-testid="pr-switcher"
					type="button"
				>
					<span className="whitespace-nowrap">{t("pr.switcher.others", { count: others })}</span>
					<ChevronDown aria-hidden="true" className="size-icon-2xs shrink-0" />
				</button>
			</DropdownMenuTrigger>
			<DropdownMenuContent align="end" className="w-80 max-w-[calc(100vw-2rem)]">
				<DropdownMenuRadioGroup
					onValueChange={(key) => { const next = prs.find((pr) => prFocusKey(pr) === key); if (next) onFocus(next); }}
					value={prFocusKey(focused)}
				>
					{active.map(item)}
					{earlier.length > 0 ? (
						<Fragment>
							{active.length > 0 ? <DropdownMenuSeparator /> : null}
							<DropdownMenuLabel className="text-micro font-medium text-passive">{t("pr.switcher.earlier")}</DropdownMenuLabel>
							{earlier.map(item)}
						</Fragment>
					) : null}
				</DropdownMenuRadioGroup>
			</DropdownMenuContent>
		</DropdownMenu>
	);
}
