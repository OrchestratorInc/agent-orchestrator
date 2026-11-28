/** How a line of task output reads at a glance. */
export type LineTone = "ok" | "warning" | "error";

// Output arrives without its terminal colors, so a line's tone comes from what
// common tools print: a compiler's "error TS2322:", npm's "ERR!", a test
// runner's ✓ and ×, Next's ⚠, a logger's ERROR or level=warn. A word only
// counts where a tool puts it, at the start of the line after any timestamp or
// bracketed tag, so "handled error: none" or LOG_LEVEL=ERROR stay plain, as
// does a summary such as "Found 1 error.".
const PREFIX = String.raw`^\s*(?:(?:\[[^\]]*\]|\([^)]*\)|\d[\d:.\-TZ/,]*)\s+)*`;
const tone = (symbol: string, word: string, levels: string, npm: string, level: string) => new RegExp([
	String.raw`^\s*${symbol}`,
	// TypeError:, error:, error TS2322:, AssertionError [ERR_ASSERTION]:
	PREFIX + String.raw`(?:[A-Z][A-Za-z]*)?(?:${word})(?: TS\d+| \[[^\]]*\])?:`,
	PREFIX + String.raw`\[?(?:${levels})\]?(?=[\s:]|$)`,
	// src/app.ts(3,4): error TS2322:
	String.raw`\b(?:${word.toLowerCase()}) TS\d+:`,
	String.raw`^\s*npm ${npm}`,
	String.raw`\blevel=(?:${level})\b`,
].join("|"));
const ERROR = tone("[×✗✘]\\s", "Error|error", "ERROR|ERR!?|FATAL", "ERR!", "error|fatal");
// What dev servers and test runners print for a failure: vite's "Internal
// server error:", webpack's "Module not found: Error: Can't resolve", a
// browser's "Uncaught SyntaxError:", and vitest's or jest's " FAIL " marker.
const DEV_ERROR = new RegExp([
	PREFIX + String.raw`(?:Internal server error|Module not found):`,
	PREFIX + String.raw`Uncaught (?:[A-Z][A-Za-z]*)?Error\b`,
	String.raw`\bError: Can't resolve\b`,
	String.raw`^\s*FAIL\s`,
].join("|"));
const WARNING = tone("⚠", "Warning|warning|Warn|warn", "WARNING|WARN", "WARN\\b", "warn|warning");
const OK = /^\s*[✓✔]\s/;

export function lineTone(line: string): LineTone | undefined {
	if (ERROR.test(line) || DEV_ERROR.test(line)) return "error";
	if (WARNING.test(line)) return "warning";
	if (OK.test(line)) return "ok";
	return undefined;
}

/** The output's lines. The newline that ends the last line doesn't start another. */
export function outputLines(text: string): string[] {
	if (text === "") return [];
	const lines = text.split("\n");
	if (lines.at(-1) === "") lines.pop();
	return lines;
}

/** How many lines the output has, and how many of them read as warnings or errors. */
export function outputSummary(text: string): { lines: number; warnings: number; errors: number } {
	const lines = outputLines(text);
	let warnings = 0;
	let errors = 0;
	for (const line of lines) {
		const tone = lineTone(line);
		if (tone === "warning") warnings += 1;
		else if (tone === "error") errors += 1;
	}
	return { lines: lines.length, warnings, errors };
}
