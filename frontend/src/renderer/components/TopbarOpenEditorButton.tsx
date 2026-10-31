import { ChevronDown, Code2, Copy, FolderOpen, SquareTerminal } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import type { OpenTarget, OpenTargetId } from "../../shared/editor-handoff";
import { useSessionShortcut } from "../hooks/useAppShortcut";
import { useEditorHandoffState, useOpenSessionTarget, workspaceCheckFailed, editorHandoffErrorMessage } from "../hooks/useEditorHandoff";
import { aoBridge } from "../lib/bridge";
import { useUiStore } from "../stores/ui-store";
import { TopbarActionError, TopbarButton } from "./TopbarButton";
import {
	DropdownMenu,
	DropdownMenuContent,
	DropdownMenuItem,
	DropdownMenuTrigger,
} from "./ui/dropdown-menu";
import { Tooltip, TooltipContent, TooltipTrigger } from "./ui/tooltip";
import {
	AndroidStudioIcon,
	AntigravityIcon,
	CursorIcon,
	FinderIcon,
	GhosttyIcon,
	JetBrainsIcon,
	SublimeIcon,
	VSCodeIcon,
	VSCodiumIcon,
	WindsurfIcon,
	ZedIcon,
} from "./icons";

const editorIcons: Record<string, typeof VSCodeIcon> = {
	vscode: VSCodeIcon,
	"vscode-insiders": VSCodeIcon,
	vscodium: VSCodiumIcon,
	cursor: CursorIcon,
	windsurf: WindsurfIcon,
	zed: ZedIcon,
	antigravity: AntigravityIcon,
	sublime: SublimeIcon,
	"android-studio": AndroidStudioIcon,
	intellij: JetBrainsIcon,
	webstorm: JetBrainsIcon,
	pycharm: JetBrainsIcon,
	goland: JetBrainsIcon,
	phpstorm: JetBrainsIcon,
	rubymine: JetBrainsIcon,
	clion: JetBrainsIcon,
	rider: JetBrainsIcon,
	fleet: JetBrainsIcon,
};

const editorColors: Record<string, string> = {
	vscode: "#1F9CF0",
	"vscode-insiders": "#1F9CF0",
	vscodium: "#2F80ED",
	antigravity: "#4285F4",
	sublime: "#FF9800",
	"android-studio": "#3DDC84",
};

export function TargetIcon({ target, className }: { target?: OpenTarget; className?: string }) {
	const [failedIcon, setFailedIcon] = useState<string>();
	if (target?.kind === "file_manager") {
		const Icon = target.name === "Finder" ? FinderIcon : FolderOpen;
		return <Icon className={className} aria-hidden="true" />;
	}
	if (target?.id === "terminal") return <SquareTerminal className={className} aria-hidden="true" />;
	if (target?.id === "ghostty") return <GhosttyIcon className={className} aria-hidden="true" />;
	const iconDataUrl = target?.iconDataUrl;
	if (iconDataUrl?.startsWith("data:image/png;base64,") && iconDataUrl !== failedIcon) {
		// Use the same SVG slot and sizing rules as every other menu icon.
		return (
			<svg width="24" height="24" viewBox="0 0 24 24" className={className} aria-hidden="true">
				<image href={iconDataUrl} width="24" height="24" onError={() => setFailedIcon(iconDataUrl)} />
			</svg>
		);
	}
	if (target?.kind === "terminal") {
		return <SquareTerminal className={className} aria-hidden="true" />;
	}
	const Icon = (target && editorIcons[target.id]) || Code2;
	const color = target ? editorColors[target.id] : undefined;
	return <Icon className={className} style={color ? { color } : undefined} aria-hidden="true" />;
}

