import type { BoardRelatedPullRequestPresentation } from "@aoagents/product-ui";
import type { TFunction } from "i18next";
import type { MessageKey } from "../i18n/messages";
import type { SessionPRLink } from "../types/workspace";

/** Related PRs are PRs a session reviews or contributes to without owning them. */
export const relatedPRRelationKeys: Record<SessionPRLink["relation"], MessageKey> = {
	reviewing: "pr.related.reviewing",
	contributing: "pr.related.contributing",
};

export const relatedPRStateKeys: Record<NonNullable<SessionPRLink["state"]>, MessageKey> = {
	draft: "pr.state.draft",
	open: "pr.state.open",
	merged: "pr.state.merged",
	closed: "pr.state.closed",
};

export function toBoardRelatedPR(link: SessionPRLink, t: TFunction): BoardRelatedPullRequestPresentation {
	const relationLabel = t(relatedPRRelationKeys[link.relation]);
	// The state is spoken and shown as text, not only as the icon's color.
	const stateLabel = link.state ? t(relatedPRStateKeys[link.state]) : undefined;
	return {
		ariaLabel: stateLabel
			? t("pr.related.ariaWithState", { relation: relationLabel, number: link.number, state: stateLabel })
			: t("pr.related.aria", { relation: relationLabel, number: link.number }),
		...(link.author ? { authorLabel: t("pr.related.by", { author: link.author }) } : {}),
		number: link.number,
		relationLabel,
		...(link.state && stateLabel ? { state: link.state, stateLabel } : {}),
		...(link.title ? { title: link.title } : {}),
		url: link.url,
	};
}
