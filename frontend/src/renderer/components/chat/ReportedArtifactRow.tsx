import { useQueryClient } from "@tanstack/react-query";
import { useEffect } from "react";
import { useTranslation } from "react-i18next";
import { subscribeArtifactFileChanges } from "../../lib/artifact-file-events";
import type { ReportedArtifact } from "../../lib/render-frame";
import { useUiStore } from "../../stores/ui-store";
import { artifactFileIcon } from "../artifact-file-icon";
import { Pressable } from "../ui/pressable";
import { useChatArtifactFiles, useChatArtifactLinks, useChatRemoteHost } from "./chat-image-source";

/**
 * Follows a local session's artifact directory while a row's file is missing
 * from the session's list. A chat event brings the row, not the list, so a file
 * the agent has just reported can be missing from a list read moments earlier;
 * the stream rereads the list as it connects and on each change.
 */
function FollowArtifactList({ sessionId }: { sessionId: string }) {
	const queryClient = useQueryClient();
	useEffect(() => subscribeArtifactFileChanges(sessionId, queryClient), [sessionId, queryClient]);
	return null;
}

/**
 * Where an agent reported an HTML artifact mid-turn: one line naming it, not
 * the page. An artifact is a standalone document, so the thread never frames
 * it; the end-of-turn card previews it and the Artifacts tab shows it. The row
 * opens the artifact there, where it is now, when the session lists it.
 */
export function ReportedArtifactRow({ artifact }: { artifact: ReportedArtifact }) {
	const { t } = useTranslation();
	const links = useChatArtifactLinks(artifact.path);
	const chatArtifacts = useChatArtifactFiles();
	const remoteHost = useChatRemoteHost();
	const requestWorkspaceFileOpen = useUiStore((state) => state.requestWorkspaceFileOpen);
	const Icon = artifactFileIcon({ kind: "html", path: artifact.path });
	const body = (
		<>
			<Icon aria-hidden="true" className="size-3.5 shrink-0 text-muted-foreground" />
			<span className="shrink-0 font-medium text-foreground">{t("chat.artifact.label")}</span>
			<span className="min-w-0 truncate text-muted-foreground group-hover:text-foreground">{artifact.name}</span>
		</>
	);
	const rowClass = "flex h-8 min-w-0 max-w-full items-center gap-1.5 text-xs";
	if (!links) {
		const follows = chatArtifacts && !chatArtifacts.cloud && !remoteHost;
		return (
			<div className={rowClass} data-testid="reported-artifact-row" title={artifact.path}>
				{body}
				{follows ? <FollowArtifactList sessionId={chatArtifacts.sessionId} /> : null}
			</div>
		);
	}
	return (
		<Pressable
			aria-label={t("chat.artifact.openNamed", { name: artifact.name })}
			className={`group ${rowClass} rounded-md focus-visible:ring-1 focus-visible:ring-ring`}
			data-testid="reported-artifact-row"
			onClick={() => requestWorkspaceFileOpen(links.sessionId, links.path, links.hostId, "artifact")}
			title={links.path}
		>
			{body}
		</Pressable>
	);
}
