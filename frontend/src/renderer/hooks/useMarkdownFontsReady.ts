import { useEffect, useState } from "react";

const MARKDOWN_FONTS = ['400 16px "Geist Variable"', '400 16px "Geist Mono Variable"'];
let loading: Promise<void> | undefined;

export function preloadMarkdownFonts(): Promise<void> {
	if (!document.fonts) return Promise.resolve();
	loading ??= Promise.allSettled(MARKDOWN_FONTS.map((font) => document.fonts.load(font))).then(() => undefined);
	return loading;
}

export function useMarkdownFontsReady(): boolean {
	const [ready, setReady] = useState(() => !document.fonts || MARKDOWN_FONTS.every((font) => document.fonts.check(font)));
	useEffect(() => {
		if (ready) return;
		let active = true;
		void preloadMarkdownFonts().then(() => { if (active) setReady(true); });
		return () => { active = false; };
	}, [ready]);
	return ready;
}
