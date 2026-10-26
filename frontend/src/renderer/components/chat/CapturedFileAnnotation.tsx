import { useId, useLayoutEffect, useRef, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import type { ChatFileReference } from "../../../shared/chat-file-reference";
import { fileReferencePreview } from "../../../shared/chat-file-reference";

export function CapturedFileAnnotation({ reference, children, onOpen, opening = false }: { reference: ChatFileReference; children?: ReactNode; onOpen?: () => void; opening?: boolean }) {
	const { t } = useTranslation();
	const [expanded, setExpanded] = useState(false);
	const [wrappedOverflow, setWrappedOverflow] = useState(false);
	const previewRef = useRef<HTMLPreElement>(null);
	const cardRef = useRef<HTMLDivElement>(null);
	const previewId = useId();
	const pointerStart = useRef<{ x: number; y: number } | null>(null);
	const dragged = useRef(false);
	const full = fileReferencePreview(reference);
	const shortened = [...full.split("\n").slice(0, 6).join("\n")].slice(0, 600).join("");
	const clipped = shortened !== full;
	useLayoutEffect(() => {
		if (expanded) return;
		const element = previewRef.current;
		if (!element) return;
		const measure = () => setWrappedOverflow(element.scrollHeight > element.clientHeight);
		measure();
		const observer = typeof ResizeObserver === "undefined" ? undefined : new ResizeObserver(measure);
		observer?.observe(element);
		return () => observer?.disconnect();
	}, [expanded, full]);
	const filename = reference.path.split("/").at(-1) ?? reference.path;
	const side = reference.endSide && reference.endSide !== reference.side ? `${reference.side} → ${reference.endSide}` : reference.side;
	const location = `L${reference.line}${reference.endLine === reference.line ? "" : `–${reference.endLine}`} · ${side}`;
	const header = <>
		<span className="min-w-0 flex-1 truncate font-semibold">{filename}</span>
		<span className="shrink-0 rounded bg-background/60 px-1.5 py-0.5 font-mono text-[10px]">{location}</span>
	</>;
	const headerClass = "mb-1 flex min-w-0 items-center gap-2 border-b border-border/60 pb-1.5";
	const open = () => {
		if (!onOpen || opening || dragged.current) return;
		const selection = window.getSelection();
		if (selection && !selection.isCollapsed && selection.rangeCount && cardRef.current && selection.getRangeAt(0).intersectsNode(cardRef.current)) return;
		onOpen();
	};
	return <div
		ref={cardRef}
		className={onOpen ? `cursor-pointer rounded bg-transparent text-left text-popover-foreground hover:text-foreground ${opening ? "opacity-50" : ""}` : undefined}
		onPointerDownCapture={event => {
			pointerStart.current = { x: event.clientX, y: event.clientY };
			dragged.current = false;
		}}
		onPointerMoveCapture={event => {
			const start = pointerStart.current;
			if (start && Math.hypot(event.clientX - start.x, event.clientY - start.y) > 4) dragged.current = true;
		}}
		onPointerUpCapture={() => { pointerStart.current = null; }}
		onClick={event => {
			if (event.target instanceof Element && event.target.closest("button, a, input, textarea, select")) return;
			open();
		}}
	>
		{onOpen ? <button type="button" className={`${headerClass} w-full text-left focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring`} title={reference.path} aria-label={`${t("chat.fileAnnotationOpenFile")}: ${reference.path} · ${location}`} disabled={opening} onClick={event => { if (event.detail === 0) dragged.current = false; open(); }}>{header}</button>
			: <div className={headerClass} title={reference.path}>{header}</div>}
		<pre ref={previewRef} id={previewId} className={`whitespace-pre-wrap break-words text-xs leading-5 select-text ${expanded ? "max-h-48 overflow-auto" : "line-clamp-6"}`}>{expanded ? full : shortened + (clipped ? "…" : "")}</pre>
		{clipped || wrappedOverflow ? <button type="button" className="mt-1 text-xs text-muted-foreground underline underline-offset-2" aria-expanded={expanded} aria-controls={previewId} onClick={() => setExpanded(value => !value)}>{t(expanded ? "chat.fileAnnotationShowLess" : "chat.fileAnnotationShowMore")}</button> : null}
		{reference.truncated ? <span className="block text-muted-foreground">{t("chat.capturedSelectionTruncated")}</span> : null}
		{children}
	</div>;
}
