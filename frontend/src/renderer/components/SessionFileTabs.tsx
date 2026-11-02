import { X } from "lucide-react";
import { useTranslation } from "react-i18next";
import type { SessionFileTabState } from "../lib/session-file-tabs";
import { TerminalTabFrame, terminalTabActionClassName } from "./TerminalTabFrame";
import { Tooltip, TooltipContent, TooltipTrigger } from "./ui/tooltip";
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
		<Tooltip>
			<TooltipTrigger asChild>
				<button
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
								className="size-2 rounded-full bg-foreground group-hover:hidden group-focus-within:hidden"
								data-testid="unsaved-tab-indicator"
							/>
							<X aria-hidden="true" className="hidden size-icon-sm group-hover:block group-focus-within:block" />
						</>
					) : <X className="size-icon-sm" aria-hidden="true" />}
				</button>
			</TooltipTrigger>
			<TooltipContent side="bottom">{t("files.closeTab", { name })}</TooltipContent>
		</Tooltip>
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
