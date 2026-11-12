import { useMemo } from "react";
import { AlertTriangle, Loader2 } from "@/components/app-icons";
import { useTranslation } from "react-i18next";
import { useReviewerConversation, useReviewerConversationCommands, useReviewerConversationModels } from "../../hooks/useReviewerConversation";
import { useHostConnection } from "../../hooks/useHostConnection";
import { useChatMessageFocus, useChatNavigationRequest, type ChatMessageFocusTarget } from "../../hooks/useChatMessageFocus";
import { sessionUiKey } from "../../lib/hosts";
import { useSessionLinkNavigation } from "../../lib/use-session-link-navigation";
import { ChatWorkspace } from "./ChatWorkspace";

export function ReviewerChatSurface({
	reviewId,
	workerSessionId,
	hostId,
	hostUnreachable = false,
	hideHeader = false,
	onOpenShell,
	openingShell,
	shellError,
}: {
	reviewId: string;
	workerSessionId: string;
	hostId?: string;
	hostUnreachable?: boolean;
	hideHeader?: boolean;
	/** Opens a shell in the worker's worktree: the stopped banner's other way on. */
	onOpenShell?: () => void;
	openingShell?: boolean;
	shellError?: string;
}) {
	const { t } = useTranslation();
	const { snapshot, isLoading, error, hasOlder, isLoadingOlder, loadOlder } = useReviewerConversation(reviewId, hostId);
	const catalog = useReviewerConversationModels(reviewId, Boolean(snapshot && snapshot.controller.state !== "stopped"), hostId);
	const commands = useReviewerConversationCommands(reviewId, hostId);
	const openSessionLink = useSessionLinkNavigation(hostId);
	const { baseUrl: remoteBase } = useHostConnection(hostId);
	const draftOwner = useMemo(() => ({ sessionId: sessionUiKey(`review:${reviewId}`, hostId), incarnation: reviewId }), [hostId, reviewId]);
	// One surface shows each review's chat in turn: a target is for its review, once.
	const annotationNavigation = useChatNavigationRequest<ChatMessageFocusTarget>(reviewId);
	useChatMessageFocus(sessionUiKey(workerSessionId, hostId), reviewId, Boolean(snapshot), annotationNavigation.navigate);
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
				{hostUnreachable && error ? t("remote.hostOffline") : error ?? t("shell.couldNotLoadSessions")}
			</Centered>
		);
	return (
		<ChatWorkspace
			annotationNavigationRequest={annotationNavigation.request}
			onAnnotationNavigationHandled={annotationNavigation.handled}
			uiSessionId={hostId ? sessionUiKey(reviewId, hostId) : undefined}
			assetBaseUrl={remoteBase}
			remoteHostId={hostId}
			snapshot={snapshot}
			newWorkDisabled={hostUnreachable}
			composerLockedPlaceholder={hostUnreachable ? t("remote.offlineCopy") : undefined}
			savedCopy={hostUnreachable}
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
			onOpenShell={hostUnreachable ? undefined : onOpenShell}
			openingShell={openingShell}
			shellError={shellError}
		/>
	);
}

function Centered({ children }: { children: React.ReactNode }) {
	return <div className="flex h-full items-center justify-center gap-2 text-xs text-muted-foreground">{children}</div>;
}
