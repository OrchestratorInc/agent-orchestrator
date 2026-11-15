import { useEffect, useLayoutEffect, useRef, useState, type RefObject } from "react";
import { createPortal } from "react-dom";
import { FileAnnotationComposer, fileAnnotationKey, type FileAnnotationModel } from "./WorkspaceDiffView";

const HIGHLIGHT_NAME = "artifact-selection";
const GAP = 4;

type HighlightRegistry = { delete: (name: string) => void; set: (name: string, highlight: unknown) => void };

function highlightRegistry(): { Highlight?: new (...ranges: Range[]) => unknown; registry?: HighlightRegistry } {
	const api = globalThis as unknown as { Highlight?: new (...ranges: Range[]) => unknown; CSS?: { highlights?: HighlightRegistry } };
	return { Highlight: api.Highlight, registry: api.CSS?.highlights };
}

function renderedSelection(container: HTMLElement): { quote: string; range: Range } | null {
	const selection = window.getSelection();
	if (!selection || selection.isCollapsed || selection.rangeCount === 0) return null;
	const range = selection.getRangeAt(0);
	const node = range.commonAncestorContainer;
	const element = node instanceof Element ? node : node.parentElement;
	const body = element?.closest(".markdown-body, [data-selection-feedback]");
	if (!body || !container.contains(body)) return null;
	const quote = selection.toString().trim();
	return quote ? { quote, range: range.cloneRange() } : null;
}

export function ArtifactSelectionAsk({
	annotation,
	containerRef,
	path,
}: {
	annotation: FileAnnotationModel;
	containerRef: RefObject<HTMLElement | null>;
	path: string;
}) {
	const [ranges] = useState(() => new Map<string, Range>());
	const popoverRef = useRef<HTMLDivElement>(null);
	const annotationRef = useRef(annotation);
	annotationRef.current = annotation;
	const target = annotation.targets.findLast((open) => open.quote != null && open.path === path);
	const range = target ? ranges.get(fileAnnotationKey(target)) : undefined;
	const targetRef = useRef(target);
	targetRef.current = target;

	useEffect(() => {
		const container = containerRef.current;
		if (!container) return;
		let timer: number | null = null;
		const capture = (event: Event) => {
			if (popoverRef.current?.contains(event.target as Node)) return;
			if (timer !== null) window.clearTimeout(timer);
			timer = window.setTimeout(() => {
				timer = null;
				const next = renderedSelection(container);
				if (!next) return;
				const nextTarget = { path, side: "file" as const, surface: "focused" as const, quote: next.quote };
				const key = fileAnnotationKey(nextTarget);
				ranges.set(key, next.range);
				if (!annotationRef.current.targets.some((open) => fileAnnotationKey(open) === key)) annotationRef.current.begin(nextTarget);
			}, 0);
		};
		const onPointerDown = (event: PointerEvent) => {
			const open = targetRef.current;
			if (!open || popoverRef.current?.contains(event.target as Node)) return;
			if (!annotationRef.current.draftFor(open).trim() && annotationRef.current.statusFor(open) === "idle") annotationRef.current.cancel(open);
		};
		document.addEventListener("pointerup", capture);
		container.addEventListener("keyup", capture);
		document.addEventListener("pointerdown", onPointerDown, true);
		return () => {
			if (timer !== null) window.clearTimeout(timer);
			document.removeEventListener("pointerup", capture);
			container.removeEventListener("keyup", capture);
			document.removeEventListener("pointerdown", onPointerDown, true);
		};
	}, [containerRef, path, ranges]);

	useEffect(() => {
		if (!range) return;
		const { Highlight, registry } = highlightRegistry();
		if (!Highlight || !registry) return;
		registry.set(HIGHLIGHT_NAME, new Highlight(range));
		return () => registry.delete(HIGHLIGHT_NAME);
	}, [range]);

	useLayoutEffect(() => {
		const popover = popoverRef.current;
		if (!popover || !range) return;
		const place = () => {
			const rect = range.getBoundingClientRect();
			const width = popover.offsetWidth;
			const height = popover.offsetHeight;
			const below = rect.bottom + GAP;
			popover.style.top = `${below + height <= window.innerHeight - GAP ? below : Math.max(GAP, rect.top - GAP - height)}px`;
			popover.style.left = `${Math.min(Math.max(GAP, rect.left), window.innerWidth - width - GAP)}px`;
		};
		place();
		const observer = typeof ResizeObserver === "undefined" ? null : new ResizeObserver(place);
		observer?.observe(popover);
		document.addEventListener("scroll", place, true);
		window.addEventListener("resize", place);
		return () => {
			observer?.disconnect();
			document.removeEventListener("scroll", place, true);
			window.removeEventListener("resize", place);
		};
	}, [range]);

	if (!target || !range) return null;
	return createPortal(
		<div
			className="z-overlay w-96 max-w-[calc(100vw-1rem)] [&_form]:shadow-lg"
			ref={popoverRef}
			style={{ left: 0, position: "fixed", top: 0, zIndex: "calc(var(--z-overlay) + 1)" }}
		>
			<FileAnnotationComposer annotation={annotation} key={fileAnnotationKey(target)} target={target} />
		</div>,
		document.body,
	);
}
