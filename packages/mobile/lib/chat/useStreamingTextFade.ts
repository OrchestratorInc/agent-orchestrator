import { useLayoutEffect, useRef, useState } from "react";

type TextRange = { start: number; end: number };
export type TextFade = TextRange & { opacity: number };
type Reveal = TextRange & { at: number };

// Match the desktop Markdown renderer's fade-in duration.
const FADE_MS = 300;
const NO_FADES: TextFade[] = [];

/** Fade each committed append in rendered text, through the last buffered update. */
export function useStreamingTextFade(text: string | undefined, streaming: boolean): TextFade[] {
	const previous = useRef({ text, streaming });
	const reveals = useRef<Reveal[]>([]);
	const [fades, setFades] = useState(NO_FADES);

	useLayoutEffect(() => {
		const before = previous.current;
		previous.current = { text, streaming };
		const now = performance.now();
		if (text === undefined || before.text === undefined || !text.startsWith(before.text)) {
			reveals.current = [];
		} else if ((streaming || before.streaming) && text.length > before.text.length) {
			reveals.current.push({ start: before.text.length, end: text.length, at: now });
		}

		let frame: number | undefined;
		const paint = (at: number) => {
			reveals.current = reveals.current.filter((reveal) => at - reveal.at < FADE_MS);
			setFades(reveals.current.length ? reveals.current.map(({ start, end, at: revealedAt }) => {
				const remaining = 1 - (at - revealedAt) / FADE_MS;
				return { start, end, opacity: 1 - remaining ** 2 };
			}) : NO_FADES);
			if (reveals.current.length) frame = requestAnimationFrame(() => paint(performance.now()));
		};
		paint(now);
		return () => { if (frame !== undefined) cancelAnimationFrame(frame); };
	}, [streaming, text]);

	return fades;
}
