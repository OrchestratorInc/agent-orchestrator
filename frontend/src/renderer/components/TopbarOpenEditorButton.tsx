import { ChevronDown, Copy } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import type { OpenTargetId } from "../../shared/editor-handoff";
import { useSessionShortcut } from "../hooks/useAppShortcut";
import { useEditorHandoffState, useOpenSessionTarget, workspaceCheckFailed, editorHandoffErrorMessage } from "../hooks/useEditorHandoff";
import { aoBridge } from "../lib/bridge";
import { useUiStore } from "../stores/ui-store";
import { captureTopbarAction } from "../lib/topbar-telemetry";
import type { TopbarView } from "../lib/topbar-actions";
import { NAV_ROW_HIGHLIGHT_HOST_CLASS, NavRowHighlight } from "./NavRowHighlight";
import { cn } from "../lib/utils";
import { TopbarButton } from "./TopbarButton";
import {
	DropdownMenu,
	DropdownMenuContent,
	DropdownMenuItem,
	DropdownMenuTrigger,
} from "./ui/dropdown-menu";
import { Tooltip, TooltipContent, TooltipTrigger } from "./ui/tooltip";
import { OpenTargetIcon } from "./OpenTargetIcon";
export { OpenTargetIcon as TargetIcon } from "./OpenTargetIcon";

// Electron main owns the complete handoff: it resolves the loopback-only
// workspace path, launches the native target, and persists editor preference.
// This renderer receives only safe target metadata and availability status.
export function TopbarOpenEditorButton({
	sessionId,
	projectId,
	sessionCreatedAt,
	sessionTerminated,
	style,
	telemetryView,
}: {
	sessionId: string;
	projectId: string;
	sessionCreatedAt?: string;
	sessionTerminated?: boolean;
	style?: React.CSSProperties;
	/** Set when mounted in a session top bar, so its clicks are counted. */
	telemetryView?: TopbarView;
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
			showGlobalToast(t("terminal.copiedToClipboard"), undefined, { tone: "success", placement: "bottom-center", dedupeKey: "path-copied" });
		} catch (error) {
			showGlobalToast(t("shortcut.sessionPathCopyFailed"), editorHandoffErrorMessage(error) ?? undefined, "error");
			void stateQuery.refetch();
		} finally {
			setCopyPending(false);
		}
	};
	const launch = (targetId?: OpenTargetId) => {
		const target = targetId ? targets.find((entry) => entry.id === targetId) : preferred;
		// Report from the promise: mutate callbacks are dropped if the button
		// unmounts (e.g. a session switch) while the launch is in flight.
		open.mutateAsync({ sessionId, projectId, ...(targetId ? { targetId } : {}) }).catch((error: unknown) => {
			showGlobalToast(t("editor.openInTarget", { name: target?.name ?? "" }), error instanceof Error ? error.message : undefined, "error");
		});
	};
	// ⌘O opens the workspace in the preferred editor, or in the first available
	// target (file manager, terminal) when no editor is installed.
	useSessionShortcut("open-in-editor", () => {
		if (busy || !workspaceAvailable) return;
		if (preferred) launch();
		else if (targets[0]) launch(targets[0].id);
	});
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
		<div
			className={cn(NAV_ROW_HIGHLIGHT_HOST_CLASS, "topbar-control-group isolate inline-flex items-center gap-0 rounded-md bg-transparent")}
			data-state={menuOpen ? "open" : "closed"}
			style={style}
		>
			<NavRowHighlight active={menuOpen} disabled={mainDisabled && menuDisabled} />
			<Tooltip>
				<TooltipTrigger asChild>
					<span className="inline-flex">
						<TopbarButton
							aria-label={waitingLabel
								?? (workspaceError
									?? (preferred
										? t("editor.openInAria", { name: preferred.name })
										: (noEditorInstalled ? t("editor.noEditorInstalled") : t("editor.chooseEditor"))))}
							className="bg-transparent hover:bg-transparent"
							highlight={false}
							disabled={mainDisabled}
							onClick={() => {
								if (telemetryView) captureTopbarAction("open_editor", telemetryView);
								launch();
							}}
							variant="icon"
						>
							<OpenTargetIcon target={preferred} className="size-icon-md" />
						</TopbarButton>
					</span>
				</TooltipTrigger>
				<TooltipContent shortcut="open-in-editor" side="bottom">{mainTitle}</TooltipContent>
			</Tooltip>
			<DropdownMenu onOpenChange={(isOpen) => {
				setMenuOpen(isOpen);
				if (isOpen && telemetryView) captureTopbarAction("editor_menu", telemetryView);
				if (isOpen) void stateQuery.refreshTargets().catch(() => stateQuery.refetch());
			}}>
				<span className="inline-flex">
					<DropdownMenuTrigger asChild>
						<TopbarButton
							aria-label={t("editor.openOptionsAria")}
							className="bg-transparent hover:bg-transparent"
							highlight={false}
							disabled={menuDisabled}
							variant="icon"
						>
							<ChevronDown className="size-icon-sm" aria-hidden="true" />
						</TopbarButton>
					</DropdownMenuTrigger>
				</span>

				<DropdownMenuContent align="end" className="min-w-52">
					{[...openInTargets, ...editors].map((target) => (
						<DropdownMenuItem key={target.id} onSelect={() => launch(target.id)}>
							<OpenTargetIcon target={target} className="size-icon-sm" />
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
	);
}
