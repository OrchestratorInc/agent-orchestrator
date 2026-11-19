/**
 * Shared syntax highlighting for chat, Markdown previews and file diffs, on
 * Pierre's WebAssembly highlighter. Token colours are AO's terminal palette as
 * CSS variables, so a theme switch repaints without re-tokenizing.
 */

import type { ElementContent, Root } from "hast";
import { codeToTokens, isSupportedLanguage, type Lang, type Theme, type ThemedToken } from "@pierre/highlights";
import type { CodeHighlighterPlugin, CodeOptions } from "@pierre/words/html";
import { createHighlightsPlugin } from "@pierre/words/highlights";

export type GrammarName = Lang;

export type CodeLine = { tokens: ElementContent[]; signature: string };

const ALIASES: Record<string, Lang> = { console: "bash", udiff: "diff" };

/** The grammar for a fence label or file extension, or undefined to render plain. */
export function canonicalLanguage(label: string | undefined): GrammarName | undefined {
	if (!label) return undefined;
	const name = label.toLowerCase();
	const lang = ALIASES[name] ?? name;
	return lang !== "ansi" && isSupportedLanguage(lang) ? lang : undefined;
}

const CODE_THEME: Theme = {
	name: "ao",
	appearance: "dark",
	cssVariables: { prefix: "--ao-code-" },
	style: {
		syntax: {
			keyword: "keyword",
			boolean: "keyword",
			"constant.builtin": "keyword",
			tag: "keyword",
			selector: "keyword",
			string: "string",
			"diff.plus": "string",
			number: "number",
			constant: "number",
			"punctuation.list_marker": "number",
			function: "title",
			title: "title",
			type: "type",
			constructor: "type",
			namespace: "type",
			"tag.component.jsx": "type",
			variable: "variable",
			property: "variable",
			attribute: "variable",
			"selector.class": "variable",
			"selector.id": "variable",
			"selector.pseudo": "variable",
			"diff.minus": "variable",
			comment: { color: "comment", font_style: "italic" },
			emphasis: { font_style: "italic" },
			"emphasis.strong": { font_weight: 600 },
		},
	},
};

const SLOT_CLASS = new Map(["keyword", "string", "number", "title", "type", "variable", "comment"].map((slot) => [`var(--ao-code-${slot})`, `hljs-${slot}`]));

function tokenNode(token: ThemedToken): ElementContent {
	const classes: string[] = [];
	const slot = token.color && SLOT_CLASS.get(token.color);
	if (slot) classes.push(slot);
	if ((token.fontStyle ?? 0) & 1) classes.push("hljs-emphasis");
	if ((token.fontStyle ?? 0) & 2) classes.push("hljs-strong");
	const text = { type: "text", value: token.content } satisfies ElementContent;
	return classes.length ? { type: "element", tagName: "span", properties: { className: classes }, children: [text] } : text;
}

const MAX_CHARS = 500_000;

/** The highlighted tree, keeping the source's exact line endings. */
export function highlightSync(code: string, language: GrammarName): Root | undefined {
	if (code.length > MAX_CHARS) return undefined;
	try {
		const { tokens } = codeToTokens(code.replace(/\r\n|\r/g, "\n"), { lang: language, theme: CODE_THEME });
		const endings = code.match(/\r\n|\r|\n/g) ?? [];
		return {
			type: "root",
			children: tokens.flatMap((line, index) => {
				const nodes = line.filter((token) => token.content !== "").map(tokenNode);
				if (endings[index]) nodes.push({ type: "text", value: endings[index] });
				return nodes;
			}),
		};
	} catch (cause) {
		console.warn(`chat: could not highlight ${language}`, cause);
		return undefined;
	}
}

const pierrePlugin = createHighlightsPlugin({ theme: CODE_THEME });

const withGrammar = (options: CodeOptions): CodeOptions => ({ ...options, language: canonicalLanguage(options.language) ?? "" });

export const streamingCodeHighlighter: CodeHighlighterPlugin = {
	highlight: (options) => pierrePlugin.highlight(withGrammar(options)),
	createSession() {
		const session = pierrePlugin.createSession!();
		return {
			highlight: (options) => session.highlight(withGrammar(options)),
			highlightLines: (options) => session.highlightLines!(withGrammar(options)),
			dispose: () => session.dispose(),
		};
	},
};
