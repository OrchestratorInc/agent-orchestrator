import { useLayoutEffect, useRef, useState, useSyncExternalStore, type RefObject } from "react";
import { useTranslation } from "react-i18next";
import { annotationDiagramBlock, annotationReferenceRange } from "../../lib/chat-annotation-navigation";
import {
	chatDraftAnnotations,
	getChatComposerMutation,
	readChatSessionDraft,
	subscribeChatDraftRuntime,
	type ChatDraftExcerptReference,
	type ChatDraftScope,
} from "../../lib/chat-drafts";
import { selectionViewport } from "../../lib/selection-toolbar";
import { Button } from "../ui/button";

const HIGHLIGHT = "chat-annotations";
const BADGE_SIZE = 24;

type HighlightApi = { Highlight?: new (...ranges: Range[]) => Set<Range>; CSS?: { highlights?: Map<string, Set<Range>> } };

export interface ChatAnnotationMarker {
	number: number;
	reference: ChatDraftExcerptReference;
}

interface Placement {
	id: string;
	number: number;
	left: number;
	top: number;
}

export function numberedReferences(references: readonly ChatDraftExcerptReference[]): ChatAnnotationMarker[] {
	return [...references]
		.sort((a, b) => (a.order ?? 0) - (b.order ?? 0))
		.map((reference, index) => ({ number: index + 1, reference }));
}

export function DraftAnnotationMarkers({ draftScope, ...props }: Omit<Parameters<typeof ChatAnnotationMarkers>[0], "markers"> & { draftScope: ChatDraftScope }) {
	const revision = useSyncExternalStore(
		(listener) => subscribeChatDraftRuntime(draftScope, listener),
		() => getChatComposerMutation(draftScope),
	);
	const [markers, setMarkers] = useState<ChatAnnotationMarker[]>([]);
	useLayoutEffect(() => {
		const next = chatDraftAnnotations(readChatSessionDraft(draftScope).composer).flatMap((annotation, index) =>
			annotation.kind === "chat" ? [{ number: index + 1, reference: annotation.reference }] : []);
		setMarkers((previous) => (JSON.stringify(previous) === JSON.stringify(next) ? previous : next));
	}, [draftScope, revision]);
	return <ChatAnnotationMarkers markers={markers} {...props} />;
}

export function ChatAnnotationMarkers({
	markers,
	conversationId,
	contentRef,
	viewportRef,
	activeId,
	onOpen,
}: {
	markers: readonly ChatAnnotationMarker[];
	conversationId: string;
	contentRef: RefObject<HTMLDivElement | null>;
	viewportRef: RefObject<HTMLDivElement | null>;
	activeId?: string;
	onOpen: (reference: ChatDraftExcerptReference, range: Range) => void;
}) {
	const { t } = useTranslation();
	const layer = useRef<HTMLDivElement>(null);
	const ranges = useRef(new Map<string, Range>());
	const [placed, setPlaced] = useState<Placement[]>([]);
	const own = markers.filter((marker) => marker.reference.conversationId === conversationId);
	const ownKey = JSON.stringify(own);
	useLayoutEffect(() => {
		const own = JSON.parse(ownKey) as ChatAnnotationMarker[];
		const content = contentRef.current;
		const viewport = viewportRef.current;
		const api = globalThis as unknown as HighlightApi;
		const registry = api.CSS?.highlights;
		const resolved = ranges.current;
		resolved.clear();
		if (!content || !viewport || !layer.current || own.length === 0) {
			setPlaced([]);
			return;
		}
		let frame: number | undefined;
		let painted: Range[] = [];
		const resolve = ({ reference }: ChatAnnotationMarker) => {
			const cached = resolved.get(reference.id);
			if (cached) return cached;
			const source = content.querySelector<HTMLElement>(`[data-chat-message-id="${CSS.escape(reference.messageId)}"]`);
			if (!source) return undefined;
			let range = annotationReferenceRange(source, reference);
			if (!range) {
				const block = annotationDiagramBlock(source, reference);
				if (block) {
					range = document.createRange();
					range.selectNodeContents(block);
				}
			}
			if (range) resolved.set(reference.id, range);
			return range;
		};
		const update = () => {
			frame = undefined;
			const surface = layer.current?.getBoundingClientRect();
			if (!surface) return;
			const bounds = selectionViewport(viewport);
			const next: Placement[] = [];
			for (const marker of own) {
				const range = resolve(marker);
				if (!range) continue;
				const lines = range.getClientRects();
				let line: DOMRect | undefined;
				for (let i = 0; i < lines.length && !line; i++) if (lines[i].width > 0 && lines[i].height > 0) line = lines[i];
				if (!line || line.top < bounds.top || line.top > bounds.bottom - 4) continue;
				const left = Math.min(line.right - 2, bounds.right - BADGE_SIZE) - surface.left;
				next.push({ id: marker.reference.id, number: marker.number, left, top: Math.max(bounds.top, line.top - BADGE_SIZE + 2) - surface.top });
			}
			const current = [...resolved.values()];
			if (registry && api.Highlight && (current.length !== painted.length || current.some((range, index) => range !== painted[index]))) {
				let highlight = registry.get(HIGHLIGHT);
				if (!highlight) registry.set(HIGHLIGHT, (highlight = new api.Highlight()));
				for (const range of painted) if (!current.includes(range)) highlight.delete(range);
				for (const range of current) highlight.add(range);
				painted = current;
			}
			setPlaced((previous) => (JSON.stringify(previous) === JSON.stringify(next) ? previous : next));
		};
		const schedule = () => { frame ??= window.requestAnimationFrame(update); };
		const onScroll = (event: Event) => {
			if (event.target instanceof Node && event.target.contains(viewport)) schedule();
		};
		const resize = new ResizeObserver(schedule);
		resize.observe(viewport);
		resize.observe(content);
		const mutation = new MutationObserver(() => {
			resolved.clear();
			schedule();
		});
		mutation.observe(content, { subtree: true, childList: true, characterData: true });
		document.addEventListener("scroll", onScroll, true);
		window.addEventListener("resize", schedule);
		update();
		return () => {
			if (frame !== undefined) window.cancelAnimationFrame(frame);
			resize.disconnect();
			mutation.disconnect();
			document.removeEventListener("scroll", onScroll, true);
			window.removeEventListener("resize", schedule);
			const highlight = registry?.get(HIGHLIGHT);
			for (const range of painted) highlight?.delete(range);
			if (highlight?.size === 0) registry?.delete(HIGHLIGHT);
		};
	}, [ownKey, contentRef, viewportRef]);
	if (own.length === 0) return null;
	return (
		<div ref={layer} className="pointer-events-none absolute inset-0 z-[1]" data-chat-annotation-markers="">
			{placed.map((place) => (
				<Button
					key={place.id}
					type="button"
					variant="primary"
					size="icon-xs"
					shape="pill"
					data-chat-annotation-marker=""
					aria-pressed={place.id === activeId}
					aria-label={t("chat.annotation.badge", { number: place.number })}
					className="pointer-events-auto absolute rounded-bl-none text-xs font-medium tabular-nums"
					style={{ left: place.left, top: place.top }}
					onMouseDown={(event) => event.preventDefault()}
					onClick={() => {
						const marker = own.find((item) => item.reference.id === place.id);
						const range = ranges.current.get(place.id);
						if (marker && range) onOpen(marker.reference, range);
					}}
				>
					{place.number}
				</Button>
			))}
		</div>
	);
}
