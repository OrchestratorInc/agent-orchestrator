import { useId, useLayoutEffect, useRef, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import type { ChatFileReference } from "../../../shared/chat-file-reference";
import { fileReferencePreview } from "../../../shared/chat-file-reference";

export function CapturedFileAnnotation({ reference, children }: { reference: ChatFileReference; children?: ReactNode }) {
	const { t } = useTranslation();
	const [expanded, setExpanded] = useState(false);
	const [wrappedOverflow, setWrappedOverflow] = useState(false);
	const previewRef = useRef<HTMLPreElement>(null);
	const previewId = useId();
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
	return <>
		<div className="mb-1 flex min-w-0 items-center gap-2 border-b border-border/60 pb-1.5" title={reference.path}>
			<span className="min-w-0 flex-1 truncate font-semibold">{filename}</span>
			<span className="shrink-0 rounded bg-background/60 px-1.5 py-0.5 font-mono text-[10px]">L{reference.line}{reference.endLine === reference.line ? "" : `–${reference.endLine}`} · {side}</span>
		</div>
		<pre ref={previewRef} id={previewId} className={`whitespace-pre-wrap break-words text-xs leading-5 select-text ${expanded ? "max-h-48 overflow-auto" : "line-clamp-6"}`}>{expanded ? full : shortened + (clipped ? "…" : "")}</pre>
		{clipped || wrappedOverflow ? <button type="button" className="mt-1 text-xs text-muted-foreground underline underline-offset-2" aria-expanded={expanded} aria-controls={previewId} onClick={() => setExpanded(value => !value)}>{t(expanded ? "chat.fileAnnotationShowLess" : "chat.fileAnnotationShowMore")}</button> : null}
		{reference.truncated ? <span className="block text-muted-foreground">{t("chat.capturedSelectionTruncated")}</span> : null}
		{children}
	</>;
}
