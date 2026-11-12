import { describe, expect, it } from "vitest";
import { appI18n } from "../i18n";
import type { SessionPRLink } from "../types/workspace";
import { toBoardRelatedPR } from "./related-prs";

const t = appI18n.t.bind(appI18n);

const link: SessionPRLink = {
	url: "https://github.com/acme/app/pull/8020",
	provider: "github",
	host: "github.com",
	repo: "acme/app",
	number: 8020,
	relation: "contributing",
	title: "Refactor sidebar update UI",
	state: "open",
	author: "contributor",
};

describe("related PRs", () => {
	it("labels the relation, never as owned", () => {
		const view = toBoardRelatedPR(link, t);
		expect(view.relationLabel).toBe(t("pr.related.contributing"));
		expect(view.stateLabel).toBe(t("pr.state.open"));
		expect(view.ariaLabel).toBe(t("pr.related.ariaWithState", { relation: view.relationLabel, number: 8020, state: view.stateLabel }));
		expect(view.authorLabel).toBe(t("pr.related.by", { author: "contributor" }));
		expect(view).toMatchObject({ number: 8020, state: "open", title: "Refactor sidebar update UI", url: link.url });
	});

	it("leaves out facts the provider has not reported yet", () => {
		const view = toBoardRelatedPR({ ...link, relation: "reviewing", title: undefined, state: undefined, author: undefined }, t);
		expect(view.relationLabel).toBe(t("pr.related.reviewing"));
		expect(view).not.toHaveProperty("title");
		expect(view).not.toHaveProperty("state");
		expect(view).not.toHaveProperty("stateLabel");
		expect(view.ariaLabel).toBe(t("pr.related.aria", { relation: view.relationLabel, number: 8020 }));
		expect(view).not.toHaveProperty("authorLabel");
	});
});
