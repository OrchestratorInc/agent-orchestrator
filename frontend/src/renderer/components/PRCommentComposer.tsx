import { Loader2, Paperclip } from "lucide-react";
import { useEffect, useRef, useState, type ClipboardEvent, type DragEvent, type KeyboardEvent, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { markdownCodeRanges } from "../lib/markdown-code";
import { cn } from "../lib/utils";
import { Button } from "./ui/button";

/** Largest file the daemon takes for a comment attachment. */
export const PR_ATTACHMENT_MAX_BYTES = 10 * 1024 * 1024;
/** Largest a comment's files can be together. */
export const PR_ATTACHMENTS_TOTAL_MAX_BYTES = 25 * 1024 * 1024;
/** What the file picker offers; the daemon checks the bytes again. */
const PR_ATTACHMENT_ACCEPT = "image/png,image/jpeg,image/gif,image/webp,video/mp4,video/quicktime,video/webm,application/pdf,text/plain,application/json,text/csv,application/zip,.log,.txt,.csv,.json,.zip,.mov";

type Edit = { text: string; selectionStart: number; selectionEnd: number };

/** Wraps the selection (or a placeholder) in a marker, like **bold**. */
export function wrapSelection(value: string, start: number, end: number, marker: string, placeholder: string): Edit {
	const selected = value.slice(start, end) || placeholder;
	const text = value.slice(0, start) + marker + selected + marker + value.slice(end);
	return { text, selectionStart: start + marker.length, selectionEnd: start + marker.length + selected.length };
}

/** A link around the selection, with the URL selected to type over. */
export function insertLink(value: string, start: number, end: number, placeholder: string): Edit {
	const label = value.slice(start, end) || placeholder;
	const url = "url";
	const text = `${value.slice(0, start)}[${label}](${url})${value.slice(end)}`;
	const urlStart = start + label.length + 3;
	return { text, selectionStart: urlStart, selectionEnd: urlStart + url.length };
}

export function readAsBase64(file: File): Promise<string> {
	return new Promise((resolve, reject) => {
		const reader = new FileReader();
		reader.onload = () => resolve(String(reader.result).replace(/^data:[^,]*,/, ""));
		reader.onerror = () => reject(reader.error ?? new Error("read failed"));
		reader.readAsDataURL(file);
	});
}

/** A comment body's link to one of its files: "](attachment:0)". */
const ATTACHMENT_LINK = /\]\(attachment:(\d+)\)/g;
/** Most files one comment takes, as the daemon counts them. */
export const PR_ATTACHMENTS_MAX_FILES = 10;

/** The text's attachment links outside code (an example there is not a file), as the daemon reads them. */
function attachmentLinks(text: string): { start: number; end: number; index: number }[] {
	const code = markdownCodeRanges(text);
	return [...text.matchAll(ATTACHMENT_LINK)]
		.map((match) => ({ start: match.index, end: match.index + match[0].length, index: Number(match[1]) }))
		.filter((link) => !code.some(([start, end]) => link.start >= start && link.end <= end));
}

/**
 * The files a comment links, in the order the text first links them, with the
 * text renumbered to match. A file whose link was deleted is left out, so it
 * never leaves this machine.
 */
export function linkedAttachments(text: string, files: ReadonlyMap<number, File>): { body: string; files: File[] } {
	const links = attachmentLinks(text);
	const order: number[] = [];
	for (const { index } of links) {
		if (files.has(index) && !order.includes(index)) order.push(index);
	}
	let body = "";
	let last = 0;
	for (const link of links) {
		const position = order.indexOf(link.index);
		body += text.slice(last, link.start) + (position < 0 ? text.slice(link.start, link.end) : `](attachment:${position})`);
		last = link.end;
	}
	body += text.slice(last);
	return { body, files: order.map((index) => files.get(index)!) };
}

/** A file's link label: its name, without what would end the markdown link early. */
const linkLabel = (name: string) => name.replace(/[[\]()]/g, "_");

/**
 * The box a PR comment or reply is written in: text that grows as it is
 * written, and attachments (picked, pasted or dropped). A file stays on this
 * machine, linked from the text as "![name](attachment:0)", until the comment
 * posts: it goes with the comment then, so a cancelled draft uploads nothing.
 * ⌘B, ⌘I, ⌘K and ⌘E format the selection; ⌘↵ posts and Escape cancels.
 */
