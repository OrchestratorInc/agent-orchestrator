import { Loader2, Paperclip } from "lucide-react";
import { useEffect, useRef, useState, type ClipboardEvent, type DragEvent, type KeyboardEvent, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { cn } from "../lib/utils";
import { Button } from "./ui/button";

/** Largest file the daemon takes for a comment attachment. */
export const PR_ATTACHMENT_MAX_BYTES = 10 * 1024 * 1024;
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

function readAsBase64(file: File): Promise<string> {
	return new Promise((resolve, reject) => {
		const reader = new FileReader();
		reader.onload = () => resolve(String(reader.result).replace(/^data:[^,]*,/, ""));
		reader.onerror = () => reject(reader.error ?? new Error("read failed"));
		reader.readAsDataURL(file);
	});
}

export type PRAttachmentUpload = (file: { name: string; contentType: string; data: string }) => Promise<string>;

/**
 * The box a PR comment or reply is written in: text that grows as it is
 * written, and attachments (picked, pasted or dropped; uploaded to the PR's
 * repository while a placeholder holds their place). ⌘B, ⌘I, ⌘K and ⌘E
 * format the selection; ⌘↵ posts and Escape cancels.
 */
export function PRCommentComposer({ autoFocus, avatar, busy, error, initial = "", onCancel, onSubmit, placeholder, roomy, submitLabel, upload }: {
	/** Takes focus as it opens, with the caret after any text it starts with. */
	autoFocus?: boolean;
	/** Who is writing, beside the text. */
	avatar?: ReactNode;
	busy: boolean;
	error?: string | null;
	initial?: string;
	onCancel?: () => void;
	onSubmit: (body: string) => Promise<unknown>;
	placeholder: string;
	/** Reading size, for the PR's tab; the sidebar stays compact. */
	roomy?: boolean;
	submitLabel: string;
	/** Uploads a file and returns its markdown; without it there is no Attach. */
	upload?: PRAttachmentUpload;
}) {
	const { t } = useTranslation();
	const [value, setValue] = useState(initial);
	const [uploading, setUploading] = useState(0);
	const [uploadError, setUploadError] = useState<string | null>(null);
	const [dragging, setDragging] = useState(false);
	const textareaRef = useRef<HTMLTextAreaElement>(null);
	const fileRef = useRef<HTMLInputElement>(null);
	// The latest text, for uploads that finish after more typing.
	const valueRef = useRef(value);
	valueRef.current = value;
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
		requestAnimationFrame(() => {
			element.focus();
			element.setSelectionRange(next.selectionStart, next.selectionEnd);
		});
	};
	const bold = () => apply((v, s, e) => wrapSelection(v, s, e, "**", t("pr.comments.format.boldPlaceholder")));
	const italic = () => apply((v, s, e) => wrapSelection(v, s, e, "_", t("pr.comments.format.italicPlaceholder")));
	const code = () => apply((v, s, e) => (v.slice(s, e).includes("\n") ? wrapSelection(v, s, e, "\n```\n", "") : wrapSelection(v, s, e, "`", t("pr.comments.format.codePlaceholder"))));
	const link = () => apply((v, s, e) => insertLink(v, s, e, t("pr.comments.format.linkPlaceholder")));

	const attach = async (files: File[]) => {
		if (!upload || files.length === 0) return;
		setUploadError(null);
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

	const fileInput = upload ? (
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
	) : null;
	const clear = () => { setValue(""); setUploadError(null); onCancel?.(); textareaRef.current?.blur(); };
	return (
		<div
			className={cn("flex min-w-0 flex-col gap-1.5", roomy ? "text-sm" : "text-xs")}
			data-testid="pr-comment-composer"
			onDragLeave={() => setDragging(false)}
			onDragOver={(event) => { if (upload && event.dataTransfer.types.includes("Files")) { event.preventDefault(); setDragging(true); } }}
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
						disabled={busy}
						onChange={(event) => setValue(event.target.value)}
						onKeyDown={onKeyDown}
						onPaste={onPaste}
						placeholder={placeholder}
						ref={textareaRef}
						value={value}
					/>
					<div className="flex min-w-0 items-center gap-1.5 text-xs">
						<Button className="h-7 gap-1 rounded-full px-3 text-xs" disabled={busy || uploading > 0 || !value.trim()} onClick={() => void submit()} size="sm" type="button">
							{busy ? <Loader2 aria-hidden="true" className="size-icon-sm animate-spin" /> : null}
							{submitLabel}
						</Button>
						{upload ? (
							<button className="inline-flex h-7 shrink-0 items-center gap-1 rounded-full px-2 font-medium text-muted-foreground transition-colors hover:bg-interactive-hover hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60" onClick={() => fileRef.current?.click()} onMouseDown={(event) => event.preventDefault()} title={t("pr.comments.simpleHint")} type="button">
								{uploading > 0 ? <Loader2 aria-hidden="true" className="size-icon-xs animate-spin" /> : <Paperclip aria-hidden="true" className="size-icon-xs" />}
								{t("pr.comments.attach.short")}
							</button>
						) : null}
						{onCancel || value ? <Button className="h-7 rounded-full px-2.5 text-xs" onClick={clear} onMouseDown={(event) => event.preventDefault()} size="sm" type="button" variant="ghost">{t("pr.comments.cancel")}</Button> : null}
					</div>
				</div>
			</div>
			{fileInput}
			{uploadError ? <p className="m-0 text-xs text-error" role="status">{uploadError}</p> : null}
			{error ? <p className="m-0 text-xs text-error" role="status">{error}</p> : null}
		</div>
	);
}
