import { useCallback } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { Archive, Code2, Copy, ExternalLink, GitBranch, Pencil, Pin, PinOff, Play } from "lucide-react";
import type { OpenTarget } from "../../shared/editor-handoff";
import { resumeAgentOnOpen, useCanResumeAgent } from "../hooks/useCanResumeAgent";
import { conversationQueryKey } from "../hooks/useConversation";
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
	// A local worker whose agent exited and that is not switching interfaces;
	// orchestrators and remote sessions resume elsewhere, so they skip the check.
	const canResume = useCanResumeAgent(session.hostId || isOrchestratorSession(session) ? undefined : session);
	// Remote and Cloud sessions have no local workspace for Electron to hand off.
	const hasLocalWorkspace = !session.hostId && !session.cloud;
	// Rename and pin go to the daemon that owns the session; a Cloud session has none.
	const isCloud = Boolean(session.cloud);

	const resume = () => {
		void resumeAgentOnOpen(session.id)
			.catch((error: unknown) => {
				showGlobalToast(t("inspector.resumeAgent"), error instanceof Error ? error.message : undefined, "error");
			})
			.finally(() => {
				void queryClient.invalidateQueries({ queryKey: workspaceQueryKeyForHost(session.hostId) });
				void queryClient.invalidateQueries({ queryKey: conversationQueryKey(session.id) });
			});
	};
	const hasManageItems = !isCloud || canResume;
	const hasLinkItems = Boolean(pr) || showBranch;
	const openPullRequest = (url: string, title: string) => {
		aoBridge.app.openExternal(url).catch((error: unknown) => {
			showGlobalToast(title, error instanceof Error ? error.message : undefined, "error");
		});
	};

	return (
		<>
			{hasLocalWorkspace ? <SessionWorkspaceMenuItems session={session} /> : null}
			{isCloud ? null : (
				<>
					<ContextMenuItem aria-label={t("shell.renameSession", { title: session.title })} onSelect={onRename}>
						<Pencil aria-hidden="true" />
						{t("shell.rename")}
					</ContextMenuItem>
					<ContextMenuItem onSelect={() => (session.isPinned ? unpinSession(session) : pinSession(session))}>
						{session.isPinned ? <PinOff aria-hidden="true" /> : <Pin aria-hidden="true" />}
						{session.isPinned ? t("shell.unpinSession") : t("shell.pinSession")}
					</ContextMenuItem>
				</>
			)}
			{canResume ? (
				<ContextMenuItem onSelect={resume}>
					<Play aria-hidden="true" />
					{t("command.resumeAgent")}
				</ContextMenuItem>
			) : null}
			{hasManageItems && hasLinkItems ? <ContextMenuSeparator /> : null}
			{pr ? (
				<>
					<ContextMenuItem onSelect={() => openPullRequest(pr.url, t("command.openPr", { number: pr.number }))}>
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
					{hasManageItems || hasLinkItems ? <ContextMenuSeparator /> : null}
					<ContextMenuItem onSelect={onArchive}>
						<Archive aria-hidden="true" />
						{t("shell.archiveSession")}
					</ContextMenuItem>
				</>
			) : null}
		</>
	);
}

/** Open in the default editor and copy the worktree path: the same Electron
 *  handoff as the session topbar's editor button. The editor row holds its
 *  place while the lookup runs so the menu does not jump when it resolves. */
function SessionWorkspaceMenuItems({ session }: { session: WorkspaceSession }) {
	const { t } = useTranslation();
	const showGlobalToast = useUiStore((state) => state.showGlobalToast);
	const { data: state, isPending } = useEditorHandoffState(session.id, {
		sessionCreatedAt: session.createdAt,
		sessionTerminated: session.isTerminated,
	});
	const open = useOpenSessionTarget();
	const editor = state?.workspaceAvailable
		? state.targets.find((target) => target.kind === "editor" && target.id === state.preferredEditorId)
		: undefined;
	// The menu unmounts as soon as an item is picked, so report failure from the
	// promise rather than from mutate callbacks that only run while mounted.
	const launch = (target: OpenTarget) => {
		open.mutateAsync({ sessionId: session.id, projectId: session.workspaceId, targetId: target.id }).catch((error: unknown) => {
			showGlobalToast(t("editor.openInTarget", { name: target.name }), error instanceof Error ? error.message : undefined, "error");
		});
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
			{isPending ? (
				<ContextMenuItem disabled>
					<Code2 aria-hidden="true" />
					{t("editor.preparingWorkspace")}
				</ContextMenuItem>
			) : editor ? (
				<ContextMenuItem onSelect={() => launch(editor)}>
					<TargetIcon target={editor} />
					{t("editor.openInTarget", { name: editor.name })}
				</ContextMenuItem>
			) : null}
			{state?.workspaceAvailable === false ? null : (
				<ContextMenuItem onSelect={copyPath}>
					<Copy aria-hidden="true" />
					{t("editor.copyPath")}
				</ContextMenuItem>
			)}
			{state?.workspaceAvailable === false ? null : <ContextMenuSeparator />}
		</>
	);
}
