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

import { memo, useEffect, useMemo, useRef, type ReactNode } from "react";
import type { ElementContent, RootContent } from "hast";
import type { LiveTokenizer } from "@pierre/highlights";
import { createLiveHighlight, highlightSync, liveLine, type CodeLine, type GrammarName } from "../../lib/code-highlight";
// The token colours live with the engine rather than with one caller: they are
// scoped under `.chat-code`/`.markdown-code`, so anything rendering these class
// names has to be inside one of those containers and has to have loaded this sheet.
import "./code-theme.css";

/**
 * Code, highlighted when it can be, plain otherwise.
 *
 * Memoized because the timeline re-renders on every poll: a settled block's props
 * do not change, so it should not walk its token tree again once a second.
 */
export const HighlightedCode = memo(function HighlightedCode({
	code,
	language,
	streaming,
}: {
	code: string;
	/** Undefined renders plain monospace. */
	language?: GrammarName;
	streaming?: boolean;
}) {
	const live = useLiveLines(code, streaming ? language : undefined);
	const settled = useMemo(() => {
		const tree = !streaming && language ? highlightSync(code, language) : undefined;
		return tree ? tokenLines(tree.children).map((tokens) => ({ tokens, signature: JSON.stringify(tokens) })) : undefined;
	}, [code, language, streaming]);
	const lines = live ?? settled;
	return lines ? <>{lines.map((line, index) => <HighlightedLine key={index} {...line} />)}</> : <>{code}</>;
});

type Live = { tokenizer: LiveTokenizer; code: string; language: GrammarName; lines: CodeLine[] };

function useLiveLines(code: string, language: GrammarName | undefined): CodeLine[] | undefined {
	const live = useRef<Live>(undefined);
	useEffect(() => () => {
		live.current?.tokenizer.dispose();
		live.current = undefined;
	}, []);
	const previous = live.current;
	if (previous?.code === code && previous.language === language) return previous.lines;
	if (language && previous?.language === language && code.startsWith(previous.code)) {
		const { tokenizer } = previous;
		const last = tokenizer.lineCount - 1;
		const end = { line: last, character: tokenizer.getLineLength(last) };
		const { lineChanges } = tokenizer.applyEdits([{ range: { start: end, end }, newText: code.slice(previous.code.length) }]);
		const lines = previous.lines.slice();
		for (const change of [...lineChanges].reverse()) {
			const changed = Array.from({ length: change.newEndLine - change.newStartLine }, (_, index) => liveLine(tokenizer, change.newStartLine + index));
			lines.splice(change.oldStartLine, change.oldEndLine - change.oldStartLine, ...changed);
		}
		live.current = { tokenizer, code, language, lines };
		return lines;
	}
	previous?.tokenizer.dispose();
	const tokenizer = language ? createLiveHighlight(code, language) : undefined;
	if (!tokenizer || !language) {
		live.current = undefined;
		return undefined;
	}
	const lines = Array.from({ length: tokenizer.lineCount }, (_, line) => liveLine(tokenizer, line));
	live.current = { tokenizer, code, language, lines };
	return lines;
}

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
