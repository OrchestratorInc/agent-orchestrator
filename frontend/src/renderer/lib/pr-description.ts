/**
 * A PR body's words as one plain line, for the PR card's two-line preview: the
 * markdown syntax, HTML and template comments go, and headings drop out when
 * there is text besides them (the preview has no room for "## Summary").
 */
export function descriptionPreview(markdown: string): string {
	const lines = markdown
		.replace(/<!--[\s\S]*?-->/g, "")
		.replace(/<(script|style)\b[\s\S]*?<\/\1>/gi, "")
		.replace(/<[^>]+>/g, "")
		.split("\n")
		.map((line) => line.trim())
		.filter((line) => line && !/^(```|~~~|---+$|\*\*\*+$|\|?[\s:|-]+\|[\s:|-]*$)/.test(line));
	const isHeading = (line: string) => /^#{1,6}\s/.test(line);
	const body = lines.some((line) => !isHeading(line)) ? lines.filter((line) => !isHeading(line)) : lines;
	return body
		.map((line) =>
			line
				.replace(/^#{1,6}\s+/, "")
				.replace(/^>\s?/, "")
				.replace(/^([-*+]|\d+[.)])\s+/, "")
				.replace(/^\[[ xX]\]\s+/, "")
				.replace(/!\[([^\]]*)\]\([^)]*\)/g, "$1")
				.replace(/\[([^\]]+)\]\([^)]*\)/g, "$1")
				.replace(/(\*\*|__|`)/g, ""),
		)
		.join(" ")
		.replace(/\s+/g, " ")
		.trim();
}
