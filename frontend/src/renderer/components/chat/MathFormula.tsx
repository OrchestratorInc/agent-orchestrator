/**
 * One TeX formula, typeset by KaTeX.
 *
 * Plain-first, like mermaid and syntax highlighting: the TeX source shows until
 * the KaTeX chunk arrives, and stays when KaTeX cannot parse it — a formula
 * still streaming in, or a command KaTeX does not support. Half-typed TeX is
 * readable as source; an error box that reads as content is not.
 *
 * `trust: false` keeps `\href`, `\url`, `\htmlClass` and the like from emitting
 * anything active, and KaTeX escapes the TeX it renders. That is what makes
 * the one `dangerouslySetInnerHTML` here safe for agent-authored input.
 *
 * Copying a selection that touches a formula copies its TeX from `data-tex`
 * (see `lib/katex.ts`), not the glyphs twice over from KaTeX's visual and
 * MathML halves.
 */

import { memo, useEffect, useMemo, useState } from "react";

type Katex = typeof import("katex").default;

let katex: Katex | undefined;
let katexPromise: Promise<Katex> | undefined;

function loadKatex(): Promise<Katex> {
	katexPromise ??= import("../../lib/katex").then((module) => (katex = module.default));
	return katexPromise;
}

function typeset(engine: Katex, tex: string, display: boolean): string | undefined {
	try {
		return engine.renderToString(tex, {
			displayMode: display,
			throwOnError: true,
			trust: false,
			strict: "ignore",
			maxSize: 20,
			maxExpand: 1000,
		});
	} catch {
		return undefined;
	}
}

export const MathFormula = memo(function MathFormula({ tex, display = false }: { tex: string; display?: boolean }) {
	const [engine, setEngine] = useState(katex);
	useEffect(() => {
		if (engine) return;
		let live = true;
		loadKatex().then(
			(loaded) => live && setEngine(loaded),
			() => {},
		);
		return () => {
			live = false;
		};
	}, [engine]);

	const source = tex.trim();
	const html = useMemo(() => (engine ? typeset(engine, source, display) : undefined), [engine, source, display]);
	const content = html ? (
		<span dangerouslySetInnerHTML={{ __html: html }} />
	) : (
		<span className="whitespace-pre-wrap">{display ? `$$\n${source}\n$$` : `$${source}$`}</span>
	);

	// A display formula scrolls sideways rather than widen the conversation
	// column. KaTeX's struts overhang their box by a pixel or two, so the
	// vertical axis is clipped (with padding to spare) instead of growing a
	// scrollbar. Inline formulas stay inline: KaTeX breaks them at operators,
	// and a scroll box would shift the baseline and add a scrollbar per formula.
	return display ? (
		<div className="my-2.5 overflow-x-auto overflow-y-hidden py-1 [&_.katex-display]:m-0" data-math="display" data-tex={source}>
			{content}
		</div>
	) : (
		<span data-math="inline" data-tex={source}>
			{content}
		</span>
	);
});
