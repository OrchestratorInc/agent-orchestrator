/** How a line of task output reads at a glance. */
export type LineTone = "ok" | "warning" | "error";

// Output arrives without its terminal colors, so a line's tone comes from what
// common tools print: a compiler's "error TS2322:", npm's "ERR!", a test
// runner's ✓ and ×, Next's ⚠. A summary such as "Found 1 error." stays plain.
const ERROR = /^\s*[×✗✘]\s|\b(?:[A-Z][A-Za-z]*)?(?:Error|error)(?: TS\d+)?:|\bERR(?:OR)?\b/;
const WARNING = /^\s*⚠|\b(?:Warning|warning|Warn|warn)(?: TS\d+)?:|\bWARN(?:ING)?\b/;
const OK = /^\s*[✓✔]\s/;

export function lineTone(line: string): LineTone | undefined {
	if (ERROR.test(line)) return "error";
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
