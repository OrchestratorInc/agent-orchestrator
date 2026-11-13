import { Loader2, Pencil } from "lucide-react";
import { useEffect, useRef, useState, type KeyboardEvent, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { PREditConflict, type PREditContent, type PREditField, type usePREdit } from "../hooks/usePREdit";
import type { SessionPRSummary } from "../hooks/useSessionScmSummary";
import { cn } from "../lib/utils";
import { insertLink, wrapSelection } from "./PRCommentComposer";
import { PRDescriptionBody } from "./PRDescriptionBody";
import { Button } from "./ui/button";
import { Input } from "./ui/input";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "./ui/tabs";

/** The providers' title limits: GitHub takes 256 characters, GitLab 255. */
export const PR_TITLE_MAX_LENGTH = 255;
/** GitHub's limit for a PR description, in characters. */
export const PR_GITHUB_BODY_MAX_LENGTH = 65536;

type PREdit = ReturnType<typeof usePREdit>;
type BlockedKey = "pr.edit.blocked.noPermission" | "pr.edit.blocked.notOpen" | "pr.edit.blocked.unsupported";

const reasonKey = (reason: PREditContent["reason"]): BlockedKey =>
	reason === "not_open" ? "pr.edit.blocked.notOpen" : reason === "unsupported" ? "pr.edit.blocked.unsupported" : "pr.edit.blocked.noPermission";

/** Why this PR can't be edited, as far as is known before asking the provider again. */
function blockedKey(pr: SessionPRSummary, content: PREditContent | undefined): BlockedKey | undefined {
	if (pr.state === "merged" || pr.state === "closed") return "pr.edit.blocked.notOpen";
	return content && !content.canEdit ? reasonKey(content.reason) : undefined;
}

// A text field reads the provider's CRLF line endings back as LF.
const unixLines = (text: string) => text.replace(/\r\n/g, "\n");

type Editing = {
	/** The provider's text as the editor read it, sent back so a change made elsewhere is caught. */
	base: string;
	draft: string;
	/** The last save was refused because the text changed elsewhere; base is now that newer text. */
	conflict: boolean;
};

/**
 * One field's editor: closed, reading the provider's current text, or open on
 * a draft. A save the provider refuses as a conflict keeps the draft, takes
 * the newer text as its base and says so, so saving again is a choice.
 */
function usePRFieldEditor(field: PREditField, edit: PREdit, pr: SessionPRSummary, validate: (value: string) => string | undefined) {
	const { t } = useTranslation();
	const [editing, setEditing] = useState<Editing | null>(null);
	const [loading, setLoading] = useState(false);
	const [error, setError] = useState<string | null>(null);
	const [confirmingDiscard, setConfirmingDiscard] = useState(false);
	const blocked = blockedKey(pr, edit.access.data);
	const saving = edit.save.isPending;
	// Set before the first await, so a second click or ⌘↵ can't save twice.
	const busyRef = useRef(false);

	const open = async () => {
		if (blocked || busyRef.current || editing) return;
		busyRef.current = true;
		setLoading(true);
		setError(null);
		try {
			const content = await edit.load();
			if (!content.canEdit) setError(t(reasonKey(content.reason)));
			else setEditing({ base: content[field], draft: unixLines(content[field]), conflict: false });
		} catch (cause) {
			setError(cause instanceof Error ? cause.message : t("pr.edit.loadFailed"));
		} finally {
			busyRef.current = false;
			setLoading(false);
		}
	};
	const value = editing ? (field === "title" ? editing.draft.trim() : editing.draft) : "";
	const changed = Boolean(editing) && value !== unixLines(editing!.base);
	const close = () => {
		setEditing(null);
		setError(null);
		setConfirmingDiscard(false);
	};
	// Cancel and Escape close an untouched editor at once; a changed draft is
	// only thrown away by a second, explicit Discard.
	const cancel = () => {
		if (saving) return;
		if (changed) setConfirmingDiscard(true);
		else close();
	};
	const discard = () => {
		if (!saving) close();
	};
	const keepEditing = () => setConfirmingDiscard(false);
	const change = (draft: string) => {
		setEditing((current) => (current ? { ...current, draft } : current));
		setConfirmingDiscard(false);
		if (!editing?.conflict) setError(null);
	};
	const save = async () => {
		if (!editing || busyRef.current) return;
		// Nothing to send, unless the text moved elsewhere and this draft is to replace it.
		if (!changed) return close();
		const invalid = validate(value);
		if (invalid) return setError(invalid);
		busyRef.current = true;
		setError(null);
		setConfirmingDiscard(false);
		try {
			await edit.save.mutateAsync({ base: editing.base, field, value });
			setEditing(null);
		} catch (cause) {
			if (cause instanceof PREditConflict) {
				// The newer text becomes the base: saving again knowingly replaces it.
				const latest = await edit.load().catch(() => undefined);
				if (latest) setEditing((current) => (current ? { ...current, base: latest[field], conflict: true } : current));
			}
			setError(cause instanceof Error ? cause.message : t("pr.edit.saveFailed"));
		} finally {
			busyRef.current = false;
		}
	};
	// ⌘↵ saves and Escape closes only this editor, wherever in it the focus is.
	// Escape never discards a changed draft itself: it asks, and a second Escape
	// goes back to editing.
	const onKeyDown = (event: KeyboardEvent) => {
		if ((event.metaKey || event.ctrlKey) && event.key === "Enter") {
			event.preventDefault();
			void save();
		} else if (event.key === "Escape" && !saving) {
			event.preventDefault();
			event.stopPropagation();
			if (confirmingDiscard) keepEditing();
			else cancel();
		}
	};
	return { blocked: blocked ? t(blocked) : undefined, cancel, change, changed, confirmingDiscard, discard, editing, error, keepEditing, loading, onKeyDown, open, save, saving };
}

function EditorActions({ editor, hint }: { editor: ReturnType<typeof usePRFieldEditor>; hint?: boolean }) {
	const { t } = useTranslation();
	if (editor.confirmingDiscard) {
		return (
			<div className="flex min-w-0 flex-wrap items-center gap-1.5" data-testid="pr-edit-discard-confirm">
				<span aria-live="polite" className="mr-1 text-xs text-muted-foreground">{t("pr.edit.discardPrompt")}</span>
				<Button className="h-7 rounded-full px-2.5 text-xs" onClick={editor.discard} size="sm" type="button" variant="destructive-ghost">{t("pr.edit.discard")}</Button>
				<Button className="h-7 rounded-full px-2.5 text-xs" onClick={editor.keepEditing} size="sm" type="button" variant="ghost">{t("pr.edit.keepEditing")}</Button>
			</div>
		);
	}
	return (
		<div className="flex min-w-0 flex-wrap items-center gap-1.5">
			<Button className="h-7 gap-1 rounded-full px-3 text-xs" disabled={editor.saving} onClick={() => void editor.save()} size="sm" type="button">
				{editor.saving ? <Loader2 aria-hidden="true" className="size-icon-sm animate-spin" /> : null}
				{editor.editing?.conflict ? t("pr.edit.saveAnyway") : t("pr.edit.save")}
			</Button>
			<Button className="h-7 rounded-full px-2.5 text-xs" disabled={editor.saving} onClick={editor.cancel} size="sm" type="button" variant="ghost">{t("pr.edit.cancel")}</Button>
			{hint ? <span className="ml-auto hidden text-xs text-passive @md:inline">{t("pr.edit.hint")}</span> : null}
		</div>
	);
}

const EditorError = ({ children }: { children: string | null }) => (children ? <p className="m-0 text-xs text-error" role="alert">{children}</p> : null);

/** The button that opens an editor; when the PR can't be edited it stays, greyed out, and says why. */
function EditButton({ editor, label, showLabel }: { editor: ReturnType<typeof usePRFieldEditor>; label: string; showLabel?: boolean }) {
	return (
		<button
			aria-disabled={editor.blocked ? true : undefined}
			aria-label={label}
			className={cn(
				"inline-flex h-7 shrink-0 items-center justify-center gap-1.5 rounded-full text-control font-medium text-muted-foreground transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60",
				showLabel ? "px-2.5" : "w-7",
				editor.blocked ? "cursor-not-allowed opacity-50" : "hover:bg-interactive-hover hover:text-foreground",
			)}
			disabled={editor.loading}
			onClick={() => void editor.open()}
			title={editor.blocked ?? label}
			type="button"
		>
			{editor.loading ? <Loader2 aria-hidden="true" className="size-icon-sm animate-spin" /> : <Pencil aria-hidden="true" className="size-icon-sm" />}
			{showLabel ? <span>{label}</span> : null}
		</button>
	);
}

/**
 * The PR's title in its tab, with a pencil that edits it in place: Enter or
 * ⌘↵ saves, Escape cancels (asking first when the title was changed).
 * Without `edit` it is the plain title.
 */
export function PREditableTitle({ className, edit, pr }: { className?: string; edit?: PREdit; pr: SessionPRSummary }) {
	const titleClass = "m-0 min-w-0 text-heading font-medium leading-tight text-foreground [overflow-wrap:anywhere]";
	if (!edit) return <h2 className={cn(titleClass, className)}>{pr.title}</h2>;
	return <EditableTitle className={className} edit={edit} pr={pr} titleClass={titleClass} />;
}

function EditableTitle({ className, edit, pr, titleClass }: { className?: string; edit: PREdit; pr: SessionPRSummary; titleClass: string }) {
	const { t } = useTranslation();
	const editor = usePRFieldEditor("title", edit, pr, (value) => (value ? undefined : t("pr.edit.titleRequired")));
	const inputRef = useRef<HTMLInputElement>(null);
	const open = Boolean(editor.editing);
	useEffect(() => {
		if (open) inputRef.current?.select();
	}, [open]);
	if (!editor.editing) {
		return (
			<div className={cn("flex min-w-0 flex-col gap-1.5", className)}>
				<div className="flex min-w-0 items-start gap-1.5">
					<h2 className={titleClass}>{pr.title}</h2>
					<EditButton editor={editor} label={t("pr.edit.editTitle")} />
				</div>
				<EditorError>{editor.error}</EditorError>
			</div>
		);
	}
	return (
		<div className={cn("flex min-w-0 flex-col gap-2", className)} data-testid="pr-title-editor" onKeyDown={editor.onKeyDown}>
			<Input
				aria-label={t("pr.edit.titleLabel")}
				className="h-9 border-border bg-background text-base font-medium focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/25"
				disabled={editor.saving}
				maxLength={PR_TITLE_MAX_LENGTH}
				onChange={(event) => editor.change(event.target.value)}
				onKeyDown={(event) => {
					if (event.key === "Enter" && !event.nativeEvent.isComposing) {
						event.preventDefault();
						event.stopPropagation();
						void editor.save();
					}
				}}
				ref={inputRef}
				value={editor.editing.draft}
			/>
			<EditorError>{editor.error}</EditorError>
			<EditorActions editor={editor} />
		</div>
	);
}

/**
 * The PR's description in its tab, with an Edit that turns it into a markdown
 * editor: Write and Preview (drawn exactly as the description is), ⌘B, ⌘I, ⌘K
 * and ⌘E to format, ⌘↵ to save and Escape to cancel. `children` is the
 * description as it reads.
 */
export function PREditableDescription({ children, edit, pr }: { children: ReactNode; edit: PREdit; pr: SessionPRSummary }) {
	const { t } = useTranslation();
	const tooLong = (value: string) => (pr.provider === "github" && value.length > PR_GITHUB_BODY_MAX_LENGTH ? t("pr.edit.tooLong", { limit: PR_GITHUB_BODY_MAX_LENGTH.toLocaleString() }) : undefined);
	const editor = usePRFieldEditor("body", edit, pr, tooLong);
	const [mode, setMode] = useState<"write" | "preview">("write");
	const textareaRef = useRef<HTMLTextAreaElement>(null);
	const open = Boolean(editor.editing);
	useEffect(() => {
		if (!open) return;
		setMode("write");
		textareaRef.current?.focus();
		textareaRef.current?.setSelectionRange(0, 0);
	}, [open]);

	if (!editor.editing) {
		return (
			<div className="flex min-w-0 flex-col gap-2">
				<div className="flex min-w-0 items-center gap-2">
					<h3 className="m-0 min-w-0 flex-1 truncate text-control font-normal text-muted-foreground">{t("pr.description.title")}</h3>
					<EditButton editor={editor} label={t("pr.edit.editDescription")} showLabel />
				</div>
				<EditorError>{editor.error}</EditorError>
				{children}
			</div>
		);
	}
	const format = (edit: (value: string, start: number, end: number) => { text: string; selectionStart: number; selectionEnd: number }) => {
		const element = textareaRef.current;
		if (!element) return;
		const next = edit(element.value, element.selectionStart, element.selectionEnd);
		editor.change(next.text);
		requestAnimationFrame(() => {
			element.focus();
			element.setSelectionRange(next.selectionStart, next.selectionEnd);
		});
	};
	const shortcuts: Record<string, () => void> = {
		b: () => format((v, s, e) => wrapSelection(v, s, e, "**", t("pr.comments.format.boldPlaceholder"))),
		i: () => format((v, s, e) => wrapSelection(v, s, e, "_", t("pr.comments.format.italicPlaceholder"))),
		k: () => format((v, s, e) => insertLink(v, s, e, t("pr.comments.format.linkPlaceholder"))),
		e: () => format((v, s, e) => (v.slice(s, e).includes("\n") ? wrapSelection(v, s, e, "\n```\n", "") : wrapSelection(v, s, e, "`", t("pr.comments.format.codePlaceholder")))),
	};
	const draft = editor.editing.draft;
	return (
		<Tabs className="@container flex min-w-0 flex-col gap-2" data-testid="pr-description-editor" onKeyDown={editor.onKeyDown} onValueChange={(next) => setMode(next as "write" | "preview")} value={mode}>
			<div className="flex min-w-0 items-center gap-2">
				<h3 className="m-0 min-w-0 flex-1 truncate text-control font-normal text-muted-foreground">{t("pr.edit.descriptionLabel")}</h3>
				<TabsList aria-label={t("pr.edit.descriptionLabel")} className="h-8 shrink-0">
					<TabsTrigger className="px-2.5 text-xs" value="write">{t("pr.edit.write")}</TabsTrigger>
					<TabsTrigger className="px-2.5 text-xs" value="preview">{t("pr.edit.preview")}</TabsTrigger>
				</TabsList>
			</div>
			<TabsContent className="min-w-0 data-[state=inactive]:hidden" forceMount value="write">
				<textarea
					aria-label={t("pr.edit.descriptionLabel")}
					// Grows with what is written, up to most of the window, then scrolls.
					className="block max-h-[60vh] min-h-48 w-full min-w-0 resize-none rounded-xl border border-border bg-background px-4 py-3 font-mono text-xs leading-relaxed text-foreground [field-sizing:content] placeholder:text-muted-foreground focus-visible:border-ring focus-visible:outline-none focus-visible:ring-3 focus-visible:ring-ring/25"
					disabled={editor.saving}
					onChange={(event) => editor.change(event.target.value)}
					onKeyDown={(event) => {
						const shortcut = (event.metaKey || event.ctrlKey) && !event.shiftKey && !event.altKey ? shortcuts[event.key] : undefined;
						if (!shortcut) return;
						event.preventDefault();
						shortcut();
					}}
					placeholder={t("pr.edit.descriptionPlaceholder")}
					ref={textareaRef}
					spellCheck
					value={draft}
				/>
			</TabsContent>
			<TabsContent className="min-h-48 min-w-0 rounded-xl border border-border px-4 py-3" value="preview">
				{draft.trim() ? <PRDescriptionBody cloud={false} pr={{ ...pr, description: draft, descriptionTruncated: false, detailsObservedAt: pr.detailsObservedAt ?? pr.updatedAt }} /> : <p className="m-0 text-sm text-passive">{t("pr.edit.previewEmpty")}</p>}
			</TabsContent>
			<EditorError>{editor.error}</EditorError>
			<EditorActions editor={editor} hint />
		</Tabs>
	);
}
