import { useQueryClient } from "@tanstack/react-query";
import { useEffect } from "react";
import { useTranslation } from "react-i18next";
import { cloudSessionsQueryKey, sharedCloudWorkspacesQueryKey } from "../../hooks/useWorkspaceQuery";
import { subscribeArtifactFileChanges } from "../../lib/artifact-file-events";
import type { ReportedArtifact } from "../../lib/render-frame";
import { cn } from "../../lib/utils";
import { useUiStore } from "../../stores/ui-store";
import { artifactFileIcon } from "../artifact-file-icon";
import { Pressable } from "../ui/pressable";
import { useChatArtifactFiles, useChatArtifactLinks, useChatRemoteHost } from "./chat-image-source";

/**
 * Follows a local session's artifact directory while a row's file is missing
 * from the session's list. A chat event brings the row, not the list, so a file
 * the agent has just reported can be missing from a list read moments earlier;
 * the stream rereads the list as it connects and on each change, and closes
 * once the file is listed.
 */
function FollowArtifactList({ sessionId }: { sessionId: string }) {
	const queryClient = useQueryClient();
	useEffect(() => subscribeArtifactFileChanges(sessionId, queryClient), [sessionId, queryClient]);
	return null;
}

const ROW_CLASS = "flex h-8 min-w-0 max-w-full items-center gap-1.5 text-xs";

/**
 * Where an agent reported an HTML artifact mid-turn: one line naming it, not
 * the page. An artifact is a standalone document, so the thread never frames
 * it; the end-of-turn card previews it and the Artifacts tab shows it.
 *
 * - Listed by the session: the row opens it in the Artifacts tab, where it is now.
 * - A cloud session's file not listed yet: the row still opens it. The click
 *   rereads the session list once, and the Artifacts tab shows the file as soon
 *   as the list has it, as a file link in a cloud chat does. Nothing polls.
 * - Otherwise (deleted, never registered, or on a remote host's list): the row
 *   is muted and says the file is not in the session's artifacts. A local chat
 *   follows the directory meanwhile, so a file reported moments ago becomes
 *   openable once it is listed.
 */
export function ReportedArtifactRow({ artifact }: { artifact: ReportedArtifact }) {
	const { t } = useTranslation();
	const queryClient = useQueryClient();
	const links = useChatArtifactLinks(artifact.path);
	const chatArtifacts = useChatArtifactFiles();
	const remoteHost = useChatRemoteHost();
	const requestWorkspaceFileOpen = useUiStore((state) => state.requestWorkspaceFileOpen);
	const Icon = artifactFileIcon({ kind: "html", path: artifact.path });
	const cloud = chatArtifacts?.cloud;
	const openable = Boolean(links) || Boolean(cloud && chatArtifacts);

	if (!openable) {
		const unavailable = t("chat.artifact.unavailable");
		const follows = chatArtifacts && !cloud && !remoteHost;
		return (
			<div className={ROW_CLASS} data-testid="reported-artifact-row" title={unavailable}>
				<Icon aria-hidden="true" className="size-3.5 shrink-0 text-passive" />
				<span className="shrink-0 font-medium text-muted-foreground">{t("chat.artifact.label")}</span>
				<span className="min-w-0 shrink truncate text-muted-foreground">{artifact.name}</span>
				<span aria-hidden="true" className="shrink-0 text-passive">·</span>
				<span className="min-w-0 flex-1 truncate text-muted-foreground">{unavailable}</span>
				{follows ? <FollowArtifactList sessionId={chatArtifacts.sessionId} /> : null}
			</div>
		);
	}

	const open = () => {
		if (links) {
			requestWorkspaceFileOpen(links.sessionId, links.path, links.hostId, "artifact");
			return;
		}
		// A cloud session's list can lag the chat; read it again now rather than
		// at its next refresh, and open the path for the tab to show once listed.
		if (!chatArtifacts) return;
		void queryClient.invalidateQueries({ queryKey: cloud?.shared ? sharedCloudWorkspacesQueryKey : cloudSessionsQueryKey });
		requestWorkspaceFileOpen(chatArtifacts.sessionId, artifact.path, undefined, "artifact");
	};
	return (
		<Pressable
			aria-label={t("chat.artifact.openNamed", { name: artifact.name })}
			className={cn("group rounded-md focus-visible:ring-1 focus-visible:ring-ring", ROW_CLASS)}
			data-testid="reported-artifact-row"
			onClick={open}
			title={links?.path ?? artifact.path}
		>
			<Icon aria-hidden="true" className="size-3.5 shrink-0 text-muted-foreground" />
			<span className="shrink-0 font-medium text-foreground">{t("chat.artifact.label")}</span>
			<span className="min-w-0 truncate text-muted-foreground group-hover:text-foreground">{artifact.name}</span>
		</Pressable>
	);
}
