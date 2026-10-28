import { useCallback } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { Archive, Copy, ExternalLink, GitBranch, Pencil, Pin, PinOff, Play } from "lucide-react";
import type { OpenTarget } from "../../shared/editor-handoff";
import { canResumeAgent, resumeAgentOnOpen } from "../hooks/useCanResumeAgent";
import { editorHandoffErrorMessage, useEditorHandoffState, useOpenSessionTarget } from "../hooks/useEditorHandoff";
import { usePinSession, useUnpinSession } from "../hooks/usePinSession";
import { workspaceQueryKeyForHost } from "../hooks/useWorkspaceQuery";
import { aoBridge } from "../lib/bridge";
import { sessionBranchPresentation } from "../lib/session-branch";
import { useUiStore } from "../stores/ui-store";
import { isOrchestratorSession, primaryPR, type WorkspaceSession } from "../types/workspace";
import { TargetIcon } from "./TopbarOpenEditorButton";
import { ContextMenuItem, ContextMenuSeparator } from "./ui/context-menu";

/** Copy from a menu, which has no inline place to confirm the copy. */
export function useCopyToClipboard() {
	const { t } = useTranslation();
	const showGlobalToast = useUiStore((state) => state.showGlobalToast);
	return useCallback(
		(text: string) => {
			void aoBridge.clipboard.writeText(text).then(
				() => showGlobalToast(t("shell.copied")),
				() => showGlobalToast(t("shell.copyFailed"), undefined, "error"),
			);
		},
		[showGlobalToast, t],
	);
}

/** Radix mounts menu content only while open, so these lookups run on demand
 *  rather than once per sidebar row. */
export function SessionContextMenuItems({
	session,
	onArchive,
	onRename,
}: {
	session: WorkspaceSession;
	/** Absent where the session cannot be archived from here. */
	onArchive?: () => void;
	onRename: () => void;
}) {
	const { t } = useTranslation();
	const queryClient = useQueryClient();
	const showGlobalToast = useUiStore((state) => state.showGlobalToast);
	const copyToClipboard = useCopyToClipboard();
	const { mutate: pinSession } = usePinSession();
	const { mutate: unpinSession } = useUnpinSession();
	const branch = sessionBranchPresentation(session).branch;
	const showBranch = Boolean(branch) && !isOrchestratorSession(session) && branch !== `session/${session.id}`;
	const pr = primaryPR(session);
	// Same eligibility as the hover warm-up: a local worker whose agent exited.
	const canResume = canResumeAgent(session) && !session.hostId && !isOrchestratorSession(session);
	// Remote and Cloud sessions have no local workspace for Electron to hand off.
	const hasLocalWorkspace = !session.hostId && !session.cloud;

	const resume = () => {
		void resumeAgentOnOpen(session.id)
			.catch((error: unknown) => {
				showGlobalToast(t("inspector.resumeAgent"), error instanceof Error ? error.message : undefined, "error");
			})
			.finally(() => {
				void queryClient.invalidateQueries({ queryKey: workspaceQueryKeyForHost(session.hostId) });
			});
	};

	return (
		<>
			{hasLocalWorkspace ? <SessionWorkspaceMenuItems session={session} /> : null}
			<ContextMenuItem aria-label={t("shell.renameSession", { title: session.title })} onSelect={onRename}>
				<Pencil aria-hidden="true" />
				{t("shell.rename")}
			</ContextMenuItem>
			<ContextMenuItem onSelect={() => (session.isPinned ? unpinSession(session) : pinSession(session))}>
				{session.isPinned ? <PinOff aria-hidden="true" /> : <Pin aria-hidden="true" />}
				{session.isPinned ? t("shell.unpinSession") : t("shell.pinSession")}
			</ContextMenuItem>
			{canResume ? (
				<ContextMenuItem onSelect={resume}>
					<Play aria-hidden="true" />
					{t("command.resumeAgent")}
				</ContextMenuItem>
			) : null}
			{pr || showBranch ? <ContextMenuSeparator /> : null}
			{pr ? (
				<>
					<ContextMenuItem onSelect={() => void aoBridge.app.openExternal(pr.url)}>
						<ExternalLink aria-hidden="true" />
						{t("command.openPr", { number: pr.number })}
					</ContextMenuItem>
					<ContextMenuItem onSelect={() => copyToClipboard(pr.url)}>
						<Copy aria-hidden="true" />
						{t("command.copyPrUrl", { number: pr.number })}
					</ContextMenuItem>
				</>
			) : null}
			{showBranch && branch ? (
				<ContextMenuItem onSelect={() => copyToClipboard(branch)}>
					<GitBranch aria-hidden="true" />
					{t("command.copyBranch")}
				</ContextMenuItem>
			) : null}
			{onArchive ? (
				<>
					<ContextMenuSeparator />
					<ContextMenuItem onSelect={onArchive}>
						<Archive aria-hidden="true" />
						{t("shell.archiveSession")}
					</ContextMenuItem>
				</>
			) : null}
		</>
	);
}

/** Open in the preferred editor or file manager, and copy the worktree path:
 *  the same Electron handoff as the session topbar's editor button. */
function SessionWorkspaceMenuItems({ session }: { session: WorkspaceSession }) {
	const { t } = useTranslation();
	const showGlobalToast = useUiStore((state) => state.showGlobalToast);
	const { data: state } = useEditorHandoffState(session.id, {
		sessionCreatedAt: session.createdAt,
		sessionTerminated: session.isTerminated,
	});
	const open = useOpenSessionTarget();
	if (!state?.workspaceAvailable) return null;
	const editors = state.targets.filter((target) => target.kind === "editor");
	const editor = editors.find((target) => target.id === state.preferredEditorId) ?? editors[0];
	const fileManager = state.targets.find((target) => target.kind === "file_manager");
	const launch = (target: OpenTarget) => {
		open.mutate(
			{ sessionId: session.id, projectId: session.workspaceId, targetId: target.id },
			{
				onError: (error) =>
					showGlobalToast(t("editor.openInTarget", { name: target.name }), error.message, "error"),
			},
		);
	};
	const copyPath = () => {
		aoBridge.editorHandoff.copyWorkspacePath(session.id).then(
			() => showGlobalToast(t("shortcut.sessionPathCopied")),
			(error: unknown) =>
				showGlobalToast(t("shortcut.sessionPathCopyFailed"), editorHandoffErrorMessage(error) ?? undefined, "error"),
		);
	};
	return (
		<>
			{editor ? (
				<ContextMenuItem onSelect={() => launch(editor)}>
					<TargetIcon target={editor} />
					{t("editor.openInTarget", { name: editor.name })}
				</ContextMenuItem>
			) : null}
			{fileManager ? (
				<ContextMenuItem onSelect={() => launch(fileManager)}>
					<TargetIcon target={fileManager} />
					{t("editor.openInTarget", { name: fileManager.name })}
				</ContextMenuItem>
			) : null}
			<ContextMenuItem onSelect={copyPath}>
				<Copy aria-hidden="true" />
				{t("editor.copyPath")}
			</ContextMenuItem>
			<ContextMenuSeparator />
		</>
	);
}
