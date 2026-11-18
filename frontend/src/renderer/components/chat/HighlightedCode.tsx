/**
 * Tokenized code as React elements.
 *
 * Extracted from `ChatMarkdown` so a patch can be highlighted by the same engine as
 * a fenced block. Two highlighters in one surface would mean two grammar payloads,
 * two caches, and two chances for a code block and a diff to disagree about what
 * `go` looks like.
 *
 * It renders the token content only — no `pre`, no chrome — because the two callers
 * frame it differently: a fence has a language header and copy controls, a patch sits
 * inside a file row. Everything about how the tree is produced is shared; nothing
 * about how it is framed is.
 */

import { memo, useEffect, useMemo, useReducer, type ReactNode } from "react";
import type { ElementContent, Root, RootContent } from "hast";
import { highlight, highlightSync, type GrammarName } from "../../lib/code-highlight";
// The token colours live with the engine rather than with one caller: they are
// scoped under `.chat-code`/`.markdown-code`, so anything rendering these class
// names has to be inside one of those containers and has to have loaded this sheet.
import "./code-theme.css";
import type { CodeLine } from "../../lib/code-highlight-protocol";

/**
 * Code, highlighted when it can be, plain otherwise.
 *
 * Memoized because the timeline re-renders on every poll: a settled block's props
 * do not change, so it should not walk its token tree again once a second.
 */
export const HighlightedCode = memo(function HighlightedCode({
	code,
	language,
}: {
	code: string;
	/** Undefined renders plain monospace and loads no grammar at all. */
	language?: GrammarName;
}) {
	const tokens = useHighlighted(code, language);
	const lines = useMemo(() => tokens ? tokenLines(tokens.children).map((tokens) => ({ tokens, signature: JSON.stringify(tokens) })) : undefined, [tokens]);
	return lines ? <>{lines.map((line, index) => <HighlightedLine key={index} {...line} />)}</> : <>{code}</>;
});

// Split the tree, preserving nested scopes on each line. Putting ancestor and
// child classes on the same span changes CSS inheritance and selector matching.
function tokenLines(nodes: readonly RootContent[]): ElementContent[][] {
	const split = (children: readonly RootContent[]): ElementContent[][] => {
		const lines: ElementContent[][] = [[]];
		for (const node of children) {
			if (node.type === "element") {
				const pieces = split(node.children);
				for (let i = 0; i < pieces.length; i++) {
					if (i > 0) lines.push([]);
					if (pieces[i].length > 0) lines[lines.length - 1].push({ ...node, children: pieces[i] });
				}
			} else if (node.type === "text") {
				const pieces = node.value.split("\n");
				for (let i = 0; i < pieces.length; i++) {
					if (i > 0) lines.push([]);
					const text = pieces[i] + (i < pieces.length - 1 ? "\n" : "");
					if (text) lines[lines.length - 1].push({ type: "text", value: text });
				}
			}
		}
		return lines;
	};
	const lines = split(nodes);
	if (lines[lines.length - 1].length === 0) lines.pop();
	return lines;
}

function renderTokenNodes(nodes: readonly ElementContent[]): ReactNode {
	return nodes.map((node, index) => {
		if (node.type === "text") return node.value;
		if (node.type !== "element") return null;
		const className = node.properties.className;
		return <span key={index} className={Array.isArray(className) ? className.join(" ") : undefined}>
			{renderTokenNodes(node.children)}
		</span>;
	});
}

const HighlightedLine = memo(function HighlightedLine({ tokens }: CodeLine) {
	return <span>{renderTokenNodes(tokens)}</span>;
}, (previous, next) => previous.signature === next.signature);

/**
 * The token tree for a settled block, as soon as one can be had.
 *
 * Two paths, because the flash between them is the whole problem: a cached or
 * already-warm block is highlighted in its first render, and only a cold one renders
 * plain for a beat while the grammars load. The cache is the state — the async path
 * writes into it and this re-reads it, so nothing here can hold a tree that disagrees
 * with the one the next block will get.
 */
function useHighlighted(
	code: string,
	language: GrammarName | undefined,
): Root | undefined {
	const wanted = Boolean(language);
	const tokens = wanted ? highlightSync(code, language!) : undefined;
	const [, reread] = useReducer((count: number) => count + 1, 0);

	useEffect(() => {
		if (!wanted || tokens) return;
		let live = true;
		void highlight(code, language!).then(() => {
			if (live) reread();
		});
		return () => {
			live = false;
		};
	}, [code, language, wanted, tokens]);

	return tokens;
}

