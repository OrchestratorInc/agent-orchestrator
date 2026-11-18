import { createContext, useContext, useEffect, useState, type ReactNode } from "react";
import { Code2, Copy, FileText } from "@/components/app-icons";
import { useTranslation } from "react-i18next";
import type { ChatPathActionInput, EditorHandoffState } from "../../shared/editor-handoff";
import { aoBridge } from "../lib/bridge";
import { editorHandoffErrorMessage } from "../hooks/useEditorHandoff";
import { useUiStore } from "../stores/ui-store";
import { MenuEntries, type MenuEntry } from "./menu-model";
import { ContextMenu, ContextMenuContent, ContextMenuItem, ContextMenuSeparator, ContextMenuTrigger } from "./ui/context-menu";

import { OpenTargetIcon } from "./OpenTargetIcon";

type ChatPathMenuScope = { sessionId?: string; remote: boolean };

/** Native actions are available only for the local desktop session. */
export const ChatPathMenuContext = createContext<ChatPathMenuScope | undefined>(undefined);

/**
 * A path's actions as menu entries: copy, then `afterCopy`, reveal and open
 * with each editor. Shared by markdown anchors, inline-code paths and file
 * tabs; call it from menu content, which mounts only while open.
 * `showUnavailable` keeps the native rows, disabled, where they cannot run.
 * An absent path yields no entries and looks nothing up.
 */
export function useChatPathMenuEntries(
	path: string | undefined,
	{ scope, showUnavailable = false, afterCopy = [] }: { scope?: ChatPathMenuScope; showUnavailable?: boolean; afterCopy?: MenuEntry[] } = {},
): MenuEntry[] {
	const contextScope = useContext(ChatPathMenuContext);
	const context = scope ?? contextScope;
	const { t } = useTranslation();
	const showGlobalToast = useUiStore((state) => state.showGlobalToast);
	const [state, setState] = useState<EditorHandoffState>();
	const sessionId = context?.sessionId;
	const native = Boolean(path !== undefined && sessionId && !context?.remote && window.ao);
	useEffect(() => {
		if (!native || !sessionId) return;
		let active = true;
		setState(undefined);
		void aoBridge.editorHandoff.getState(sessionId).then(
			(result) => { if (active) setState(result); },
			() => { /* Copy remains available when native targets cannot be checked. */ },
		);
		return () => { active = false; };
	}, [native, sessionId]);
	if (path === undefined) return [];
	// Callers normalize once, preserving filenames containing literal percent escapes.
	const reference = path;
	const run = (action: () => Promise<void>) => {
		void action().catch((error: unknown) => showGlobalToast(t("chat.path.actionFailed"), editorHandoffErrorMessage(error) ?? undefined, "error"));
	};
	const perform = (action: ChatPathActionInput["action"], editorId?: ChatPathActionInput["editorId"]) =>
		aoBridge.editorHandoff.pathAction({ sessionId: sessionId!, path: reference, action, ...(editorId ? { editorId } : {}) });
	const fileManager = state?.targets.find((target) => target.kind === "file_manager");
	const editors = state?.targets.filter((target) => target.kind === "editor") ?? [];
	const entries: MenuEntry[] = [
		{
			kind: "item",
			id: "path-copy",
			icon: <Copy aria-hidden="true" />,
			label: t("chat.path.copy"),
			onSelect: () => run(async () => {
				await (native ? perform("copy") : aoBridge.clipboard.writeText(reference));
				showGlobalToast(t("terminal.copiedToClipboard"), undefined, { tone: "success", placement: "bottom-center", dedupeKey: "path-copied" });
			}),
		},
		...afterCopy,
	];
	if (native || showUnavailable) {
		entries.push({
			kind: "item",
			id: "path-reveal",
			disabled: !native || !state?.workspaceAvailable,
			icon: <OpenTargetIcon target={fileManager ?? { id: "file-manager", name: "", kind: "file_manager" }} />,
			label: fileManager?.name === "Finder" ? t("chat.path.revealInFinder") : t("chat.path.revealInFileManager"),
			onSelect: () => run(() => perform("reveal")),
		});
	}
	if (showUnavailable && editors.length === 0) {
		entries.push({ kind: "item", id: "path-open-editor", disabled: true, icon: <Code2 aria-hidden="true" />, label: t("shortcut.open-in-editor") });
	}
	if (native) {
		for (const editor of editors) {
			entries.push({
				kind: "item",
				id: `path-open-${editor.id}`,
				disabled: !state?.workspaceAvailable,
				icon: <OpenTargetIcon target={editor} />,
				label: t("chat.path.openWith", { editor: editor.name }),
				onSelect: () => run(() => perform("open", editor.id as ChatPathActionInput["editorId"])),
			});
		}
	}
	return entries;
}

/** A path's actions in the surrounding menu. */
export function ChatPathMenuItems({ path, showUnavailable = false }: { path: string; showUnavailable?: boolean }) {
	return <MenuEntries entries={useChatPathMenuEntries(path, { showUnavailable })} />;
}

export function ChatPathMenu({ path, onFileOpen, children }: { path: string; onFileOpen?: () => void; children: ReactNode }) {
	const { t } = useTranslation();
	return <ContextMenu>
		<ContextMenuTrigger asChild>{children}</ContextMenuTrigger>
		<ContextMenuContent className="min-w-52">
			{onFileOpen && <>
				<ContextMenuItem onSelect={onFileOpen}><FileText aria-hidden="true" />{t("link.openInAO")}</ContextMenuItem>
				<ContextMenuSeparator />
			</>}
			<ChatPathMenuItems path={path} />
		</ContextMenuContent>
	</ContextMenu>;
}