// Electron main owns the complete handoff: it resolves the loopback-only
// workspace path, launches the native target, and persists editor preference.
// This renderer receives only safe target metadata and availability status.
export function TopbarOpenEditorButton({
	sessionId,
	projectId,
	sessionCreatedAt,
	sessionTerminated,
	style,
}: {
	sessionId: string;
	projectId: string;
	sessionCreatedAt?: string;
	sessionTerminated?: boolean;
	style?: React.CSSProperties;
}) {
	const { t } = useTranslation();
	const stateQuery = useEditorHandoffState(sessionId, { sessionCreatedAt, sessionTerminated });
	const open = useOpenSessionTarget();
	const showGlobalToast = useUiStore((state) => state.showGlobalToast);
	const [copyPending, setCopyPending] = useState(false);
	const state = stateQuery.data;
	const [menuOpen, setMenuOpen] = useState(false);
	const targets = state?.targets ?? [];
	const editors = targets.filter((target) => target.kind === "editor");
	const preferred = editors.find((target) => target.id === state?.preferredEditorId);
	const openInTargets = targets.filter((target) => target.kind === "file_manager" || target.kind === "terminal");
	const workspaceAvailable = state?.workspaceAvailable === true;
	const busy = stateQuery.isPending || open.isPending || copyPending;
	const mainDisabled = busy || !workspaceAvailable || !preferred;
	const menuDisabled = busy || !workspaceAvailable || targets.length === 0;

	const copyPath = async () => {
		setCopyPending(true);
		try {
			await aoBridge.editorHandoff.copyWorkspacePath(sessionId);
			showGlobalToast(t("shortcut.sessionPathCopied"));
		} catch (error) {
			showGlobalToast(t("shortcut.sessionPathCopyFailed"), editorHandoffErrorMessage(error) ?? undefined, "error");
			void stateQuery.refetch();
		} finally {
			setCopyPending(false);
		}
	};
	const launch = (targetId?: OpenTargetId) => {
		open.reset();
		open.mutate({ sessionId, projectId, ...(targetId ? { targetId } : {}) });
	};
	// ⌘O opens the workspace in the preferred editor, or in the first available
	// target (file manager, terminal) when no editor is installed.
	useSessionShortcut("open-in-editor", () => {
		if (busy || !workspaceAvailable) return;
		if (preferred) launch();
		else if (targets[0]) launch(targets[0].id);
	});
	const launchError = open.error instanceof Error ? open.error.message : null;
	const waitingLabel = stateQuery.isPending
		? t("editor.preparingWorkspace")
		: (workspaceCheckFailed(state) ? t("editor.checkingWorkspace") : null);
	const workspaceError = !waitingLabel && !workspaceAvailable
		? state?.unavailableReason ?? t("editor.workspaceUnavailable")
		: null;
	const noEditorInstalled = !stateQuery.isPending && workspaceAvailable && editors.length === 0;
	const mainTitle = waitingLabel
		?? (workspaceError
			?? (preferred
				? t("editor.openWorkspaceInTitle", { name: preferred.name })
				: (noEditorInstalled ? t("editor.noEditorInstalled") : t("editor.chooseEditorTitle"))));

	return (
		<>
			{/* Only a launch the user just attempted earns inline topbar space. An
			    unavailable workspace is a standing state: the controls are disabled
			    and the reason lives in the main button's tooltip. */}
			{launchError ? (
				<TopbarActionError className="max-w-content-max truncate" title={launchError}>
					{launchError}
				</TopbarActionError>
			) : null}
			<div
				className="inline-flex items-center gap-0 rounded-md transition-colors hover:bg-interactive-hover data-[state=open]:bg-interactive-hover"
				data-state={menuOpen ? "open" : "closed"}
				style={style}
			>
				<Tooltip>
					<TooltipTrigger asChild>
						<span className="inline-flex">
							<TopbarButton
								aria-label={waitingLabel
									?? (workspaceError
										?? (preferred
											? t("editor.openInAria", { name: preferred.name })
											: (noEditorInstalled ? t("editor.noEditorInstalled") : t("editor.chooseEditor"))))}
								className="hover:bg-transparent"
								disabled={mainDisabled}
								onClick={() => launch()}
								variant="icon"
							>
								<TargetIcon target={preferred} className="size-icon-md" />
							</TopbarButton>
						</span>
					</TooltipTrigger>
					<TooltipContent side="bottom">{mainTitle}</TooltipContent>
				</Tooltip>
				<DropdownMenu onOpenChange={(isOpen) => {
					setMenuOpen(isOpen);
					if (isOpen) void stateQuery.refreshTargets().catch(() => stateQuery.refetch());
				}}>
					<Tooltip>
						<TooltipTrigger asChild>
							<span className="inline-flex">
								<DropdownMenuTrigger asChild>
									<TopbarButton
										aria-label={t("editor.openOptionsAria")}
										className="hover:bg-transparent"
										disabled={menuDisabled}
										variant="icon"
									>
										<ChevronDown className="size-icon-sm" aria-hidden="true" />
									</TopbarButton>
								</DropdownMenuTrigger>
							</span>
						</TooltipTrigger>
						<TooltipContent side="bottom">{t("editor.openOptionsAria")}</TooltipContent>
					</Tooltip>
					<DropdownMenuContent align="end" className="min-w-52">
						{[...openInTargets, ...editors].map((target) => (
							<DropdownMenuItem key={target.id} onSelect={() => launch(target.id)}>
								<TargetIcon target={target} className="size-icon-sm" />
								{target.name}
							</DropdownMenuItem>
						))}
						<DropdownMenuItem onSelect={() => void copyPath()}>
							<Copy className="size-icon-sm" aria-hidden="true" />
							{t("editor.copyPath")}
						</DropdownMenuItem>
					</DropdownMenuContent>
				</DropdownMenu>
			</div>
		</>
	);
}
