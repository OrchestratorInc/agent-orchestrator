import { Bold, Code, Italic, Link, List, ListOrdered, Loader2, Paperclip, TextQuote } from "lucide-react";
import { useRef, useState, type ClipboardEvent, type DragEvent, type KeyboardEvent, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { cn } from "../lib/utils";
import { PRMarkdown } from "./PRMarkdown";
import { Button } from "./ui/button";
import { Tooltip, TooltipContent, TooltipTrigger } from "./ui/tooltip";

/** Largest file the daemon takes for a comment attachment. */
export const PR_ATTACHMENT_MAX_BYTES = 10 * 1024 * 1024;
/** What the file picker offers; the daemon checks the bytes again. */
export const PR_ATTACHMENT_ACCEPT = "image/png,image/jpeg,image/gif,image/webp,video/mp4,video/quicktime,video/webm,application/pdf,text/plain,application/json,text/csv,application/zip,.log,.txt,.csv,.json,.zip,.mov";

type Edit = { text: string; selectionStart: number; selectionEnd: number };

/** Wraps the selection (or a placeholder) in a marker, like **bold**. */
export function wrapSelection(value: string, start: number, end: number, marker: string, placeholder: string): Edit {
	const selected = value.slice(start, end) || placeholder;
	const text = value.slice(0, start) + marker + selected + marker + value.slice(end);
	return { text, selectionStart: start + marker.length, selectionEnd: start + marker.length + selected.length };
}

/** Starts each selected line with a prefix, like "> " or "- "; a numbered list counts up. */
export function prefixLines(value: string, start: number, end: number, prefix: (index: number) => string): Edit {
	const lineStart = value.lastIndexOf("\n", start - 1) + 1;
	const lineEnd = value.indexOf("\n", end) === -1 ? value.length : value.indexOf("\n", end);
	const lines = value.slice(lineStart, lineEnd).split("\n").map((line, index) => prefix(index) + line);
	const block = lines.join("\n");
	return { text: value.slice(0, lineStart) + block + value.slice(lineEnd), selectionStart: lineStart, selectionEnd: lineStart + block.length };
}

/** A link around the selection, with the URL selected to type over. */
export function insertLink(value: string, start: number, end: number, placeholder: string): Edit {
	const label = value.slice(start, end) || placeholder;
	const url = "url";
	const text = `${value.slice(0, start)}[${label}](${url})${value.slice(end)}`;
	const urlStart = start + label.length + 3;
	return { text, selectionStart: urlStart, selectionEnd: urlStart + url.length };
}

function readAsBase64(file: File): Promise<string> {
	return new Promise((resolve, reject) => {
		const reader = new FileReader();
		reader.onload = () => resolve(String(reader.result).replace(/^data:[^,]*,/, ""));
		reader.onerror = () => reject(reader.error ?? new Error("read failed"));
		reader.readAsDataURL(file);
	});
}

export type PRAttachmentUpload = (file: { name: string; contentType: string; data: string }) => Promise<string>;

function ToolButton({ children, label, onClick, disabled }: { children: ReactNode; label: string; onClick: () => void; disabled?: boolean }) {
	return (
		<Tooltip>
			<TooltipTrigger asChild>
				<button
					aria-label={label}
					className="inline-flex size-6 shrink-0 items-center justify-center rounded-sm text-muted-foreground transition-colors hover:bg-interactive-hover hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60 disabled:pointer-events-none disabled:opacity-50"
					disabled={disabled}
					onClick={onClick}
					// Keeps the textarea's selection for the edit.
					onMouseDown={(event) => event.preventDefault()}
					type="button"
				>
					{children}
				</button>
			</TooltipTrigger>
			<TooltipContent side="top">{label}</TooltipContent>
		</Tooltip>
	);
}

/**
 * The box a PR comment or reply is written in, with what a comment on GitHub
 * needs and no more: bold, italic, code, a link, a quote, lists, attachments
 * (picked, pasted or dropped; uploaded to the PR's repository while a
 * placeholder holds their place), and a preview before posting.
 */
export function PRCommentComposer({ autoFocus, busy, compact, error, initial = "", onCancel, onSubmit, placeholder, submitLabel, upload }: {
	autoFocus?: boolean;
	busy: boolean;
	/** One line until focused or typed in. */
	compact?: boolean;
	error?: string | null;
	initial?: string;
	onCancel?: () => void;
	onSubmit: (body: string) => Promise<unknown>;
	placeholder: string;
	submitLabel: string;
	/** Uploads a file and returns its markdown; without it there is no Attach. */
	upload?: PRAttachmentUpload;
}) {
	const { t } = useTranslation();
	const [value, setValue] = useState(initial);
	const [focused, setFocused] = useState(false);
	const [previewing, setPreviewing] = useState(false);
	const [uploading, setUploading] = useState(0);
	const [uploadError, setUploadError] = useState<string | null>(null);
	const [dragging, setDragging] = useState(false);
	const textareaRef = useRef<HTMLTextAreaElement>(null);
	const fileRef = useRef<HTMLInputElement>(null);
	// The latest text, for uploads that finish after more typing.
	const valueRef = useRef(value);
	valueRef.current = value;
	const roomy = !compact || focused || value.length > 0 || previewing || uploading > 0;

	const apply = (edit: (value: string, start: number, end: number) => Edit) => {
		const element = textareaRef.current;
		if (!element) return;
		const next = edit(element.value, element.selectionStart, element.selectionEnd);
		setValue(next.text);
		setPreviewing(false);
		requestAnimationFrame(() => {
			element.focus();
			element.setSelectionRange(next.selectionStart, next.selectionEnd);
		});
	};
	const bold = () => apply((v, s, e) => wrapSelection(v, s, e, "**", t("pr.comments.format.boldPlaceholder")));
	const italic = () => apply((v, s, e) => wrapSelection(v, s, e, "_", t("pr.comments.format.italicPlaceholder")));
	const code = () => apply((v, s, e) => (v.slice(s, e).includes("\n") ? wrapSelection(v, s, e, "\n```\n", "") : wrapSelection(v, s, e, "`", t("pr.comments.format.codePlaceholder"))));
	const link = () => apply((v, s, e) => insertLink(v, s, e, t("pr.comments.format.linkPlaceholder")));
	const quote = () => apply((v, s, e) => prefixLines(v, s, e, () => "> "));
	const bullets = () => apply((v, s, e) => prefixLines(v, s, e, () => "- "));
	const numbers = () => apply((v, s, e) => prefixLines(v, s, e, (index) => `${index + 1}. `));

	const attach = async (files: File[]) => {
		if (!upload || files.length === 0) return;
		setUploadError(null);
		setPreviewing(false);
		for (const file of files) {
			if (file.size > PR_ATTACHMENT_MAX_BYTES) {
				setUploadError(t("pr.comments.attach.tooLarge", { name: file.name }));
				continue;
			}
			const image = file.type.startsWith("image/");
			const holder = `${image ? "!" : ""}[${t("pr.comments.attach.uploading", { name: file.name })}]()`;
			const element = textareaRef.current;
			const at = element ? element.selectionEnd : valueRef.current.length;
			const current = valueRef.current;
			const before = current.slice(0, at);
			const spacer = before && !before.endsWith("\n") ? "\n" : "";
			setValue(`${before}${spacer}${holder}\n${current.slice(at)}`);
			setUploading((count) => count + 1);
			try {
				const markdown = await upload({ name: file.name, contentType: file.type || "application/octet-stream", data: await readAsBase64(file) });
				setValue((text) => text.replace(holder, markdown));
			} catch (failure) {
				setValue((text) => text.replace(`${holder}\n`, "").replace(holder, ""));
				setUploadError(failure instanceof Error ? failure.message : t("pr.comments.attach.failed", { name: file.name }));
			} finally {
				setUploading((count) => count - 1);
			}
		}
	};

	const submit = async () => {
		if (!value.trim() || busy || uploading > 0) return;
		try {
			await onSubmit(value.trim());
			setValue("");
			setPreviewing(false);
		} catch {
			// The mutation shows the error; the draft stays so nothing typed is lost.
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
		} else if (event.key === "Escape" && onCancel) {
			event.preventDefault();
			event.stopPropagation();
			onCancel();
		}
	};
	const onPaste = (event: ClipboardEvent<HTMLTextAreaElement>) => {
		const files = Array.from(event.clipboardData.files);
		if (!upload || files.length === 0) return;
		event.preventDefault();
		void attach(files);
	};
	const onDrop = (event: DragEvent<HTMLDivElement>) => {
		setDragging(false);
		const files = Array.from(event.dataTransfer.files);
		if (!upload || files.length === 0) return;
		event.preventDefault();
		void attach(files);
	};

	return (
		<div
			className={cn("flex min-w-0 flex-col gap-1.5 text-xs", dragging && "rounded-md ring-2 ring-ring/50")}
			data-testid="pr-comment-composer"
			onDragLeave={() => setDragging(false)}
			onDragOver={(event) => { if (upload && event.dataTransfer.types.includes("Files")) { event.preventDefault(); setDragging(true); } }}
			onDrop={onDrop}
		>
			<div className={cn("flex min-w-0 flex-col rounded-md border border-border bg-background", "focus-within:border-ring focus-within:ring-2 focus-within:ring-ring/40")}>
				{roomy ? (
					<div className="flex min-w-0 items-center gap-0.5 border-b border-border/60 px-1 py-0.5" role="toolbar" aria-label={t("pr.comments.format.toolbar")}>
						<ToolButton disabled={previewing} label={t("pr.comments.format.bold")} onClick={bold}><Bold aria-hidden="true" className="size-icon-xs" /></ToolButton>
						<ToolButton disabled={previewing} label={t("pr.comments.format.italic")} onClick={italic}><Italic aria-hidden="true" className="size-icon-xs" /></ToolButton>
						<ToolButton disabled={previewing} label={t("pr.comments.format.code")} onClick={code}><Code aria-hidden="true" className="size-icon-xs" /></ToolButton>
						<ToolButton disabled={previewing} label={t("pr.comments.format.link")} onClick={link}><Link aria-hidden="true" className="size-icon-xs" /></ToolButton>
						<ToolButton disabled={previewing} label={t("pr.comments.format.quote")} onClick={quote}><TextQuote aria-hidden="true" className="size-icon-xs" /></ToolButton>
						<ToolButton disabled={previewing} label={t("pr.comments.format.bullets")} onClick={bullets}><List aria-hidden="true" className="size-icon-xs" /></ToolButton>
						<ToolButton disabled={previewing} label={t("pr.comments.format.numbers")} onClick={numbers}><ListOrdered aria-hidden="true" className="size-icon-xs" /></ToolButton>
						{upload ? (
							<ToolButton disabled={previewing} label={t("pr.comments.attach.label")} onClick={() => fileRef.current?.click()}>
								{uploading > 0 ? <Loader2 aria-hidden="true" className="size-icon-xs animate-spin" /> : <Paperclip aria-hidden="true" className="size-icon-xs" />}
							</ToolButton>
						) : null}
						<span className="ml-auto inline-flex items-center gap-0.5 text-micro">
							<button aria-pressed={!previewing} className={cn("rounded-sm px-1.5 py-0.5 font-medium transition-colors", previewing ? "text-passive hover:text-foreground" : "text-foreground")} onClick={() => setPreviewing(false)} type="button">{t("pr.comments.write")}</button>
							<button aria-pressed={previewing} className={cn("rounded-sm px-1.5 py-0.5 font-medium transition-colors", previewing ? "text-foreground" : "text-passive hover:text-foreground")} onClick={() => setPreviewing(true)} type="button">{t("pr.comments.preview")}</button>
						</span>
					</div>
				) : null}
				{previewing ? (
					<div className="min-h-16 px-2 py-1.5" data-testid="pr-comment-preview">
						{value.trim() ? <PRMarkdown body={value} /> : <p className="m-0 text-passive">{t("pr.comments.nothingToPreview")}</p>}
					</div>
				) : (
					<textarea
						aria-label={placeholder}
						autoFocus={autoFocus}
						className={cn(
							"w-full border-0 bg-transparent px-2 py-1.5 text-xs leading-relaxed text-foreground placeholder:text-passive focus-visible:outline-none",
							roomy ? "min-h-16 resize-y" : "h-8 resize-none",
						)}
						disabled={busy}
						onBlur={() => setFocused(false)}
						onChange={(event) => setValue(event.target.value)}
						onFocus={() => setFocused(true)}
						onKeyDown={onKeyDown}
						onPaste={onPaste}
						placeholder={placeholder}
						ref={textareaRef}
						value={value}
					/>
				)}
			</div>
			{upload ? (
				<input
					accept={PR_ATTACHMENT_ACCEPT}
					aria-hidden="true"
					className="hidden"
					data-testid="pr-comment-file"
					multiple
					onChange={(event) => { const files = Array.from(event.target.files ?? []); event.target.value = ""; void attach(files); }}
					ref={fileRef}
					tabIndex={-1}
					type="file"
				/>
			) : null}
			{uploadError ? <p className="m-0 text-xs text-error" role="status">{uploadError}</p> : null}
			{error ? <p className="m-0 text-xs text-error" role="status">{error}</p> : null}
			{roomy ? (
				<div className="flex items-center justify-end gap-1.5">
					<span className="mr-auto min-w-0 truncate text-micro text-passive">{upload ? t("pr.comments.markdownAttachHint") : t("pr.comments.markdownHint")}</span>
					{onCancel ? <Button className="px-2 text-xs" onClick={onCancel} size="sm" type="button" variant="ghost">{t("pr.comments.cancel")}</Button> : null}
					<Button className="gap-1 px-2 text-xs" disabled={busy || uploading > 0 || !value.trim()} onClick={() => void submit()} size="sm" type="button" variant="secondary">
						{busy ? <Loader2 aria-hidden="true" className="size-icon-sm animate-spin" /> : null}
						{submitLabel}
					</Button>
				</div>
			) : null}
		</div>
	);
}
