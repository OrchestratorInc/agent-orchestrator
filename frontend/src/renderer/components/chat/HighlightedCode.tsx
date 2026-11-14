import { memo, useEffect, useMemo, useReducer, useRef, useState, type ReactNode } from "react";
import type { ElementContent, Root, RootContent } from "hast";
import { highlight, highlightSync, createCodeHighlightStream, type GrammarName } from "../../lib/code-highlight";
import "./code-theme.css";
import type { CodeLine, StreamingCode } from "../../lib/code-highlight-protocol";

export const HighlightedCode = memo(function HighlightedCode({
	code,
	language,
	streaming,
}: {
	code: string;
	language?: GrammarName;
	streaming?: boolean;
}) {
	const tokens = useHighlighted(code, language, Boolean(streaming));
	const snapshot = useStreamingHighlight(code, language, Boolean(streaming));
	const prefix = snapshot && snapshot.language === language && code.startsWith(snapshot.source) ? snapshot : undefined;
	const settledLines = useMemo(() => tokens ? tokenLines(tokens.children).map((tokens) => ({ tokens, signature: JSON.stringify(tokens) })) : undefined, [tokens]);
	const lines = settledLines ?? prefix?.lines ?? [];
	const plain = tokens ? "" : code.slice(prefix?.source.length ?? 0);
	return <>{lines.map((line, index) => <HighlightedLine key={index} {...line} />)}{plain}</>;
});

function useStreamingHighlight(code: string, language: GrammarName | undefined, streaming: boolean) {
	const completed = code.slice(0, code.lastIndexOf("\n") + 1);
	const [snapshot, setSnapshot] = useState<StreamingCode>();
	const [busy, setBusy] = useState(false);
	const attempted = useRef<{ code: string; language: GrammarName }>(undefined);
	const next = useRef<{ code: string; language: GrammarName }>(undefined);
	const timer = useRef<ReturnType<typeof setTimeout>>(undefined);
	const mounted = useRef(false);
	const stream = useRef<ReturnType<typeof createCodeHighlightStream>>(undefined);
	useEffect(() => {
		mounted.current = true;
		stream.current = createCodeHighlightStream();
		attempted.current = undefined;
		return () => {
			mounted.current = false;
			clearTimeout(timer.current);
			timer.current = undefined;
			stream.current?.dispose();
			stream.current = undefined;
		};
	}, []);
	useEffect(() => {
		next.current = streaming && language && completed ? { code: completed, language } : undefined;
		if (!next.current || busy || timer.current !== undefined) return;
		if (attempted.current?.code === completed && attempted.current.language === language) return;
		timer.current = setTimeout(() => {
			timer.current = undefined;
			const request = next.current;
			const session = stream.current;
			if (!request || !session) return;
			attempted.current = request;
			setBusy(true);
			void session.update(request.code, request.language).then((result) => {
				if (!mounted.current) return;
				if (result && stream.current === session) setSnapshot(result);
				setBusy(false);
			});
		}, 50);
	}, [completed, language, streaming, busy]);
	return snapshot;
}

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

function useHighlighted(
	code: string,
	language: GrammarName | undefined,
	streaming: boolean,
): Root | undefined {
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

