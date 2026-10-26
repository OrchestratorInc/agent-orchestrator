import { useEffect, useRef, useState, type CSSProperties } from "react";
import { useTranslation } from "react-i18next";
import type { PDFDocumentLoadingTask, PDFDocumentProxy, RenderTask, TextLayer } from "pdfjs-dist";
import { PanelMessage } from "./WorkspaceDiffView";

type Pdfjs = typeof import("pdfjs-dist");
type PageSize = { width: number; height: number };

const PAGE_GAP = 12;
const MAX_SCALE = 2;

let pdfjsModule: Promise<Pdfjs> | null = null;

/** Loads pdf.js and its worker on first use. */
function loadPdfjs(): Promise<Pdfjs> {
	pdfjsModule ??= Promise.all([import("pdfjs-dist"), import("pdfjs-dist/build/pdf.worker.min.mjs?url")]).then(([pdfjs, worker]) => {
		pdfjs.GlobalWorkerOptions.workerSrc = worker.default;
		return pdfjs;
	});
	return pdfjsModule;
}

/**
 * A PDF's pages, fit to the pane width, with a text layer over each page so
 * its text can be selected. Pages draw as they near the viewport.
 */
export function PdfPreview({ onError, path, src }: { onError: () => void; path: string; src: string }) {
	const { t } = useTranslation();
	const [loaded, setLoaded] = useState<{ pdf: PDFDocumentProxy; pdfjs: Pdfjs; sizes: PageSize[] } | null>(null);
	const [width, setWidth] = useState(0);
	const scrollRef = useRef<HTMLDivElement>(null);
	const onErrorRef = useRef(onError);
	onErrorRef.current = onError;

	useEffect(() => {
		let cancelled = false;
		let loadingTask: PDFDocumentLoadingTask | null = null;
		setLoaded(null);
		void (async () => {
			const pdfjs = await loadPdfjs();
			const response = await fetch(src);
			if (!response.ok) throw new Error(`HTTP ${response.status}`);
			const data = new Uint8Array(await response.arrayBuffer());
			if (cancelled) return;
			loadingTask = pdfjs.getDocument({ data });
			const pdf = await loadingTask.promise;
			const sizes = await Promise.all(Array.from({ length: pdf.numPages }, async (_, index) => {
				const { width, height } = (await pdf.getPage(index + 1)).getViewport({ scale: 1 });
				return { width, height };
			}));
			if (!cancelled) setLoaded({ pdf, pdfjs, sizes });
		})().catch(() => {
			if (!cancelled) onErrorRef.current();
		});
		return () => {
			cancelled = true;
			void loadingTask?.destroy();
		};
	}, [src]);

	useEffect(() => {
		const element = scrollRef.current;
		if (!element || typeof ResizeObserver === "undefined") return;
		let timer: number | null = null;
		let measured = false;
		const observer = new ResizeObserver(([entry]) => {
			if (timer !== null) window.clearTimeout(timer);
			// A drag-resize settles before the pages redraw at the new width.
			timer = window.setTimeout(() => setWidth(Math.floor(entry.contentRect.width)), measured ? 120 : 0);
			measured = true;
		});
		observer.observe(element);
		return () => {
			if (timer !== null) window.clearTimeout(timer);
			observer.disconnect();
		};
	}, []);

	return (
		<div aria-label={path} className="board-scrollbar min-h-0 min-w-0 flex-1 overflow-auto bg-surface p-3" ref={scrollRef} role="document">
			{!loaded ? (
				<PanelMessage>{t("files.loading")}</PanelMessage>
			) : width > 0 ? (
				<div className="flex flex-col items-center" data-selection-feedback="" style={{ gap: PAGE_GAP }}>
					{loaded.sizes.map((size, index) => (
						<PdfPage key={index} pageNumber={index + 1} pdf={loaded.pdf} pdfjs={loaded.pdfjs} root={scrollRef} scale={Math.min(width / size.width, MAX_SCALE)} size={size} />
					))}
				</div>
			) : null}
		</div>
	);
}

function PdfPage({ pageNumber, pdf, pdfjs, root, scale, size }: {
	pageNumber: number;
	pdf: PDFDocumentProxy;
	pdfjs: Pdfjs;
	root: React.RefObject<HTMLDivElement | null>;
	scale: number;
	size: PageSize;
}) {
	const pageRef = useRef<HTMLDivElement>(null);
	const canvasRef = useRef<HTMLCanvasElement>(null);
	const textRef = useRef<HTMLDivElement>(null);
	const [near, setNear] = useState(false);

	useEffect(() => {
		const page = pageRef.current;
		if (!page || near) return;
		if (typeof IntersectionObserver === "undefined") {
			setNear(true);
			return;
		}
		const observer = new IntersectionObserver((entries) => {
			if (entries.some((entry) => entry.isIntersecting)) setNear(true);
		}, { root: root.current, rootMargin: "100% 0px" });
		observer.observe(page);
		return () => observer.disconnect();
	}, [near, root]);

	useEffect(() => {
		const canvas = canvasRef.current;
		const text = textRef.current;
		if (!near || !canvas || !text) return;
		let cancelled = false;
		let renderTask: RenderTask | null = null;
		let textLayer: TextLayer | null = null;
		void (async () => {
			const page = await pdf.getPage(pageNumber);
			if (cancelled) return;
			const viewport = page.getViewport({ scale });
			const ratio = window.devicePixelRatio || 1;
			canvas.width = Math.floor(viewport.width * ratio);
			canvas.height = Math.floor(viewport.height * ratio);
			renderTask = page.render({ canvas, viewport, transform: ratio === 1 ? undefined : [ratio, 0, 0, ratio, 0, 0] });
			await renderTask.promise;
			if (cancelled) return;
			text.replaceChildren();
			textLayer = new pdfjs.TextLayer({ container: text, textContentSource: page.streamTextContent(), viewport });
			await textLayer.render();
		})().catch(() => {
			// A cancelled or failed page keeps its blank sheet; the others still draw.
		});
		return () => {
			cancelled = true;
			renderTask?.cancel();
			textLayer?.cancel();
		};
	}, [near, pageNumber, pdf, pdfjs, scale]);

	const width = Math.floor(size.width * scale);
	const height = Math.floor(size.height * scale);
	return (
		<div
			className="relative shrink-0 overflow-hidden rounded-xs bg-white"
			data-page-number={pageNumber}
			ref={pageRef}
			style={{ height, width, "--total-scale-factor": scale, "--scale-round-x": "1px", "--scale-round-y": "1px" } as CSSProperties}
		>
			<canvas aria-hidden="true" className="absolute inset-0 size-full" ref={canvasRef} />
			<div className="pdf-text-layer textLayer" ref={textRef} />
		</div>
	);
}
