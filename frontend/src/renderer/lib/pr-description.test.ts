import { describe, expect, it } from "vitest";
import { descriptionPreview } from "./pr-description";

describe("descriptionPreview", () => {
	it("keeps the words and drops markdown syntax, HTML and template comments", () => {
		expect(
			descriptionPreview(
				"<!-- Describe your change -->\n## Summary\n\nPrevent **headless** runs from [overwriting](https://x) the `identity`.\n\n- [x] first fix\n2. second <img src=x onerror=alert(1)>\n\n```go\nfoo()\n```",
			),
		).toBe("Prevent headless runs from overwriting the identity. first fix second foo()");
	});

	it("drops what sits inside script and style tags, not only the tags", () => {
		expect(descriptionPreview("Fixes it.<script>window.__pwned = true</script><style>p{}</style>")).toBe("Fixes it.");
	});

	it("keeps headings when they are all there is", () => {
		expect(descriptionPreview("## Summary\n### Details")).toBe("Summary Details");
	});

	it("is empty for a body of only comments and rules", () => {
		expect(descriptionPreview("<!-- template -->\n---\n")).toBe("");
	});
});
