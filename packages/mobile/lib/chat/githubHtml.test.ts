import { describe, expect, it } from "vitest";
import { githubSegments, isDecorativeIcon } from "./githubHtml";

const ICON = "https://storage.googleapis.com/coderabbit_public_assets/review-comment-icons-v1";

// A CodeRabbit inline review comment as GitHub stores it (trimmed): hidden markers,
// a 16px icon inside bold, a nested <details>/<dl><dd> tools section, and the agent prompt.
const CODERABBIT = [
	"<!-- coderabbit-ai-generated:v1 -->",
	"",
	`**<picture><img src="${ICON}/amber-16.svg" width="16" height="16" align="absmiddle" alt=""></picture> Minor** · **Functional Correctness** · **Quick win**`,
	"",
	"**Qualify the cross-worker profile isolation claim.**",
	"",
	"Workers use separate temporary profiles by default.",
	"",
	"<details>",
	`<summary><picture><source media="(prefers-color-scheme: dark)" srcset="${ICON}/wrench-dark.svg"><img src="${ICON}/wrench.svg" width="16" height="16" align="absmiddle" alt=""></picture> Tools</summary>`,
	"<dl>",
	"<dd>",
	"<br>",
	"",
	"<details>",
	"<summary>LanguageTool</summary>",
	"<dl>",
	"<dd>",
	"<br>",
	"",
	"[style] ~3-~3: Consider removing “of” to be more concise",
	"",
	"</dd>",
	"</dl>",
	"</details>",
	"",
	"</dd>",
	"</dl>",
	"</details>",
	"",
	"<!-- ai-agent-prompt:start -->",
	"",
	"<details>",
	`<summary><picture><source media="(prefers-color-scheme: dark)" srcset="${ICON}/bot-dark.svg"><img src="${ICON}/bot.svg" width="16" height="16" align="absmiddle" alt=""></picture> Prompt for AI Agents</summary>`,
	"<dl>",
	"<dd>",
	"",
	"```",
	"Review comment at browser.md line 3: <details> stays as written here.",
	"```",
	"",
	"</dd>",
	"</dl>",
	"</details>",
	"",
	"<!-- ai-agent-prompt:end -->",
	"",
	"<!-- fingerprinting:phantom:medusa:wombat62 -->",
	"",
	"<!-- This is an auto-generated comment by CodeRabbit -->",
	"",
	"✅ Addressed in commits 966e4db to edf4e75",
].join("\n");

describe("githubSegments", () => {
	it("reads a CodeRabbit comment as its words and folded sections, with no tags left", () => {
		expect(githubSegments(CODERABBIT)).toEqual([
			{
				kind: "markdown",
				text: "**Minor** · **Functional Correctness** · **Quick win**\n\n**Qualify the cross-worker profile isolation claim.**\n\nWorkers use separate temporary profiles by default.",
			},
			{
				kind: "details",
				summary: "Tools",
				body: [{ kind: "details", summary: "LanguageTool", body: [{ kind: "markdown", text: "[style] ~3-~3: Consider removing “of” to be more concise" }] }],
			},
			{
				kind: "details",
				summary: "Prompt for AI Agents",
				body: [{ kind: "markdown", text: "```\nReview comment at browser.md line 3: <details> stays as written here.\n```" }],
			},
			{ kind: "markdown", text: "✅ Addressed in commits 966e4db to edf4e75" },
		]);
	});

	it("leaves plain Markdown as written", () => {
		const text = "Rename `foo` to **bar**.\n\n- one\n- two";
		expect(githubSegments(text)).toEqual([{ kind: "markdown", text }]);
	});

	it("reads only GitHub's tags as tags, and never reads into code", () => {
		expect(githubSegments("Return `Array<T>` or a Map<string, number>; see `<!-- x -->`.")).toEqual([
			{ kind: "markdown", text: "Return `Array<T>` or a Map<string, number>; see `<!-- x -->`." },
		]);
		expect(githubSegments("```html\n<details><summary>s</summary></details>\n<!-- kept -->\n```")).toEqual([
			{ kind: "markdown", text: "```html\n<details><summary>s</summary></details>\n<!-- kept -->\n```" },
		]);
	});

	it("turns inline and block HTML into Markdown and decodes entities", () => {
		expect(githubSegments('<p>Use <b>bold</b>, <em>this</em> and <code>a &lt; b</code>.<br>Next &amp; last</p>')).toEqual([
			{ kind: "markdown", text: "Use **bold**, *this* and `a < b`.\nNext & last" },
		]);
		expect(githubSegments('<a href="https://github.com/o/r/pull/1">PR one</a>')).toEqual([{ kind: "markdown", text: "[PR one](https://github.com/o/r/pull/1)" }]);
		expect(githubSegments("<ul><li>one</li><li>two</li></ul>")).toEqual([{ kind: "markdown", text: "- one\n- two" }]);
		expect(githubSegments("<h3>Walkthrough</h3>text")).toEqual([{ kind: "markdown", text: "### Walkthrough\n\ntext" }]);
	});

	it("keeps an image's alt text, drops icons and a link that only wrapped one", () => {
		expect(githubSegments('<a href="https://app.coderabbit.ai/x"><picture><img src="a.svg" alt="Explain Change" height="32"></picture></a>')).toEqual([
			{ kind: "markdown", text: "[Explain Change](https://app.coderabbit.ai/x)" },
		]);
		expect(githubSegments('<a href="https://example.com"><img src="i.svg" width="16" alt=""></a> after')).toEqual([{ kind: "markdown", text: "after" }]);
	});

	it("closes unbalanced sections and hides an unterminated comment", () => {
		expect(githubSegments("before</details><details><summary>Open</summary>inside")).toEqual([
			{ kind: "markdown", text: "before" },
			{ kind: "details", summary: "Open", body: [{ kind: "markdown", text: "inside" }] },
		]);
		expect(githubSegments("shown <!-- hidden forever")).toEqual([{ kind: "markdown", text: "shown" }]);
		expect(githubSegments("<details>no summary</details>")).toEqual([{ kind: "details", summary: "Details", body: [{ kind: "markdown", text: "no summary" }] }]);
	});

	it("returns nothing for a body that is only hidden state", () => {
		expect(githubSegments("<!-- a -->\n\n<!-- b -->")).toEqual([]);
	});
});

describe("isDecorativeIcon", () => {
	it("is an icon only with no alt text and only icon sizes", () => {
		expect(isDecorativeIcon("", "16", "16")).toBe(true);
		expect(isDecorativeIcon("Status", "16", "16")).toBe(false);
		expect(isDecorativeIcon("", "600", "32")).toBe(false);
		expect(isDecorativeIcon("", undefined, undefined)).toBe(false);
	});
});
