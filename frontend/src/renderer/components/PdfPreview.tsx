import { useEffect, useLayoutEffect, useRef, useState, type CSSProperties } from "react";
import { useTranslation } from "react-i18next";
import type { PDFDocumentLoadingTask, PDFDocumentProxy, RenderTask, TextLayer } from "pdfjs-dist";
import { useArtifactZoomTarget } from "../lib/artifact-zoom";
import { aoBridge } from "../lib/bridge";
import { formatBytes } from "./ReadOnlyFileView";
import { PanelMessage } from "./WorkspaceDiffView";

type Pdfjs = typeof import("pdfjs-dist");
type PageSize = { width: number; height: number };

const PAGE_GAP = 12;
const MAX_CANVAS_PIXELS = 2 ** 25;
const MAX_CANVAS_SIDE = 16_384;

export function pdfOutputScale(width: number, height: number, pixelRatio: number): number {
	if (width <= 0 || height <= 0) return pixelRatio;
	return Math.min(pixelRatio, Math.sqrt(MAX_CANVAS_PIXELS / (width * height)), MAX_CANVAS_SIDE / width, MAX_CANVAS_SIDE / height);
}
const PADDING = 12;
const MAX_SCALE = 2;
const REDRAW_DELAY_MS = 150;
const MAX_PDF_BYTES = 100 * 1024 * 1024;

class PdfTooLargeError extends Error {
	constructor(readonly size: number) {
		super("PDF too large to preview");
	}
}

export function anchoredPdfScrollTop(scrollTop: number, anchorY: number, before: readonly number[], after: readonly number[], gap = PAGE_GAP, padding = PADDING): number {
	const y = scrollTop + anchorY - padding;
	let top = 0;
	let nextTop = 0;
	let page = 0;
	while (page < before.length - 1 && y >= top + before[page] + gap) {
		top += before[page] + gap;
		nextTop += after[page] + gap;
		page += 1;
	}
	const height = before[page] ?? 0;
	const within = Math.min(y - top, height);
	const scaled = height > 0 ? within * (after[page] ?? 0) / height : within;
	return nextTop + scaled + (y - top - within) + padding - anchorY;
}

let pdfjsModule: Promise<Pdfjs> | null = null;

function loadPdfjs(): Promise<Pdfjs> {
	pdfjsModule ??= Promise.all([import("pdfjs-dist"), import("pdfjs-dist/build/pdf.worker.min.mjs?url")]).then(([pdfjs, worker]) => {
		pdfjs.GlobalWorkerOptions.workerSrc = worker.default;
		return pdfjs;
	});
	return pdfjsModule;
}

