import { autocompletion } from "@codemirror/autocomplete";
import { defaultKeymap, history, historyKeymap, indentWithTab } from "@codemirror/commands";
import { markdown } from "@codemirror/lang-markdown";
import { indentUnit, syntaxHighlighting, defaultHighlightStyle } from "@codemirror/language";
import { searchKeymap } from "@codemirror/search";
import { EditorState } from "@codemirror/state";
import { drawSelection, EditorView, highlightActiveLine, keymap, lineNumbers } from "@codemirror/view";
import { Bold, Heading1, Heading2, Heading3, Heading4, Heading5, Image, Italic, Link, List, ListChecks, ListOrdered, MoreHorizontal, Pilcrow, Quote, Strikethrough } from "lucide-react";
import { useCallback, useEffect, useRef } from "react";
import { useTranslation } from "react-i18next";
import { Button } from "./ui/button";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuLabel, DropdownMenuSeparator, DropdownMenuTrigger } from "./ui/dropdown-menu";

const editorTheme = EditorView.theme({
	"&": {
		backgroundColor: "var(--color-bg-primary)",
		color: "var(--color-text-primary)",
		fontSize: "var(--font-size-sm)",
		height: "100%",
	},
	".cm-scroller": { fontFamily: "var(--font-family-mono)", overflow: "auto" },
	".cm-content": { caretColor: "var(--color-text-primary)", minHeight: "100%", padding: "1rem 1.5rem" },
	".cm-cursor, .cm-dropCursor": { borderLeftColor: "var(--color-text-primary)" },
	".cm-line": { padding: "0" },
	".cm-gutters": { backgroundColor: "var(--color-bg-secondary)", border: "0", color: "var(--color-text-muted)" },
	".cm-activeLineGutter, .cm-activeLine": { backgroundColor: "var(--color-interactive-hover)" },
	".cm-selectionBackground, ::selection": { backgroundColor: "var(--color-interactive-active) !important" },
}, { dark: false });