export function PRCommentComposer({ attachments, autoFocus, avatar, busy, error, initial = "", onCancel, onEdit, onSubmit, placeholder, roomy, submitLabel }: {
	/**
	 * Files can be attached. Without it there is no Attach; with blockedReason,
	 * Attach is off and that says why (no write access to the repository).
	 */
	attachments?: { blockedReason?: string };
	/** Takes focus as it opens, with the caret after any text it starts with. */
	autoFocus?: boolean;
	/** Who is writing, beside the text. */
	avatar?: ReactNode;
	busy: boolean;
	error?: string | null;
	initial?: string;
	onCancel?: () => void;
	/** The draft changed (typed, formatted or given a file), so an error about the last post no longer applies. */
	onEdit?: () => void;
	/** Posts the text with the files it links, in the order it links them. */
	onSubmit: (body: string, files: File[]) => Promise<unknown>;
	placeholder: string;
	/** Reading size, for the PR's tab; the sidebar stays compact. */
	roomy?: boolean;
	submitLabel: string;
}) {
	const { t } = useTranslation();
	const [value, setValue] = useState(initial);
	// The files the text links, by the number in their link.
	const [files, setFiles] = useState<ReadonlyMap<number, File>>(() => new Map());
	const nextFile = useRef(0);
	const [uploadError, setUploadError] = useState<string | null>(null);
	const [dragging, setDragging] = useState(false);
	const textareaRef = useRef<HTMLTextAreaElement>(null);
	const fileRef = useRef<HTMLInputElement>(null);
	// Opened to write, with the caret after any text it starts with (a quote and @mention).
	useEffect(() => {
		if (!autoFocus) return;
		const element = textareaRef.current;
		if (!element) return;
		element.focus();
		element.setSelectionRange(element.value.length, element.value.length);
	}, [autoFocus]);

	const apply = (edit: (value: string, start: number, end: number) => Edit) => {
		const element = textareaRef.current;
		if (!element) return;
		const next = edit(element.value, element.selectionStart, element.selectionEnd);
		setValue(next.text);
		onEdit?.();
		requestAnimationFrame(() => {
			element.focus();
			element.setSelectionRange(next.selectionStart, next.selectionEnd);
		});
	};
	const bold = () => apply((v, s, e) => wrapSelection(v, s, e, "**", t("pr.comments.format.boldPlaceholder")));
	const italic = () => apply((v, s, e) => wrapSelection(v, s, e, "_", t("pr.comments.format.italicPlaceholder")));
	const code = () => apply((v, s, e) => (v.slice(s, e).includes("\n") ? wrapSelection(v, s, e, "\n```\n", "") : wrapSelection(v, s, e, "`", t("pr.comments.format.codePlaceholder"))));
	const link = () => apply((v, s, e) => insertLink(v, s, e, t("pr.comments.format.linkPlaceholder")));

	// Set before the first await, so a second click or ⌘↵ can't post twice
	// while the parent is still asking or posting.
	const submittingRef = useRef(false);
	const [submitting, setSubmitting] = useState(false);
	const posting = busy || submitting;
	const attach = (picked: File[]) => {
		// While a comment posts, its text is already sent: a file added now would be lost.
		if (!attachments || picked.length === 0 || posting) return;
		if (attachments.blockedReason) {
			setUploadError(attachments.blockedReason);
			return;
		}
		setUploadError(null);
		// Only files the text still links count: a deleted link frees its room.
		const linked = linkedAttachments(value, files).files;
		let total = linked.reduce((sum, file) => sum + file.size, 0);
		let count = linked.length;
		const accepted: File[] = [];
		for (const file of picked) {
			if (count >= PR_ATTACHMENTS_MAX_FILES) {
				setUploadError(t("pr.comments.attach.tooMany", { count: PR_ATTACHMENTS_MAX_FILES }));
				break;
			}
			if (file.size > PR_ATTACHMENT_MAX_BYTES) {
				setUploadError(t("pr.comments.attach.tooLarge", { name: file.name }));
				continue;
			}
			if (total + file.size > PR_ATTACHMENTS_TOTAL_MAX_BYTES) {
				setUploadError(t("pr.comments.attach.tooLargeTotal"));
				continue;
			}
			total += file.size;
			count += 1;
			accepted.push(file);
		}
		if (accepted.length === 0) return;
		const numbered = accepted.map((file) => [nextFile.current++, file] as const);
		setFiles((current) => new Map([...current, ...numbered]));
		const links = numbered.map(([index, file]) => `${file.type.startsWith("image/") ? "!" : ""}[${linkLabel(file.name)}](attachment:${index})`);
		const caret = textareaRef.current?.selectionEnd;
		onEdit?.();
		setValue((current) => {
			const at = caret === undefined ? current.length : Math.min(caret, current.length);
			const before = current.slice(0, at);
			const spacer = before && !before.endsWith("\n") ? "\n" : "";
			return `${before}${spacer}${links.join("\n")}\n${current.slice(at)}`;
		});
	};

	const submit = async () => {
		if (!value.trim() || posting || submittingRef.current) return;
		submittingRef.current = true;
		setSubmitting(true);
		const linked = linkedAttachments(value.trim(), files);
		try {
			await onSubmit(linked.body, linked.files);
			setValue("");
			setFiles(new Map());
			nextFile.current = 0;
		} catch {
			// The mutation shows the error; the draft stays so nothing typed is lost.
		} finally {
			submittingRef.current = false;
			setSubmitting(false);
		}
	};
	const onKeyDown = (event: KeyboardEvent<HTMLTextAreaElement>) => {
		const mod = event.metaKey || event.ctrlKey;
		if (mod && event.key === "Enter") {
			event.preventDefault();
			void submit();
		} else if (mod && !event.shiftKey && (event.key === "b" || event.key === "i" || event.key === "k" || event.key === "e")) {
			event.preventDefault();
			({ b: bold, i: italic, k: link, e: code })[event.key]();
		} else if (event.key === "Escape" && onCancel && !posting) {
			event.preventDefault();
			event.stopPropagation();
			onCancel();
		}
	};
	const onPaste = (event: ClipboardEvent<HTMLTextAreaElement>) => {
		const pasted = Array.from(event.clipboardData.files);
		if (!attachments || pasted.length === 0) return;
		event.preventDefault();
		attach(pasted);
	};
	const onDrop = (event: DragEvent<HTMLDivElement>) => {
		setDragging(false);
		const dropped = Array.from(event.dataTransfer.files);
		if (!attachments || dropped.length === 0 || posting) return;
		event.preventDefault();
		attach(dropped);
	};

	const fileInput = attachments ? (
		<input
			accept={PR_ATTACHMENT_ACCEPT}
			aria-hidden="true"
			className="hidden"
			data-testid="pr-comment-file"
			multiple
			onChange={(event) => { const picked = Array.from(event.target.files ?? []); event.target.value = ""; attach(picked); }}
			ref={fileRef}
			tabIndex={-1}
			type="file"
		/>
	) : null;
	const clear = () => { setValue(""); setFiles(new Map()); nextFile.current = 0; setUploadError(null); onCancel?.(); textareaRef.current?.blur(); };
	return (
		<div
			className={cn("flex min-w-0 flex-col gap-1.5", roomy ? "text-sm" : "text-xs")}
			data-testid="pr-comment-composer"
			onDragLeave={() => setDragging(false)}
			onDragOver={(event) => { if (attachments && !attachments.blockedReason && event.dataTransfer.types.includes("Files")) { event.preventDefault(); setDragging(true); } }}
			onDrop={onDrop}
		>
			<div
				className={cn(
					"flex min-w-0 items-start gap-2.5 rounded-xl border border-border bg-background transition-[border-color,box-shadow]",
					roomy ? "px-4 py-3" : "px-3 py-2.5",
					"focus-within:border-ring focus-within:ring-3 focus-within:ring-ring/25",
					dragging && "border-ring ring-3 ring-ring/25",
				)}
			>
				{avatar}
				<div className="flex min-w-0 flex-1 flex-col gap-2">
					<textarea
						aria-label={placeholder}
						// Grows with what is written, up to a cap, then scrolls.
						className="max-h-64 min-h-6 min-w-0 resize-none border-0 bg-transparent p-0 leading-6 text-foreground [field-sizing:content] placeholder:text-muted-foreground focus-visible:outline-none"
						disabled={posting}
						onChange={(event) => { setValue(event.target.value); onEdit?.(); }}
						onKeyDown={onKeyDown}
						onPaste={onPaste}
						placeholder={placeholder}
						ref={textareaRef}
						value={value}
					/>
					<div className="flex min-w-0 items-center gap-1.5 text-xs">
						<Button className="h-7 gap-1 rounded-full px-3 text-xs" disabled={posting || !value.trim()} onClick={() => void submit()} size="sm" type="button">
							{posting ? <Loader2 aria-hidden="true" className="size-icon-sm animate-spin" /> : null}
							{submitLabel}
						</Button>
						{attachments ? (
							<button className="inline-flex h-7 shrink-0 items-center gap-1 rounded-full px-2 font-medium text-muted-foreground transition-colors hover:bg-interactive-hover hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60 disabled:pointer-events-none disabled:opacity-50" aria-disabled={attachments.blockedReason ? true : undefined} disabled={posting} onClick={() => (attachments.blockedReason ? setUploadError(attachments.blockedReason) : fileRef.current?.click())} onMouseDown={(event) => event.preventDefault()} title={attachments.blockedReason ?? t("pr.comments.simpleHint")} type="button">
								<Paperclip aria-hidden="true" className="size-icon-xs" />
								{t("pr.comments.attach.short")}
							</button>
						) : null}
						{onCancel || value ? <Button className="h-7 rounded-full px-2.5 text-xs" disabled={posting} onClick={clear} onMouseDown={(event) => event.preventDefault()} size="sm" type="button" variant="ghost">{t("pr.comments.cancel")}</Button> : null}
					</div>
				</div>
			</div>
			{fileInput}
			{uploadError ? <p className="m-0 text-xs text-error" role="status">{uploadError}</p> : null}
			{error ? <p className="m-0 text-xs text-error" role="status">{error}</p> : null}
		</div>
	);
}