export function PdfPreview({ cloud = false, cover = false, firstPageOnly = false, load, onError, path, src }: {
	cloud?: boolean;
	cover?: boolean;
	firstPageOnly?: boolean;
	load?: () => Promise<Uint8Array>;
	onError: () => void;
	path: string;
	src: string;
}) {
	const { t } = useTranslation();
	const [loaded, setLoaded] = useState<{ pdf: PDFDocumentProxy; pdfjs: Pdfjs; sizes: PageSize[] } | null>(null);
	const [width, setWidth] = useState(0);
	const [height, setHeight] = useState(0);
	const [tooLarge, setTooLarge] = useState<number | null>(null);
	const scrollRef = useRef<HTMLDivElement>(null);
	const onErrorRef = useRef(onError);
	onErrorRef.current = onError;
	const loadRef = useRef(load);
	loadRef.current = load;
	const { controller: zoomController, zoom } = useArtifactZoomTarget(!firstPageOnly);
	const zoomRef = useRef(zoom);

	useLayoutEffect(() => {
		const previous = zoomRef.current;
		zoomRef.current = zoom;
		const element = scrollRef.current;
		const anchor = zoomController?.takeAnchor();
		if (!element || previous === zoom) return;
		const rect = element.getBoundingClientRect();
		const ax = anchor ? anchor.clientX - rect.left : element.clientWidth / 2;
		const ay = anchor ? anchor.clientY - rect.top : element.clientHeight / 2;
		const ratio = zoom / previous;
		element.scrollLeft = (element.scrollLeft + ax - PADDING) * ratio + PADDING - ax;
		const heights = (level: number) => loaded && width > 0 ? loaded.sizes.map((size) => Math.floor(size.height * (Math.min(width / size.width, MAX_SCALE) * level))) : [];
		const before = heights(previous);
		element.scrollTop = before.length > 0
			? anchoredPdfScrollTop(element.scrollTop, ay, before, heights(zoom))
			: (element.scrollTop + ay - PADDING) * ratio + PADDING - ay;
	}, [loaded, width, zoom, zoomController]);

	useEffect(() => {
		let cancelled = false;
		let loadingTask: PDFDocumentLoadingTask | null = null;
		setLoaded(null);
		setTooLarge(null);
		void (async () => {
			const pdfjs = await loadPdfjs();
			const data = loadRef.current ? await loadRef.current() : cloud ? await aoBridge.cloudArtifact.fetch(src) : await readPdf(src);
			if (data.byteLength > MAX_PDF_BYTES) throw new PdfTooLargeError(data.byteLength);
			if (cancelled) return;
			loadingTask = pdfjs.getDocument({ data });
			const pdf = await loadingTask.promise;
			const sizes = await Promise.all(Array.from({ length: firstPageOnly || cover ? Math.min(pdf.numPages, 1) : pdf.numPages }, async (_, index) => {
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
	}, [cloud, cover, firstPageOnly, src]);

	useEffect(() => {
		const element = scrollRef.current;
		if (!element || typeof ResizeObserver === "undefined") return;
		let timer: number | null = null;
		let measured = false;
		const observer = new ResizeObserver(([entry]) => {
			if (timer !== null) window.clearTimeout(timer);
			timer = window.setTimeout(() => {
				setWidth(Math.floor(entry.contentRect.width));
				setHeight(Math.floor(entry.contentRect.height));
			}, measured ? 120 : 0);
			measured = true;
		});
		observer.observe(element);
		return () => {
			if (timer !== null) window.clearTimeout(timer);
			observer.disconnect();
		};
	}, []);

	if (cover) {
		if (tooLarge !== null) return null;
		const page = loaded?.sizes[0];
		return (
			<div aria-label={path} className="flex min-h-0 min-w-0 flex-1 items-start justify-center overflow-hidden" ref={scrollRef} role="document">
				{loaded && page && width > 0 && height > 0 ? (
					<PdfPage pageNumber={1} pdf={loaded.pdf} pdfjs={loaded.pdfjs} root={scrollRef} scale={Math.max(width / page.width, height / page.height)} size={page} />
				) : null}
			</div>
		);
	}
	if (tooLarge !== null) return <PanelMessage>{t("files.explorer.tooLarge", { size: formatBytes(tooLarge) })}</PanelMessage>;
	return (
		<div aria-label={path} className="board-scrollbar min-h-0 min-w-0 flex-1 overflow-auto overscroll-contain bg-surface p-3" ref={scrollRef} role="document">
			{!loaded ? (
				<PanelMessage>{t("files.loading")}</PanelMessage>
			) : width > 0 ? (
				<div className="mx-auto flex w-max min-w-full flex-col items-center" data-selection-feedback="" style={{ gap: PAGE_GAP }}>
					{loaded.sizes.map((size, index) => (
						<PdfPage key={index} pageNumber={index + 1} pdf={loaded.pdf} pdfjs={loaded.pdfjs} root={scrollRef} scale={Math.min(width / size.width, MAX_SCALE) * zoom} size={size} />
					))}
				</div>
			) : null}
		</div>
	);
}

async function readPdf(src: string): Promise<Uint8Array> {
	const response = await fetch(src);
	if (!response.ok) throw new Error(`HTTP ${response.status}`);
	const declaredSize = Number(response.headers.get("content-length")) || 0;
	if (declaredSize > MAX_PDF_BYTES) {
		void response.body?.cancel();
		throw new PdfTooLargeError(declaredSize);
	}
	return new Uint8Array(await response.arrayBuffer());
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
	const [drawScale, setDrawScale] = useState(scale);
	useEffect(() => {
		if (scale === drawScale) return;
		const timer = window.setTimeout(() => setDrawScale(scale), drawnRef.current ? REDRAW_DELAY_MS : 0);
		return () => window.clearTimeout(timer);
	}, [drawScale, scale]);

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
			const viewport = page.getViewport({ scale: drawScale });
			const ratio = pdfOutputScale(viewport.width, viewport.height, window.devicePixelRatio || 1);
			const target = drawnRef.current ? document.createElement("canvas") : canvas;
			target.width = Math.floor(viewport.width * ratio);
			target.height = Math.floor(viewport.height * ratio);
			drawnRef.current = true;
			renderTask = page.render({ canvas: target, viewport, transform: ratio === 1 ? undefined : [ratio, 0, 0, ratio, 0, 0] });
			await renderTask.promise;
			if (cancelled) return;
			if (target !== canvas) {
				canvas.width = target.width;
				canvas.height = target.height;
				canvas.getContext("2d")?.drawImage(target, 0, 0);
			}
			text.replaceChildren();
			textLayer = new pdfjs.TextLayer({ container: text, textContentSource: page.streamTextContent(), viewport });
			await textLayer.render();
			const endOfContent = document.createElement("div");
			endOfContent.className = "endOfContent";
			text.append(endOfContent);
		})().catch(() => {
		});
		return () => {
			cancelled = true;
			renderTask?.cancel();
			textLayer?.cancel();
		};
	}, [drawScale, near, pageNumber, pdf, pdfjs]);

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
