import { useEffect, useRef, useState, type CSSProperties } from "react";
import { useTranslation } from "react-i18next";
import type { PDFDocumentLoadingTask, PDFDocumentProxy, RenderTask, TextLayer } from "pdfjs-dist";
import { formatBytes } from "./ReadOnlyFileView";
import { PanelMessage } from "./WorkspaceDiffView";

type Pdfjs = typeof import("pdfjs-dist");
type PageSize = { width: number; height: number };

const PAGE_GAP = 12;
const MAX_SCALE = 2;
/** The largest PDF read into memory for preview. */
const MAX_PDF_BYTES = 100 * 1024 * 1024;

class PdfTooLargeError extends Error {
	constructor(readonly size: number) {
		super("PDF too large to preview");
	}
}

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
 * its text can be selected. Pages draw as they near the viewport and release
 * their drawing once they are far from it. A file over MAX_PDF_BYTES is not read.
 */
export function PdfPreview({ firstPageOnly = false, onError, path, src }: { firstPageOnly?: boolean; onError: () => void; path: string; src: string }) {
	const { t } = useTranslation();
	const [loaded, setLoaded] = useState<{ pdf: PDFDocumentProxy; pdfjs: Pdfjs; sizes: PageSize[] } | null>(null);
	const [width, setWidth] = useState(0);
	const [tooLarge, setTooLarge] = useState<number | null>(null);
	const scrollRef = useRef<HTMLDivElement>(null);
	const onErrorRef = useRef(onError);
	onErrorRef.current = onError;

	useEffect(() => {
		let cancelled = false;
		let loadingTask: PDFDocumentLoadingTask | null = null;
		setLoaded(null);
		setTooLarge(null);
		void (async () => {
			const pdfjs = await loadPdfjs();
			const response = await fetch(src);
			if (!response.ok) throw new Error(`HTTP ${response.status}`);
			const declaredSize = Number(response.headers.get("content-length")) || 0;
			if (declaredSize > MAX_PDF_BYTES) {
				void response.body?.cancel();
				throw new PdfTooLargeError(declaredSize);
			}
			const data = new Uint8Array(await response.arrayBuffer());
			if (data.byteLength > MAX_PDF_BYTES) throw new PdfTooLargeError(data.byteLength);
			if (cancelled) return;
			loadingTask = pdfjs.getDocument({ data });
			const pdf = await loadingTask.promise;
			const sizes = await Promise.all(Array.from({ length: firstPageOnly ? Math.min(pdf.numPages, 1) : pdf.numPages }, async (_, index) => {
				const { width, height } = (await pdf.getPage(index + 1)).getViewport({ scale: 1 });
				return { width, height };
			}));
			if (!cancelled) setLoaded({ pdf, pdfjs, sizes });
		})().catch((error: unknown) => {
			if (cancelled) return;
			if (error instanceof PdfTooLargeError) setTooLarge(error.size);
			else onErrorRef.current();
		});
		return () => {
			cancelled = true;
			void loadingTask?.destroy();
		};
	}, [firstPageOnly, src]);

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

	if (tooLarge !== null) return <PanelMessage>{t("files.explorer.tooLarge", { size: formatBytes(tooLarge) })}</PanelMessage>;
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
	const drawnRef = useRef(false);

	useEffect(() => {
		const page = pageRef.current;
		if (!page) return;
		if (typeof IntersectionObserver === "undefined") {
			setNear(true);
			return;
		}
		const observer = new IntersectionObserver((entries) => {
			setNear(entries[entries.length - 1].isIntersecting);
		}, { root: root.current, rootMargin: "100% 0px" });
		observer.observe(page);
		return () => observer.disconnect();
	}, [root]);

	// A page far from the viewport drops its bitmap, text layer and parsed page data.
	useEffect(() => {
		const canvas = canvasRef.current;
		const text = textRef.current;
		if (near || !drawnRef.current || !canvas || !text) return;
		drawnRef.current = false;
		canvas.width = 0;
		canvas.height = 0;
		text.replaceChildren();
		void pdf.getPage(pageNumber).then((page) => page.cleanup()).catch(() => {});
	}, [near, pageNumber, pdf]);

	// pdf.js's end-of-content marker: while a drag selection is in progress it
	// covers the page so the selection doesn't jump to the end of the text.
	useEffect(() => {
		const text = textRef.current;
		if (!text) return;
		const start = () => text.classList.add("selecting");
		const end = () => text.classList.remove("selecting");
		text.addEventListener("pointerdown", start);
		document.addEventListener("pointerup", end);
		return () => {
			text.removeEventListener("pointerdown", start);
			document.removeEventListener("pointerup", end);
		};
	}, []);

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
			drawnRef.current = true;
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
			const endOfContent = document.createElement("div");
			endOfContent.className = "endOfContent";
			text.append(endOfContent);
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
