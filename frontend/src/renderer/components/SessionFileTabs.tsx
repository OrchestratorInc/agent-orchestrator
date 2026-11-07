import { Copy, FolderOpen, Globe, SquareTerminal, X } from "lucide-react";
import { useContext } from "react";
import { useTranslation } from "react-i18next";
import { useOpenSessionArtifacts } from "../hooks/useEditorHandoff";
import type { FilesSource } from "../hooks/useSessionWorkspaceFiles";
import type { SessionFileTabState } from "../lib/session-file-tabs";
import { useUiStore } from "../stores/ui-store";
import { fileManagerName, terminalName } from "./ArtifactActionsMenu";
import { AppBrowserLinkContext } from "./AppLink";
import { ChatPathMenuContext, ChatPathMenuItems } from "./ChatPathMenu";
import { useCopyToClipboard } from "./SessionContextMenuItems";
import { ContextMenuItem, ContextMenuSeparator } from "./ui/context-menu";
import { TerminalTabFrame, terminalTabActionClassName } from "./TerminalTabFrame";
import { Pressable } from "./ui/pressable";
import { WorkspaceEntryIcon } from "./WorkspaceEntryIcon";

function basename(path: string): string {
	return path.split("/").pop() || path;
}

export function SessionFileTabs({
	state,
	onActivateFile,
	onCloseFile,
	dirtyPaths,
}: {
	state: SessionFileTabState;
	onActivateFile: (path: string) => void;
	onCloseFile: (path: string) => void;
	dirtyPaths?: ReadonlySet<string>;
}) {
	if (state.openPaths.length === 0) return null;
	return (
		<>
			{state.openPaths.map((path) => (
				<SessionFileTab
					active={state.activePath === path}
					dirty={dirtyPaths?.has(path)}
					key={path}
					onActivate={() => onActivateFile(path)}
					onClose={() => onCloseFile(path)}
					path={path}
				/>
			))}
		</>
	);
}

export function SessionFileTab({
 sourceLabel,
	active,
	dirty = false,
	onActivate,
	onClose,
	path,
}: {
 sourceLabel?: string;
	active: boolean;
	dirty?: boolean;
	onActivate: () => void;
	onClose: () => void;
	path: string;
}) {
	const { t } = useTranslation();
	const name = `${basename(path)}${sourceLabel ? ` · ${sourceLabel}` : ""}`;
	// The close button takes the file icon's slot on hover, matching the other
	// tabs. A dirty file keeps an always-visible dot in that slot; the dot swaps
	// to the cross on hover.
	const closeAction = (
		<Pressable
			aria-label={t("files.closeTab", { name })}
			className={terminalTabActionClassName}
			onClick={(event) => {
				event.stopPropagation();
				onClose();
			}}
			type="button"
		>
			{dirty ? (
				<>
					<span
						aria-hidden="true"
						className="size-2 rounded-full bg-foreground group-hover:hidden group-has-[:focus-visible]:hidden"
						data-testid="unsaved-tab-indicator"
					/>
					<X aria-hidden="true" className="hidden size-icon-sm group-hover:block group-has-[:focus-visible]:block" />
				</>
			) : <X className="size-icon-sm" aria-hidden="true" />}
		</Pressable>
	);
	return (
		<TerminalTabFrame
			action={closeAction}
			actionPinned={dirty}
			active={active}
			buttonProps={{
				"aria-label": name,
				"aria-selected": active,
				onClick: onActivate,
				role: "tab",
				tabIndex: active ? 0 : -1,
				title: sourceLabel ? `${path} · ${sourceLabel}` : path,
				type: "button",
			}}
			icon={<WorkspaceEntryIcon className="size-icon-sm shrink-0" kind="file" name={name} />}
			label={name}
		/>
	);
}

/**
 * A file tab's own right-click rows: the path actions chat paths have (copy,
 * reveal, open with an editor), the workspace-relative path, and Open in AO
 * Browser for an HTML page. An artifact lives outside the workspace, so it
 * gets the artifact menu's Show in actions instead. Native rows stay listed,
 * disabled, where this machine has no copy of the file.
 */
export function FileTabMenuItems({
	path,
	source,
	sessionId,
	remote,
}: {
	path: string;
	source?: FilesSource;
	sessionId: string;
	/** Another host's or a Cloud session: nothing to open on this machine. */
	remote: boolean;
}) {
	const { t } = useTranslation();
	const copyToClipboard = useCopyToClipboard();
	const openInBrowser = useContext(AppBrowserLinkContext);
	const openArtifacts = useOpenSessionArtifacts();
	const showGlobalToast = useUiStore((state) => state.showGlobalToast);
	const copyRelative = (
		<ContextMenuItem onSelect={() => copyToClipboard(path)}>
			<Copy aria-hidden="true" />
			{t("tabMenu.copyRelativePath")}
		</ContextMenuItem>
	);
	if (source?.kind === "artifact") {
		const showIn = (targetId: "file-manager" | "terminal", name: string) => {
			// The menu unmounts on select, so report failure from the promise.
			openArtifacts.mutateAsync({ sessionId, targetId, path }).catch((error: unknown) => {
				showGlobalToast(t("artifacts.showIn", { name }), error instanceof Error ? error.message : undefined, "error");
			});
		};
		return (
			<>
				<ContextMenuItem disabled={remote} onSelect={() => showIn("file-manager", fileManagerName())}>
					<FolderOpen aria-hidden="true" />
					{t("artifacts.showIn", { name: fileManagerName() })}
				</ContextMenuItem>
				<ContextMenuItem disabled={remote} onSelect={() => showIn("terminal", terminalName())}>
					<SquareTerminal aria-hidden="true" />
					{t("artifacts.showIn", { name: terminalName() })}
				</ContextMenuItem>
				<ContextMenuSeparator />
				{copyRelative}
			</>
		);
	}
	// Same rule as the file viewer's button: the Browser serves the live file.
	const workspaceFile = !source || source.kind === "workspace";
	return (
		<>
			<ChatPathMenuContext.Provider value={{ sessionId, remote }}>
				<ChatPathMenuItems afterCopy={copyRelative} path={path} showUnavailable />
			</ChatPathMenuContext.Provider>
			{/\.html?$/i.test(path) ? (
				<>
					<ContextMenuSeparator />
					<ContextMenuItem disabled={!openInBrowser || !workspaceFile} onSelect={() => openInBrowser?.(path)}>
						<Globe aria-hidden="true" />
						{t("inspector.openInAOBrowser")}
					</ContextMenuItem>
				</>
			) : null}
		</>
	);
}
