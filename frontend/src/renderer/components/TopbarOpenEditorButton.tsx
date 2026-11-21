import { ChevronDown, Copy } from "@/components/app-icons";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import type { OpenTargetId } from "../../shared/editor-handoff";
import { useAppMenuAction, useSessionMenuAvailable } from "../hooks/useAppMenuCommand";
import { useSessionShortcut } from "../hooks/useAppShortcut";
import { useEditorHandoffState, useOpenSessionTarget, workspaceCheckFailed, editorHandoffErrorMessage } from "../hooks/useEditorHandoff";
import { aoBridge } from "../lib/bridge";
import { useUiStore } from "../stores/ui-store";
import { captureTopbarAction } from "../lib/topbar-telemetry";
import type { TopbarView } from "../lib/topbar-actions";
import { Button, GHOST_PILL_CLASS } from "./ui/button";
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
	const [menuOpen, setMenuOpen] = useState(false);
	const state = stateQuery.data;
	const targets = state?.targets ?? [];
	const editors = targets.filter((target) => target.kind === "editor");
	const preferred = editors.find((target) => target.id === state?.preferredEditorId);
	// With no installed editor chosen, the main button opens the workspace in the
	// platform file manager (Finder, File Explorer, xdg-open) instead of sitting empty.
	const fileManager = targets.find((target) => target.kind === "file_manager");
	const mainTarget = preferred ?? fileManager;
	const openInTargets = targets.filter((target) => target.kind === "file_manager" || target.kind === "terminal");
	const workspaceAvailable = state?.workspaceAvailable === true;
	const busy = stateQuery.isPending || open.isPending || copyPending;
	const mainDisabled = busy || !workspaceAvailable || !mainTarget;
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
	// Without an explicit target, main opens the preferred editor.
	const launchMain = () => (preferred ? launch() : mainTarget && launch(mainTarget.id));
	// ⌘O does what the main button does.
	useSessionShortcut("open-in-editor", () => {
		if (busy || !workspaceAvailable) return;
		launchMain();
	});
	useSessionMenuAvailable("open-in-editor", !mainDisabled);
	useSessionMenuAvailable("open-in-file-manager", !busy && workspaceAvailable && Boolean(fileManager));
	useAppMenuAction("open-in-file-manager", () => {
		if (fileManager) launch(fileManager.id);
	});
	const waitingLabel = stateQuery.isPending
		? t("editor.preparingWorkspace")
		: (workspaceCheckFailed(state) ? t("editor.checkingWorkspace") : null);
	const workspaceError = !waitingLabel && !workspaceAvailable
		? state?.unavailableReason ?? t("editor.workspaceUnavailable")
		: null;
	const mainTitle = waitingLabel
		?? (workspaceError
			?? (mainTarget ? t("editor.openWorkspaceInTitle", { name: mainTarget.name }) : t("editor.chooseEditorTitle")));

	return (
		<div
			className={`${GHOST_PILL_CLASS} inline-flex items-center gap-0 rounded-md has-[:focus-visible]:before:inset-0 has-[:focus-visible]:before:opacity-100 ${mainDisabled && menuDisabled ? "before:hidden" : ""}`}
			data-state={menuOpen ? "open" : "closed"}
			style={style}
		>
			<Tooltip>
				<TooltipTrigger asChild>
					<span className="inline-flex">
						<Button type="button" size="icon-sm"
							aria-label={waitingLabel
								?? (workspaceError
									?? (mainTarget ? t("editor.openInAria", { name: mainTarget.name }) : t("editor.chooseEditor")))}
							disabled={mainDisabled}
							onClick={() => {
								if (telemetryView) captureTopbarAction("open_editor", telemetryView);
								launchMain();
							}}
							variant="quiet"
							className="before:hidden"
						>
							<OpenTargetIcon target={mainTarget} className="size-icon-md" />
						</Button>
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
						<Button type="button" size="icon-sm"
							aria-label={t("editor.openOptionsAria")}
							disabled={menuDisabled}
							variant="quiet"
							className="before:hidden"
						>
							<ChevronDown className="size-icon-sm" aria-hidden="true" />
						</Button>
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
