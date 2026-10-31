import { useMemo } from "react";
import { AlertTriangle, Loader2 } from "lucide-react";
import { useTranslation } from "react-i18next";
import { useCloudReviewerConversation } from "../../hooks/useCloudReviewerConversation";
import { sessionUiKey } from "../../lib/hosts";
import { useSessionLinkNavigation } from "../../lib/use-session-link-navigation";
import type { WorkspaceSession } from "../../types/workspace";
import { ChatWorkspace } from "./ChatWorkspace";

/**
 * A Cloud review that runs in Chat, in the reviewer's tab as a local
 * reviewer chat is: the review prompt, the reviewer's work and verdict, and a
 * composer for follow-up questions. Its approvals are declined in the sandbox,
 * so it never asks.
 */
export function CloudReviewerChatSurface({ session, reviewId, harness, hideHeader = false }: {
	session: WorkspaceSession;
	reviewId: string;
	harness: string;
	hideHeader?: boolean;
}) {
	const { t } = useTranslation();
	const conversation = useCloudReviewerConversation(session, reviewId, harness);
	const openSessionLink = useSessionLinkNavigation(undefined, "cloud");
	const draftOwner = useMemo(() => ({ sessionId: sessionUiKey(`review:${reviewId}`), incarnation: reviewId }), [reviewId]);
	const { snapshot } = conversation;
	if (conversation.isLoading)
		return (
			<Centered>
				<Loader2 className="size-4 animate-spin" />
				{t("inspector.loadingSession")}
			</Centered>
		);
	if (!snapshot)
		return (
			<Centered>
				<AlertTriangle className="size-4 text-destructive" />
				{conversation.loadError ?? t("shell.couldNotLoadSessions")}
			</Centered>
		);
	const running = snapshot.turns.some((turn) => turn.state === "running");
	return (
		<ChatWorkspace
			snapshot={snapshot}
			draftOwner={draftOwner}
			onSessionLinkOpen={openSessionLink}
			sessionTitle={t("terminal.reviewer")}
			sessionRole="worker"
			hideHeader={hideHeader}
			busy={conversation.busy}
			commandError={conversation.error ?? conversation.loadError}
			models={[]}
			onSend={(text, _attachments, clientMessageId) => conversation.send(text, clientMessageId)}
			onInterrupt={running ? conversation.interrupt : undefined}
		/>
	);
}

function Centered({ children }: { children: React.ReactNode }) {
	return <div className="flex h-full items-center justify-center gap-2 text-xs text-muted-foreground">{children}</div>;
}
