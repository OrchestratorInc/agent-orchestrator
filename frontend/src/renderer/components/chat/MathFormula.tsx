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
