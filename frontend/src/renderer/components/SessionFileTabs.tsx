import { Copy, FolderOpen, Globe, SquareTerminal, X } from "@/components/app-icons";
import { useContext } from "react";
import { useTranslation } from "react-i18next";
import { useOpenSessionArtifacts } from "../hooks/useEditorHandoff";
import type { FilesSource } from "../hooks/useSessionWorkspaceFiles";
import type { SessionFileTabState } from "../lib/session-file-tabs";
import { useUiStore } from "../stores/ui-store";
import { fileManagerName, terminalName } from "./ArtifactActionsMenu";
import { AppBrowserLinkContext } from "./AppLink";
import { useChatPathMenuEntries } from "./ChatPathMenu";
import type { MenuEntry } from "./menu-model";
import { useCopyToClipboard } from "./SessionContextMenuItems";
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
			onClose={onClose}
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

/** A file, diff or artifact tab, for its menu. */
export type FileTabMenuTarget = {
	path: string;
	source?: FilesSource;
	sessionId: string;
	/** Another host's or a Cloud session: nothing to open on this machine. */
	remote: boolean;
};

/**
 * A file tab's own menu rows: the path actions chat paths have (copy, reveal,
 * open with an editor), the workspace-relative path, and Open in AO Browser
 * for an HTML page. An artifact lives outside the workspace, so it gets the
 * artifact menu's Show in actions instead. Native rows stay listed, disabled,
 * where this machine has no copy of the file. No file, no entries.
 */
export function useFileTabMenuEntries(file: FileTabMenuTarget | undefined): MenuEntry[] {
	const { t } = useTranslation();
	const copyToClipboard = useCopyToClipboard();
	const openInBrowser = useContext(AppBrowserLinkContext);
	const openArtifacts = useOpenSessionArtifacts();
	const showGlobalToast = useUiStore((state) => state.showGlobalToast);
	const artifact = file?.source?.kind === "artifact";
	const copyRelative: MenuEntry[] = file
		? [{ kind: "item", id: "copy-relative-path", icon: <Copy aria-hidden="true" />, label: t("tabMenu.copyRelativePath"), onSelect: () => copyToClipboard(file.path) }]
		: [];
	const pathEntries = useChatPathMenuEntries(file && !artifact ? file.path : undefined, {
		scope: file ? { sessionId: file.sessionId, remote: file.remote } : undefined,
		showUnavailable: true,
		afterCopy: copyRelative,
	});
	if (!file) return [];
	const { path, source, sessionId, remote } = file;
	if (artifact) {
		const showIn = (targetId: "file-manager" | "terminal", name: string) => {
			// The menu unmounts on select, so report failure from the promise.
			openArtifacts.mutateAsync({ sessionId, targetId, path }).catch((error: unknown) => {
				showGlobalToast(t("artifacts.showIn", { name }), error instanceof Error ? error.message : undefined, "error");
			});
		};
		return [
			{ kind: "item", id: "show-in-file-manager", disabled: remote, icon: <FolderOpen aria-hidden="true" />, label: t("artifacts.showIn", { name: fileManagerName() }), onSelect: () => showIn("file-manager", fileManagerName()) },
			{ kind: "item", id: "show-in-terminal", disabled: remote, icon: <SquareTerminal aria-hidden="true" />, label: t("artifacts.showIn", { name: terminalName() }), onSelect: () => showIn("terminal", terminalName()) },
			{ kind: "separator", id: "artifact-sep" },
			...copyRelative,
		];
	}
	if (!/\.html?$/i.test(path)) return pathEntries;
	// Same rule as the file viewer's button: the Browser serves the live file.
	const workspaceFile = !source || source.kind === "workspace";
	return [
		...pathEntries,
		{ kind: "separator", id: "browser-sep" },
		{ kind: "item", id: "open-in-ao-browser", disabled: !openInBrowser || !workspaceFile, icon: <Globe aria-hidden="true" />, label: t("inspector.openInAOBrowser"), onSelect: () => openInBrowser?.(path) },
	];
}
