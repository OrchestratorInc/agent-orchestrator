import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { PRMarkdown } from "./PRMarkdown";

// The shape of a CodeRabbit summary comment: template comments, a badge link
// around a <picture>, and a <details> walkthrough.
const codeRabbitBody = [
	"<!-- This is an auto-generated comment: summarize by coderabbit.ai -->",
	"<!-- review_stack_entry_start -->",
	"<a href=\"https://app.coderabbit.ai/change-stack/acme/repo/pull/7\"><picture><source media=\"(prefers-color-scheme: dark)\" srcset=\"https://storage.googleapis.com/coderabbit/explain-dark.svg\"><img src=\"https://storage.googleapis.com/coderabbit/explain.svg\" alt=\"Explain Change\" height=\"32\"></picture></a>",
	"<!-- review_stack_entry_end -->",
	"",
	"No actionable comments were generated in the recent review.",
	"",
	"<!-- walkthrough_start --> <details>",
	"<summary><strong>Walkthrough</strong></summary>",
	"",
	"AppLink adds system-browser routing for `<a>` links.",
	"",
	"</details>",
].join("\n");

describe("PRMarkdown", () => {
	it("reads a bot's HTML as text and images, never as literal tags", () => {
		const { container } = render(<PRMarkdown body={codeRabbitBody} />);
		const text = container.textContent ?? "";
		expect(text).not.toMatch(/<!--|<details|<summary|<picture|<a href|<\/a>|<img/);
		expect(screen.getByText("No actionable comments were generated in the recent review.")).toBeInTheDocument();
		expect(screen.getByText("Walkthrough")).toBeInTheDocument();
		// The badge keeps its picture, as an image the viewer opens.
		expect(screen.getByRole("button", { name: "Open image: Explain Change" })).toBeInTheDocument();
		// Code keeps its angle brackets.
		expect(screen.getByText("<a>")).toBeInTheDocument();
	});

	it("keeps tags written inside double-backtick code", () => {
		render(<PRMarkdown body={"Use ``<Widget>`` here, and `` a`b `` too."} />);
		expect(screen.getByText("<Widget>")).toBeInTheDocument();
		expect(screen.getByText("a`b")).toBeInTheDocument();
	});
});
