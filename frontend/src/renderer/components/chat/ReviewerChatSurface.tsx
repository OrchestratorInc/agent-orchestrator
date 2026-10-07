import { useMemo } from "react";
import { AlertTriangle, Loader2 } from "lucide-react";
import { useTranslation } from "react-i18next";
import { useReviewerConversation, useReviewerConversationCommands, useReviewerConversationModels } from "../../hooks/useReviewerConversation";
import { useHostConnection } from "../../hooks/useHostConnection";
import { sessionUiKey } from "../../lib/hosts";
import { useSessionLinkNavigation } from "../../lib/use-session-link-navigation";
import { ChatWorkspace } from "./ChatWorkspace";

export function ReviewerChatSurface({ reviewId, workerSessionId, hostId, hideHeader = false }: { reviewId: string; workerSessionId: string; hostId?: string; hideHeader?: boolean }) {
	const { t } = useTranslation();
	const { snapshot, isLoading, error, hasOlder, isLoadingOlder, loadOlder } = useReviewerConversation(reviewId, hostId);
	const catalog = useReviewerConversationModels(reviewId, Boolean(snapshot && snapshot.controller.state !== "stopped"), hostId);
	const commands = useReviewerConversationCommands(reviewId, hostId);
	const openSessionLink = useSessionLinkNavigation(hostId);
	const { baseUrl: remoteBase } = useHostConnection(hostId);
	const draftOwner = useMemo(() => ({ sessionId: sessionUiKey(`review:${reviewId}`, hostId), incarnation: reviewId }), [hostId, reviewId]);
	if (isLoading)
		return (
			<Centered>
				<Loader2 className="size-4 animate-spin" />
				{t("inspector.loadingSession")}
			</Centered>
		);
	if (error || !snapshot)
		return (
			<Centered>
				<AlertTriangle className="size-4 text-destructive" />
				{error ?? t("shell.couldNotLoadSessions")}
			</Centered>
		);
	return (
		<ChatWorkspace
			uiSessionId={hostId ? sessionUiKey(reviewId, hostId) : undefined}
			assetBaseUrl={remoteBase}
			remoteHostId={hostId}
			snapshot={snapshot}
			draftOwner={draftOwner}
			onSessionLinkOpen={openSessionLink}
			sessionTitle={t("terminal.reviewer")}
			sessionRole="worker"
			hideHeader={hideHeader}
			busy={commands.busy}
			commandError={commands.error ?? catalog.error}
			models={catalog.models}
			onChooseSettings={commands.chooseSettings}
			approvalModes={["auto"]}
			hasOlder={hasOlder}
			loadingOlder={isLoadingOlder}
			onLoadOlder={loadOlder}
			onSend={(text, attachments, clientMessageId) => commands.send({ text, attachments, clientMessageId })}
			onDecide={commands.resolve}
			onResolveInput={commands.resolveInput}
			onInterrupt={commands.interrupt}
			onResumeAgent={() => commands.resumeAgent(workerSessionId)}
			resumingAgent={commands.resumingAgent}
			resumeError={commands.resumeError}
		/>
	);
}

function Centered({ children }: { children: React.ReactNode }) {
	return <div className="flex h-full items-center justify-center gap-2 text-xs text-muted-foreground">{children}</div>;
}
