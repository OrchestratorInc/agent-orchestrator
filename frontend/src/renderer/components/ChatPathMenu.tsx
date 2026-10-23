import { createContext, useContext, useEffect, useState, type ReactNode } from "react";
import { Copy, FileText } from "lucide-react";
import { useTranslation } from "react-i18next";
import type { ChatPathActionInput, EditorHandoffState } from "../../shared/editor-handoff";
import { aoBridge } from "../lib/bridge";
import { editorHandoffErrorMessage } from "../hooks/useEditorHandoff";
import { useUiStore } from "../stores/ui-store";
import { ContextMenu, ContextMenuContent, ContextMenuItem, ContextMenuSeparator, ContextMenuTrigger } from "./ui/context-menu";

import { OpenTargetIcon } from "./OpenTargetIcon";

/** Native actions are available only for the local desktop session. */
export const ChatPathMenuContext = createContext<{ sessionId?: string; remote: boolean } | undefined>(undefined);

/** Shared by markdown anchors and inline-code paths; mounts only while the menu is open. */
export function ChatPathMenuItems({ path }: { path: string }) {
	const context = useContext(ChatPathMenuContext);
	const { t } = useTranslation();
	const showGlobalToast = useUiStore((state) => state.showGlobalToast);
	const [state, setState] = useState<EditorHandoffState>();
	const sessionId = context?.sessionId;
	const native = Boolean(sessionId && !context?.remote && window.ao);
	// Callers normalize once, preserving filenames containing literal percent escapes.
	const reference = path;
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
	const run = (action: () => Promise<void>) => {
		void action().catch((error: unknown) => showGlobalToast(t("chat.path.actionFailed"), editorHandoffErrorMessage(error) ?? undefined, "error"));
	};
	const perform = (action: ChatPathActionInput["action"], editorId?: ChatPathActionInput["editorId"]) =>
		aoBridge.editorHandoff.pathAction({ sessionId: sessionId!, path: reference, action, ...(editorId ? { editorId } : {}) });
	const fileManager = state?.targets.find((target) => target.kind === "file_manager");
	return <>
		<ContextMenuItem onSelect={() => run(async () => {
			await (native ? perform("copy") : aoBridge.clipboard.writeText(reference));
			showGlobalToast(t("terminal.copiedToClipboard"), undefined, { tone: "success", placement: "bottom-center", dedupeKey: "path-copied" });
		})}>
			<Copy aria-hidden="true" />{t("chat.path.copy")}
		</ContextMenuItem>
		{native && <ContextMenuItem disabled={!state?.workspaceAvailable} onSelect={() => run(() => perform("reveal"))}>
			<OpenTargetIcon target={fileManager ?? { id: "file-manager", name: "", kind: "file_manager" }} />{fileManager?.name === "Finder" ? t("chat.path.revealInFinder") : t("chat.path.revealInFileManager")}
		</ContextMenuItem>}
		{native && state?.targets.filter((target) => target.kind === "editor").map((editor) => (
			<ContextMenuItem key={editor.id} disabled={!state.workspaceAvailable} onSelect={() => run(() => perform("open", editor.id as ChatPathActionInput["editorId"]))}>
				<OpenTargetIcon target={editor} />{t("chat.path.openWith", { editor: editor.name })}
			</ContextMenuItem>
		))}
	</>;
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
