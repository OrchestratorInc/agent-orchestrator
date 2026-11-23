import { useEffect, useMemo, type RefObject } from "react";
import type { PDFDocumentProxy } from "pdfjs-dist";
import { useArtifactFindRegistration } from "../components/ArtifactFindScope";
import { createArtifactFindEngine, type ArtifactFindAdapter } from "../lib/artifact-find";

/** Search every page's text without eagerly painting its canvas or text layer. */
export function usePdfArtifactFind(pdf: PDFDocumentProxy | undefined, root: RefObject<HTMLDivElement | null>, enabled: boolean): void {
	const register = useArtifactFindRegistration();
	const engine = useMemo(createArtifactFindEngine, []);
	useEffect(() => {
		const element = root.current;
		if (!register || !pdf || !element || !enabled) return;
		const textCache = new Map<number, Promise<string>>();
		let pendingReveal: number | undefined;
		const adapter: ArtifactFindAdapter = {
			async search(query, signal) {
				const hits: Array<{ page: number; occurrence: number }> = [];
				let hasText = false, incomplete = false;
				for (let page = 1; page <= pdf.numPages; page++) {
					if (signal.aborted) throw new DOMException("Cancelled", "AbortError");
					let text = textCache.get(page);
					if (!text) {
						text = pdf.getPage(page).then((page) => page.getTextContent()).then((content) => content.items.map((item) => "str" in item ? item.str + (item.hasEOL ? "\n" : "") : "").join(""));
						textCache.set(page, text);
					}
					try {
						const content = await text;
						hasText ||= Boolean(content.trim());
						engine.occurrences(content, query).forEach((_, occurrence) => hits.push({ page, occurrence }));
					} catch { incomplete = true; }
				}
				if (signal.aborted) throw new DOMException("Cancelled", "AbortError");
				return {
					total: hits.length,
					warning: incomplete ? "partial" : hasText ? undefined : "noText",
					clear: engine.clear,
					paint(active, reveal) {
						const selected = hits[active];
						if (reveal && selected) pendingReveal = active;
						const ranges: Range[] = [];
						let activeRange: Range | undefined;
						for (const page of element.querySelectorAll<HTMLElement>("[data-page-number]")) {
							const layer = page.querySelector<HTMLElement>(".pdf-text-layer");
							if (!layer) continue;
							const matches = engine.ranges(layer, query);
							if (Number(page.dataset.pageNumber) === selected?.page) {
								activeRange = matches[selected.occurrence];
								if (pendingReveal === active && !activeRange) {
									page.scrollIntoView({ block: "center" });
									page.dispatchEvent(new Event("artifact-find-reveal"));
								}
							}
							ranges.push(...matches);
						}
						engine.paint(ranges, activeRange ? ranges.indexOf(activeRange) : -1, document);
						if (activeRange && pendingReveal === active) { engine.reveal(activeRange); pendingReveal = undefined; }
					},
				};
			},
		};
		const unregister = register(element, adapter);
		return () => { unregister(); engine.clear(); };
	}, [pdf, root, register, enabled, engine]);
}
