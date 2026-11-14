// Fine-grained imports keep unrelated grammars, bundled themes and WASM out of
// the renderer/worker chunks. This module itself is loaded only for known code.
import { createHighlighterCore, type ThemedToken, type ThemeRegistration } from "shiki/core";
import { createJavaScriptRegexEngine } from "shiki/engine/javascript";
import { ShikiStreamTokenizer } from "@shikijs/stream";
import bash from "@shikijs/langs/bash";
import css from "@shikijs/langs/css";
import diff from "@shikijs/langs/diff";
import go from "@shikijs/langs/go";
import html from "@shikijs/langs/html";
import javascript from "@shikijs/langs/javascript";
import jsx from "@shikijs/langs/jsx";
import json from "@shikijs/langs/json";
import jsonc from "@shikijs/langs/jsonc";
import markdown from "@shikijs/langs/markdown";
import python from "@shikijs/langs/python";
import rust from "@shikijs/langs/rust";
import sql from "@shikijs/langs/sql";
import typescript from "@shikijs/langs/typescript";
import tsx from "@shikijs/langs/tsx";
import xml from "@shikijs/langs/xml";
import yaml from "@shikijs/langs/yaml";
import type { ElementContent, Root } from "hast";
import type { GrammarName } from "./code-highlight";
import type { CodeLine } from "./code-highlight-protocol";

// Retain the existing syntax-class contract consumed by chat and word diffs.
// These CSS variables resolve in both AO themes, without re-tokenization.
const colorClasses = new Map([
	["var(--color-term-magenta)", "hljs-keyword"],
	["var(--color-term-green)", "hljs-string"],
	["var(--color-term-yellow)", "hljs-number"],
	["var(--color-term-blue)", "hljs-title"],
	["var(--color-term-cyan)", "hljs-type"],
	["var(--color-term-red)", "hljs-variable"],
	["var(--color-text-muted)", "hljs-params"],
]);
const theme = {
	name: "ao", type: "dark",
	colors: { "editor.foreground": "var(--color-foreground)", "editor.background": "var(--color-surface)" },
	tokenColors: [
		{ scope: ["keyword", "storage", "constant.language", "entity.name.tag"], settings: { foreground: "var(--color-term-magenta)" } },
		{ scope: ["string", "constant.character", "markup.inserted"], settings: { foreground: "var(--color-term-green)" } },
		{ scope: ["constant.numeric", "constant.other", "markup.list"], settings: { foreground: "var(--color-term-yellow)" } },
		{ scope: ["entity.name.function", "support.function", "markup.heading"], settings: { foreground: "var(--color-term-blue)" } },
		{ scope: ["entity.name.type", "support.type", "support.class"], settings: { foreground: "var(--color-term-cyan)" } },
		{ scope: ["variable", "entity.other.attribute-name", "support.type.property-name", "markup.deleted"], settings: { foreground: "var(--color-term-red)" } },
		{ scope: "comment", settings: { foreground: "var(--color-text-muted)", fontStyle: "italic" } },
		{ scope: "markup.bold", settings: { fontStyle: "bold" } },
		{ scope: "markup.italic", settings: { fontStyle: "italic" } },
	],
} satisfies ThemeRegistration;

function tokenNode(token: ThemedToken): ElementContent {
	const classes: string[] = [];
	const colorClass = token.color && colorClasses.get(token.color);
	if (colorClass) classes.push(colorClass === "hljs-params" && ((token.fontStyle ?? 0) & 1) ? "hljs-comment" : colorClass);
	if ((token.fontStyle ?? 0) & 1) classes.push("hljs-emphasis");
	if ((token.fontStyle ?? 0) & 2) classes.push("hljs-strong");
	if ((token.fontStyle ?? 0) & 4) classes.push("underline");
	const text = { type: "text", value: token.content } satisfies ElementContent;
	return classes.length ? { type: "element", tagName: "span", properties: { className: classes }, children: [text] } : text;
}

/** Restore the source's exact line endings after Shiki splits them for parsing. */
function codeLines(tokens: ThemedToken[][], source: string): CodeLine[] {
	const endings = source.match(/\r\n|\r|\n/g) ?? [];
	return tokens.map((line, index) => {
		const nodes = line.filter((token) => token.content !== "").map(tokenNode);
		if (endings[index]) nodes.push({ type: "text", value: endings[index] });
		return { tokens: nodes, signature: JSON.stringify(nodes) };
	});
}

export async function createEngine() {
	const highlighter = await createHighlighterCore({
		engine: createJavaScriptRegexEngine(), themes: [theme],
		langs: [bash, css, diff, go, html, javascript, jsx, json, jsonc, markdown, python, rust, sql, typescript, tsx, xml, yaml],
		langAlias: { bash: "shellscript" },
	});
	return {
		highlight(language: GrammarName, code: string): Root {
			const { tokens } = highlighter.codeToTokens(code.replace(/\r\n|\r/g, "\n"), { lang: language, theme: "ao" });
			return { type: "root", children: codeLines(tokens, code).flatMap((line) => line.tokens) };
		},
		createStream(language: GrammarName) {
			const tokenizer = new ShikiStreamTokenizer({ highlighter, lang: language, theme: "ao" });
			return async (chunk: string): Promise<CodeLine[]> => {
				// Callers supply complete lines. Only the appended chunk is parsed;
				// Shiki keeps the grammar state of the preceding stable line.
				const { stable } = await tokenizer.enqueue(chunk.replace(/\r\n|\r/g, "\n"));
				const lines: ThemedToken[][] = [[]];
				for (const token of stable) {
					if (token.content === "\n") lines.push([]);
					else lines[lines.length - 1].push(token);
				}
				// The stream tokenizer also retains stable tokens for its stock
				// renderers. AO owns those lines after delivery; retain grammar only.
				tokenizer.tokensStable = [];
				lines.pop();
				return codeLines(lines, chunk);
			};
		},
	};
}

export type CodeEngine = Awaited<ReturnType<typeof createEngine>>;
