import { useTranslation } from "react-i18next";
import { ArrowUpRight, GitPullRequest } from "@/components/app-icons";
import { relatedPRRelationKeys, relatedPRStateKeys } from "../lib/related-prs";
import type { SessionPRLink } from "../types/workspace";
import { ProductExternalLink } from "./ProductExternalLink";

/**
 * A PR the session reviews or contributes to without owning it: the relation,
 * a link out, and what the provider said when it was linked. It has no checks,
 * review, or merge controls, since none of those route to this session.
 */
export function SessionRelatedPRCard({ link }: { link: SessionPRLink }) {
	const { t } = useTranslation();
	const noun = link.provider === "gitlab" ? "MR" : "PR";
	return (
		<article
			className="min-w-0 w-full rounded-lg border border-(--color-border-settings-input) bg-(--color-bg-settings-input) px-3 py-2.5"
			data-testid="session-related-pr"
		>
			<div className="flex min-w-0 items-center gap-1.5">
				<span className="shrink-0 rounded-full border border-(--color-border-settings-input) px-1.5 py-0.5 text-[10px] leading-none text-settings-muted">
					{t(relatedPRRelationKeys[link.relation])}
				</span>
				<ProductExternalLink
					className="inline-flex min-w-0 items-center gap-1 text-xs font-medium text-settings-label underline-offset-2 hover:underline focus-visible:rounded-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60"
					href={link.url}
				>
					<GitPullRequest className="size-icon-sm shrink-0" aria-hidden="true" />
					<span className="truncate">{noun} #{link.number}</span>
					<ArrowUpRight aria-hidden="true" className="size-icon-2xs shrink-0" />
				</ProductExternalLink>
			</div>
			{link.title ? <p className="mt-1 line-clamp-2 text-xs text-settings-label">{link.title}</p> : null}
			<p className="mt-1 truncate text-xs text-settings-muted">
				{[
					`${link.host}/${link.repo}`,
					link.state ? t(relatedPRStateKeys[link.state]) : undefined,
					link.author ? t("pr.related.by", { author: link.author }) : undefined,
				].filter(Boolean).join(" · ")}
			</p>
		</article>
	);
}
