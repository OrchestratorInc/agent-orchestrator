import { describe, expect, it } from "vitest";
import { githubSegments, isDecorativeIcon } from "./githubHtml";
import { safeMarkdown } from "./automationMessages";
import { parseBlocks } from "./markdownBlocks";

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

	// Every block comes back as a ``` block; a line inside that would read as a fence starts with ZWSP.
	it("ends a fence only at a closing fence of the same character, at least as long", () => {
		expect(githubSegments("````md\n```\n<details><summary>s</summary>x</details>\n```\n````")).toEqual([
			{ kind: "markdown", text: "```md\n\u200B```\n<details><summary>s</summary>x</details>\n\u200B```\n```" },
		]);
		expect(githubSegments("~~~\n```\n<details><summary>s</summary>x</details>\n~~~")).toEqual([
			{ kind: "markdown", text: "```\n\u200B```\n<details><summary>s</summary>x</details>\n```" },
		]);
		// A shorter run, or the other character, inside a fence is code; the fence then runs on to the end.
		expect(githubSegments("````\n```\n<!-- kept -->")).toEqual([{ kind: "markdown", text: "```\n\u200B```\n<!-- kept -->\n```" }]);
		expect(githubSegments("```\ncode\n`````\n<b>bold</b>")).toEqual([{ kind: "markdown", text: "```\ncode\n```\n**bold**" }]);
	});

	it("closes an empty fence at the very next line", () => {
		for (const fence of ["```", "~~~"]) {
			expect(githubSegments(`${fence}\n${fence}\n<details><summary>Note</summary>body</details>`)).toEqual([
				{ kind: "markdown", text: "```\n```" },
				{ kind: "details", summary: "Note", body: [{ kind: "markdown", text: "body" }] },
			]);
		}
		// Each block closes at its own fence, so the HTML between two blocks is read.
		expect(githubSegments("```\n```\n<b>between</b>\n```\nb\n```")).toEqual([{ kind: "markdown", text: "```\n```\n**between**\n```\nb\n```" }]);
		// Backticks after the opening run make it an inline code span, not a fence.
		expect(githubSegments("```code``` and <b>bold</b>")).toEqual([{ kind: "markdown", text: "```code``` and **bold**" }]);
	});

	it("protects whole code spans of any backtick run, including ones that wrap a line", () => {
		const double = "Write `` `<details>` `` literally.";
		expect(githubSegments(double)).toEqual([{ kind: "markdown", text: double }]);
		const wrapped = "See `<details>\n<summary>x</summary>` here.";
		expect(githubSegments(wrapped)).toEqual([{ kind: "markdown", text: wrapped }]);
		// A blank line ends the paragraph, so a lone backtick there opens no span.
		expect(githubSegments("a ` b\n\n<b>c</b> `")).toEqual([{ kind: "markdown", text: "a ` b\n\n**c** `" }]);
	});

	it("reads a closed comment as a whole, whatever backticks or fences it holds", () => {
		expect(githubSegments("<!-- note: ` -->\nVisible review with `code`.")).toEqual([{ kind: "markdown", text: "Visible review with `code`." }]);
		expect(githubSegments("Use `x` <!-- a ` b --> and `y`.")).toEqual([{ kind: "markdown", text: "Use `x`  and `y`." }]);
		expect(githubSegments("<!--\n```\n-->\nVisible <b>text</b>")).toEqual([{ kind: "markdown", text: "Visible **text**" }]);
		// A comment opened mid-line ends at its `-->`, even with a fence line inside it.
		expect(githubSegments("prefix <!--\n```\n-->\nVisible review with `code`.")).toEqual([{ kind: "markdown", text: "prefix\nVisible review with `code`." }]);
		// A span stops at a fence line, so the fence keeps what follows as code.
		expect(githubSegments("a `\n```\nx` <b>y</b>\n```")).toEqual([{ kind: "markdown", text: "a `\n```\nx` <b>y</b>\n```" }]);
		// Only a line that really opens a fence stops it: ```example` holds a backtick, so it is not one.
		expect(githubSegments("Use `<details>\n```example` here.")).toEqual([{ kind: "markdown", text: "Use `<details>\n```example` here." }]);
		// Whichever starts first wins: a span that opens before a comment keeps it as code.
		expect(githubSegments("`<!--` stays")).toEqual([{ kind: "markdown", text: "`<!--` stays" }]);
		expect(githubSegments("```\n<!--\n```\nshown")).toEqual([{ kind: "markdown", text: "```\n<!--\n```\nshown" }]);
	});

	it("reads a backslash-escaped tag as text, keeping what follows it visible", () => {
		expect(githubSegments("Avoid \\<details\\> in this file.\n\nPlease rename the helper before merging.")).toEqual([
			{ kind: "markdown", text: "Avoid <details> in this file.\n\nPlease rename the helper before merging." },
		]);
		expect(githubSegments("\\<!-- shown --\\> and \\<b>plain\\</b>")).toEqual([{ kind: "markdown", text: "<!-- shown --> and <b>plain</b>" }]);
		// An escaped `&` keeps the entity after it as written.
		expect(githubSegments("\\&lt;b&gt;")).toEqual([{ kind: "markdown", text: "&lt;b>" }]);
		// Other escapes are left as written, and an escaped backtick opens no code span.
		expect(githubSegments("\\*not bold\\* and \\`<b>x</b>\\`")).toEqual([{ kind: "markdown", text: "\\*not bold\\* and \\`**x**\\`" }]);
		// Inside code, a backslash is code too.
		expect(githubSegments("`\\<details>` stays")).toEqual([{ kind: "markdown", text: "`\\<details>` stays" }]);
	});

	it("keeps code spans inside HTML code as written", () => {
		expect(githubSegments("<pre><code>const name = `world`;</code></pre>")).toEqual([{ kind: "markdown", text: "```\nconst name = `world`;\n```" }]);
		expect(githubSegments("<pre>```\nfence\n```</pre>")).toEqual([{ kind: "markdown", text: "```\n\u200B```\nfence\n\u200B```\n```" }]);
		expect(githubSegments("Run <code>a `b` c</code>.")).toEqual([{ kind: "markdown", text: "Run `` a `b` c ``." }]);
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

const markdownOf = (body: string) => githubSegments(body).map((segment) => segment.kind === "markdown" ? segment.text : "").join("");

describe("hostile and unusual comment HTML", () => {
	it("keeps a <pre> holding a fence line as one code block, in both mobile parsers", () => {
		const text = markdownOf("<pre>a\n```\nb</pre>\n[x](https://github.com/o/r)");
		expect(text).toBe("```\na\n\u200B```\nb\n```\n\n[x](https://github.com/o/r)");
		// ChatMarkdown's parser draws exactly one code block, guard gone, then the link as prose.
		expect(parseBlocks(text)).toEqual([{ kind: "code", language: undefined, text: "a\n```\nb" }, { kind: "paragraph", text: "[x](https://github.com/o/r)" }]);
		// safeMarkdown leaves the whole block as code and still checks the link after it.
		expect(safeMarkdown(text)).toBe(`${text.slice(0, text.lastIndexOf("\n\n"))}\n\n[x](https://github.com/o/r) (github.com)`);
	});

	it("draws ~~~ fences as ``` blocks, which ChatMarkdown reads as code", () => {
		const text = markdownOf("~~~js\n[x](https://evil.example)\n~~~");
		expect(text).toBe("```js\n[x](https://evil.example)\n```");
		expect(parseBlocks(text)).toEqual([{ kind: "code", language: "js", text: "[x](https://evil.example)" }]);
	});

	it("writes a link target the renderers read whole, and drops links they would not allow", () => {
		expect(markdownOf('<a href="https://github.com/o/r/blob/main/a_(b).md">doc</a>')).toBe("[doc](https://github.com/o/r/blob/main/a_%28b%29.md)");
		expect(safeMarkdown(markdownOf('<a href="https://github.com/o/r/blob/main/a_(b).md">doc</a>'))).toBe("[doc](https://github.com/o/r/blob/main/a_%28b%29.md) (github.com)");
		expect(safeMarkdown(markdownOf('<a href="javascript:alert(1)">click</a>'))).toBe("click");
		expect(safeMarkdown(markdownOf('<a href="&#106;avascript:alert(1)">click</a>'))).toBe("click");
		expect(safeMarkdown(markdownOf('<a href="https://evil.example/x">docs</a>'))).toBe("docs");
		expect(safeMarkdown(markdownOf('<a href="https://github.com/o/r/pull/1">PR</a>'))).toBe("[PR](https://github.com/o/r/pull/1) (github.com)");
		// A label holding brackets cannot be a link in either renderer, so it keeps its words.
		expect(markdownOf('<a href="https://github.com/o/r">[see] this</a>')).toBe("[see] this");
	});

	it("reads a > inside a quoted attribute as part of the tag", () => {
		expect(markdownOf('<a title="x>y" href="https://github.com/o/r">t</a>')).toBe("[t](https://github.com/o/r)");
		expect(markdownOf("<img alt='a > b' width=\"300\" src=\"x.png\"> after")).toBe("a > b after");
	});

	it("numbers ordered lists and marks each paragraph of bold or emphasis", () => {
		expect(markdownOf("<ol><li>one</li><li>two</li></ol><ul><li>dot</li></ul>")).toBe("1. one\n2. two\n\n- dot");
		expect(markdownOf('<ol start="3"><li>c</li></ol>')).toBe("3. c");
		expect(markdownOf("<b>a\n\nb</b> and <em>c\n\nd</em>")).toBe("**a**\n\n**b** and *c*\n\n*d*");
	});

	it("keeps the words of unclosed and stray tags", () => {
		expect(markdownOf("<b>open <i>also</i> text")).toBe("open *also* text");
		expect(markdownOf("</b>stray <a href=\"https://github.com/o/r\">never closed")).toBe("stray never closed");
	});

	it("writes a summary in what a summary draws: bold, code and links, emphasis as words", () => {
		const [section] = githubSegments('<details><summary><em>Why</em> <strong>now</strong> <code>x</code> <a href="https://github.com/o/r">docs</a></summary>body</details>');
		expect(section).toEqual({ kind: "details", summary: "Why **now** `x` [docs](https://github.com/o/r)", body: [{ kind: "markdown", text: "body" }] });
	});

	// GitHub's comment limit is 65,536 characters. Each pass reads a body once, so even
	// the worst of these stays far under the bound (about 15 ms on a laptop); a pattern
	// that rescans to the end of the body for each unclosed tag took ~280 ms for <b> alone.
	it.each<[string, string]>([
		["unclosed <b>", "<b>"], ["unclosed <a>", "<a href=x>"], ["unclosed <h1>", "<h1>"], ["unclosed <i>", "<i>"],
		["unclosed <code>", "<code>"], ["unclosed <pre>", "<pre>"], ["unclosed <details>", "<details>"], ["<details without >", "<details "],
		["unclosed <summary>", "<details><summary>"], ["unclosed quote", '<a title="'], ["<b without >", "<b "], ["unclosed comments", "<!--"],
		["fence lines", "```\nx\n"], ["lists", "<ol><li>"], ["stray ampersands", "&a"],
	])("converts a %s body at GitHub's size limit quickly", (_name, unit) => {
		const body = unit.repeat(Math.ceil(65_536 / unit.length)).slice(0, 65_536);
		const started = performance.now();
		githubSegments(body);
		expect(performance.now() - started).toBeLessThan(150);
	});

	it("converts deeply nested and long-run bodies at GitHub's size limit quickly", () => {
		let unpaired = "";
		for (let run = 1; unpaired.length < 65_536; run++) unpaired += `${"`".repeat(run)}x `;
		for (const body of ["<b>".repeat(10_922) + "</b>".repeat(10_922), unpaired.slice(0, 65_536), `<b>a${" ".repeat(65_500)}b</b>`, `<code>a${" ".repeat(65_500)}b</code>`]) {
			const started = performance.now();
			githubSegments(body);
			expect(performance.now() - started).toBeLessThan(150);
		}
	});
});
