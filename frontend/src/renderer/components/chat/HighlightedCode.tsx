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

import { memo, useEffect, useMemo, useReducer, useRef, useState, type ReactNode } from "react";
import type { ElementContent, Root, RootContent } from "hast";
import { highlight, highlightSync, highlightStreaming, type GrammarName } from "../../lib/code-highlight";
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
	/** Undefined renders plain monospace and loads no grammar at all. */
	language?: GrammarName;
	/** Text still arriving; highlight completed lines in the worker. */
	streaming?: boolean;
}) {
	const tokens = useHighlighted(code, language, Boolean(streaming));
	const snapshot = useStreamingHighlight(code, language, Boolean(streaming));
	const prefix = snapshot && snapshot.language === language && code.startsWith(snapshot.code) ? snapshot : undefined;
	const tree = tokens ?? prefix?.tree;
	const lines = useMemo(() => tree ? tokenLines(tree.children).map((tokens) => ({ tokens, signature: JSON.stringify(tokens) })) : [], [tree]);
	const plain = tokens ? "" : code.slice(prefix?.code.length ?? 0);
	return <>{lines.map((line, index) => <HighlightedLine key={index} {...line} />)}{plain}</>;
});

type HighlightSnapshot = { code: string; language: GrammarName; tree: Root };

function useStreamingHighlight(code: string, language: GrammarName | undefined, streaming: boolean) {
	const completed = code.slice(0, code.lastIndexOf("\n") + 1);
	const [snapshot, setSnapshot] = useState<HighlightSnapshot>();
	const [busy, setBusy] = useState(false);
	const attempted = useRef<{ code: string; language: GrammarName }>(undefined);
	const next = useRef<{ code: string; language: GrammarName }>(undefined);
	const timer = useRef<ReturnType<typeof setTimeout>>(undefined);
	const mounted = useRef(false);
	useEffect(() => {
		mounted.current = true;
		return () => { mounted.current = false; clearTimeout(timer.current); timer.current = undefined; };
	}, []);
	useEffect(() => {
		next.current = streaming && language && completed ? { code: completed, language } : undefined;
		if (!next.current || busy || timer.current !== undefined) return;
		if (attempted.current?.code === completed && attempted.current.language === language) return;
		// At most one request per fence. Coalesce bursts without postponing work
		// indefinitely during a continuous stream. A completion schedules only
		// the newest prefix; failed snapshots are not retried on every render.
		timer.current = setTimeout(() => {
			timer.current = undefined;
			const request = next.current;
			if (!request) return;
			attempted.current = request;
			setBusy(true);
			void highlightStreaming(request.code, request.language).then((tree) => {
				if (!mounted.current) return;
				if (tree) setSnapshot({ ...request, tree });
				setBusy(false);
			});
		}, 50);
	}, [completed, language, streaming, busy]);
	return snapshot;
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

const HighlightedLine = memo(function HighlightedLine({ tokens }: { signature: string; tokens: ElementContent[] }) {
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
	streaming: boolean,
): Root | undefined {
	// Live snapshots have their own worker path and never fill the settled LRU.
	const wanted = Boolean(language) && !streaming;
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

