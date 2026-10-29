import { readFileSync } from "node:fs";
import { pathToFileURL } from "node:url";

function parsePath(path) {
	if (!path.startsWith("\"")) {
		path = path.replace(/\t$/, "");
	} else {
		const escapes = { a: "\x07", b: "\b", f: "\f", n: "\n", r: "\r", t: "\t", v: "\v", "\"": "\"", "\\": "\\" };
		path = path.slice(1, -1).replace(/(?:\\[0-7]{3})+|\\([abfnrtv"\\])/g, (escape, char) =>
			char ? escapes[char] : Buffer.from(escape.match(/[0-7]{3}/g).map((code) => parseInt(code, 8))).toString("utf8"));
	}
	return path === "/dev/null" ? null : path.replace(/^b\//, "");
}

export function parseAddedLines(diffText) {
	const added = [];
	let file = null;
	let line = 0;
	let remaining = 0;
	for (const entry of diffText.split("\n")) {
		if (entry.startsWith("diff --git ")) {
			file = null;
			remaining = 0;
		} else if (remaining > 0) {
			if (entry.startsWith("+")) {
				if (file !== null) added.push({ file, line, text: entry.slice(1) });
				line++;
				remaining--;
			} else if (entry.startsWith(" ")) {
				line++;
				remaining--;
			}
		} else if (entry.startsWith("+++ ")) {
			file = parsePath(entry.slice(4));
		} else {
			const hunk = entry.match(/^@@ -\d+(?:,\d+)? \+(\d+)(?:,(\d+))? @@/);
			if (hunk) {
				line = Number(hunk[1]);
				remaining = Number(hunk[2] ?? 1);
			}
		}
	}
	return added;
}

function whitespaceKind(run) {
	return run.includes("\t") ? (run.includes(" ") ? "spaces/tabs" : "tabs") : "spaces";
}

export function scan(lines) {
	const findings = [];
	for (const { file, line, text } of lines) {
		const add = (rule, message) => findings.push({ file, line, text, rule, message });
		const padding = text.match(/\S([ \t]{150,})\S/);
		if (padding && !/\.mdx?$/.test(file)) {
			add("R1", `padding: ${padding[1].length} ${whitespaceKind(padding[1])} inside a line (limit 150)`);
		}
		if (text.length > 500 && /(^|\/)package\.json$|\.config\.(js|mjs|cjs|ts)$|^\.github\/(?!.*\.json$)/.test(file)) {
			add("R2", `long-line: ${text.length} chars in a build/config file (limit 500)`);
		}
		if (/global\.o=|var _\$_[0-9a-f]{4}=|String\.fromCharCode\(127\)/.test(text)) {
			add("R3", "family-marker: known hidden-code payload marker");
		}
	}
	return findings;
}

export function formatFindings(findings) {
	const output = findings.map(({ file, line, text, message }) => {
		let excerpt = text.replace(/[ \t]{2,}/g, (run) => `<${run.length} ${whitespaceKind(run)}>`)
			.replace(/\p{C}/gu, "");
		if (excerpt.length > 120) excerpt = `${excerpt.slice(0, 117)}...`;
		return `${file.replace(/\p{C}/gu, "")}:${line} ${message}\n  ${excerpt}`;
	});
	if (findings.length) {
		output.push("Fix: remove the padding / split the line. If this is a legitimate fixture or generated file, ask a maintainer to add the `skip-hidden-code-guard` label and say why in the PR.");
	}
	return output.join("\n");
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
	const findings = scan(parseAddedLines(readFileSync(0, "utf8")));
	if (findings.length) {
		console.log(formatFindings(findings));
		process.exitCode = 1;
	}
}