export function MarkdownEditor({ value, onChange, filePath }: { value: string; onChange: (value: string) => void; filePath: string }) {
	const { t } = useTranslation();
	const containerRef = useRef<HTMLDivElement>(null);
	const viewRef = useRef<EditorView | null>(null);
	const onChangeRef = useRef(onChange);
	const userEditedRef = useRef(false);
	const applyingExternalValueRef = useRef(false);
	onChangeRef.current = onChange;

	useEffect(() => {
		if (!containerRef.current) return;
		userEditedRef.current = false;
		const lineSeparator = value.includes("\r\n") ? "\r\n" : "\n";
		const state = EditorState.create({
			doc: value,
			extensions: [
				EditorState.lineSeparator.of(lineSeparator),
				lineNumbers(),
				indentUnit.of("  "),
				markdown(),
				history(),
				drawSelection(),
				highlightActiveLine(),
				autocompletion(),
				syntaxHighlighting(defaultHighlightStyle),
				editorTheme,
				keymap.of([...defaultKeymap, ...historyKeymap, ...searchKeymap, indentWithTab]),
				EditorView.updateListener.of((update) => {
					if (!update.docChanged || applyingExternalValueRef.current) return;
					userEditedRef.current = true;
					onChangeRef.current(update.state.sliceDoc());
				}),
			],
		});
		const view = new EditorView({ state, parent: containerRef.current });
		viewRef.current = view;
		view.focus();
		return () => {
			view.destroy();
			viewRef.current = null;
		};
		// The editor is intentionally created once per file. React state changes
		// must not reset the user's selection or undo history.
		// eslint-disable-next-line react-hooks/exhaustive-deps
	}, [filePath]);

	useEffect(() => {
		const view = viewRef.current;
		if (!view || userEditedRef.current || view.state.sliceDoc() === value) return;
		applyingExternalValueRef.current = true;
		view.dispatch({ changes: { from: 0, to: view.state.doc.length, insert: value } });
		applyingExternalValueRef.current = false;
	}, [value]);

	const dispatchText = useCallback((insert: string, from: number, to: number, selection?: { anchor: number; head: number }) => {
		const view = viewRef.current;
		if (!view) return;
		view.dispatch({ changes: { from, to, insert }, selection });
		view.focus();
	}, []);
	const wrapSelection = useCallback((before: string, after: string) => {
		const view = viewRef.current;
		if (!view) return;
		const { from, to } = view.state.selection.main;
		const selected = view.state.sliceDoc(from, to);
		dispatchText(`${before}${selected}${after}`, from, to, { anchor: from + before.length, head: to + before.length });
	}, [dispatchText]);
	const setBlockPrefix = useCallback((prefix: string) => {
		const view = viewRef.current;
		if (!view) return;
		const selection = view.state.selection.main;
		const line = view.state.doc.lineAt(selection.from);
		const content = line.text.replace(/^(?:#{1,6}\s+|[-*+]\s+(?:\[[ xX]\]\s+)?|\d+[.)]\s+|>\s?)/, "");
		const next = prefix ? `${prefix}${content}` : content;
		dispatchText(next, line.from, line.to, { anchor: line.from + Math.min(selection.anchor - line.from, next.length), head: line.from + Math.min(selection.head - line.from, next.length) });
	}, [dispatchText]);
	const insertLink = useCallback(() => {
		const view = viewRef.current;
		if (!view) return;
		const { from, to } = view.state.selection.main;
		const selected = view.state.sliceDoc(from, to) || "link text";
		const text = `[${selected}](url)`;
		const urlStart = from + selected.length + 3;
		dispatchText(text, from, to, { anchor: urlStart, head: urlStart + 3 });
	}, [dispatchText]);
	const insertImage = useCallback(() => {
		const view = viewRef.current;
		if (!view) return;
		const { from, to } = view.state.selection.main;
		const text = "![alt text](image-url)";
		dispatchText(text, from, to, { anchor: from + 2, head: from + 10 });
	}, [dispatchText]);
	const action = (label: string, icon: React.ReactNode, onClick: () => void) => (
		<Button aria-label={label} className="text-muted-foreground hover:text-foreground" onMouseDown={(event) => event.preventDefault()} onClick={onClick} size="icon-sm" type="button" variant="ghost">{icon}</Button>
	);
	return (
		<div className="flex h-full min-h-64 flex-col">
			<div aria-label={t("markdownEditor.toolbarLabel")} className="flex min-h-9 shrink-0 items-center gap-0.5 overflow-x-auto border-b border-border bg-background px-2 py-1" role="toolbar">
				{action(t("markdownEditor.paragraph"), <Pilcrow />, () => setBlockPrefix(""))}
				{action(t("markdownEditor.heading", { level: 1 }), <Heading1 />, () => setBlockPrefix("# "))}
				{action(t("markdownEditor.heading", { level: 2 }), <Heading2 />, () => setBlockPrefix("## "))}
				{action(t("markdownEditor.heading", { level: 3 }), <Heading3 />, () => setBlockPrefix("### "))}
				<span aria-hidden="true" className="mx-1 h-5 w-px bg-border" />
				{action(t("markdownEditor.bold"), <Bold />, () => wrapSelection("**", "**"))}
				{action(t("markdownEditor.italic"), <Italic />, () => wrapSelection("*", "*"))}
				{action(t("markdownEditor.strikethrough"), <Strikethrough />, () => wrapSelection("~~", "~~"))}
				<span aria-hidden="true" className="mx-1 h-5 w-px bg-border" />
				{action(t("markdownEditor.bulletedList"), <List />, () => setBlockPrefix("- "))}
				{action(t("markdownEditor.numberedList"), <ListOrdered />, () => setBlockPrefix("1. "))}
				{action(t("markdownEditor.taskList"), <ListChecks />, () => setBlockPrefix("- [ ] "))}
				{action(t("markdownEditor.blockQuote"), <Quote />, () => setBlockPrefix("> "))}
				{action(t("markdownEditor.insertLink"), <Link />, insertLink)}
				{action(t("markdownEditor.insertImage"), <Image />, insertImage)}
				<DropdownMenu>
					<DropdownMenuTrigger asChild>{action(t("markdownEditor.moreFormatting"), <MoreHorizontal />, () => undefined)}</DropdownMenuTrigger>
					<DropdownMenuContent align="end" className="w-48">
						<DropdownMenuLabel>{t("markdownEditor.headings")}</DropdownMenuLabel>
						<DropdownMenuItem onSelect={() => setBlockPrefix("#### ")}><Heading4 /> {t("markdownEditor.heading", { level: 4 })}</DropdownMenuItem>
						<DropdownMenuItem onSelect={() => setBlockPrefix("##### ")}><Heading5 /> {t("markdownEditor.heading", { level: 5 })}</DropdownMenuItem>
						<DropdownMenuSeparator />
						<DropdownMenuItem onSelect={() => setBlockPrefix("")}><Pilcrow /> {t("markdownEditor.paragraph")}</DropdownMenuItem>
					</DropdownMenuContent>
				</DropdownMenu>
			</div>
			<div aria-label={t("markdownEditor.edit", { filePath })} className="min-h-0 flex-1 overflow-hidden" ref={containerRef} role="textbox" />
		</div>
	);
}
