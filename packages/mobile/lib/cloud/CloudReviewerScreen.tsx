import * as Crypto from "expo-crypto";
import { useNavigation } from "expo-router";
import { useIsFocused } from "expo-router/react-navigation";
import { useEffect, useLayoutEffect, useMemo, useState } from "react";
import { StyleSheet, Text, View } from "react-native";
import { KeyboardAvoidingView } from "react-native-keyboard-controller";
import { ChatTimeline } from "../chat/ChatTimeline";
import { ReviewerComposer } from "../chat/ReviewerComposer";
import { useChatKeyboard } from "../chat/useChatKeyboard";
import { Spinner } from "../Spinner";
import { SYSTEM_GLASS_HEADER } from "../stackScreenOptions";
import type { Theme } from "../theme";
import { useThemedStyles } from "../ThemeProvider";
import { space, type } from "../tokens";
import { Button, EmptyState } from "../ui";
import { useGlassBarInset } from "../useHeaderOverlap";
import { useCloud } from "./CloudProvider";
import { CloudSignIn } from "./components";
import { cloudErrorMessage } from "./errors";
import type { CloudSession } from "./types";
import { useCloudReviewerConversation } from "./useCloudReviewerConversation";

const newKey = () => Crypto.randomUUID();
const noop = async () => {};

/**
 * A Cloud review's conversation, opened from its pull request's page: the
 * review prompt, the reviewer's work and verdict, and a composer for
 * follow-up questions, laid out as a machine's reviewer chat is. Text only:
 * the control plane's review chat takes no files.
 */
export function CloudReviewerScreen(props: { reviewId: string; sessionId: string; title?: string }) {
	// One account and organization's chat: another starts the screen over, so
	// no refresh still out for the last one can fill it.
	const { account, org } = useCloud();
	return <CloudReviewerChat key={`${account?.id ?? ""}/${org?.id ?? ""}`} {...props} />;
}

function CloudReviewerChat({ reviewId, sessionId, title }: { reviewId: string; sessionId: string; title?: string }) {
	const styles = useThemedStyles(makeStyles);
	const navigation = useNavigation();
	const focused = useIsFocused();
	const { client, org, status, sessions } = useCloud();
	const listed = sessions.find((item) => item.id === sessionId);
	// An archived worker is not in the list; read it once.
	const [read, setRead] = useState<CloudSession>();
	// A failed read says why and can be asked again: without the session the
	// conversation has nothing to load.
	const [readError, setReadError] = useState<string>();
	const [readAttempt, setReadAttempt] = useState(0);
	useEffect(() => {
		if (listed || !org?.id || !sessionId) return;
		let current = true;
		setReadError(undefined);
		void client.getSession(org.id, sessionId).then(
			({ session }) => { if (current) setRead(session); },
			(cause: unknown) => { if (current) setReadError(cloudErrorMessage(cause)); },
		);
		return () => { current = false; };
	}, [client, listed, org?.id, readAttempt, sessionId]);
	const session = listed ?? read;
	const conversation = useCloudReviewerConversation({ client, orgId: org?.id, session, reviewRunId: reviewId, active: focused && status === "ready", newKey });
	const chatKeyboard = useChatKeyboard({ underHeader: SYSTEM_GLASS_HEADER });
	const glassInset = useGlassBarInset();
	const busy = useMemo(() => Boolean(conversation.snapshot?.turns.some((turn) => turn.state === "running")), [conversation.snapshot]);

	useLayoutEffect(() => navigation.setOptions({ title: title ? `Review · ${title}` : "Reviewer chat" }), [navigation, title]);

	if (status === "signed-out") return <CloudSignIn />;
	if (!session && readError) return <EmptyState icon="chat" title="Reviewer chat unavailable" message={readError} action={<Button title="Try again" icon="refresh" onPress={() => setReadAttempt((count) => count + 1)} />} />;
	if (conversation.loading && !conversation.snapshot) return <View style={styles.center}><Spinner size="large" accessibilityLabel="Loading reviewer chat" /></View>;
	if (!conversation.snapshot) return <EmptyState icon="chat" title="Reviewer chat unavailable" message={conversation.error || "The reviewer conversation has not started yet."} action={<Button title="Try again" icon="refresh" onPress={() => void conversation.refresh()} />} />;

	return <KeyboardAvoidingView style={[styles.screen, { paddingTop: glassInset }, chatKeyboard.rootStyle]} behavior={chatKeyboard.behavior} keyboardVerticalOffset={chatKeyboard.keyboardVerticalOffset}>
		{conversation.error ? <Text accessibilityRole="alert" style={styles.error}>{conversation.error}</Text> : null}
		<ChatTimeline snapshot={conversation.snapshot} loadingOlder={false} onLoadOlder={() => {}} approvalPending={false} inputPending={false} onDecide={noop} onResolveInput={noop} />
		<ReviewerComposer
			attachmentKey={`reviewer.cloud.${reviewId}`}
			attachmentsEnabled={false}
			busy={busy}
			stopped={Boolean(session?.isTerminated)}
			onSend={(text) => conversation.send(text)}
			onAcknowledgeSend={noop}
			onInterrupt={conversation.interrupt}
			restingInset={chatKeyboard.restingInset}
			dockRiseOverride={chatKeyboard.dockRiseOverride}
		/>
	</KeyboardAvoidingView>;
}

const makeStyles = (t: Theme) => StyleSheet.create({
	screen: { flex: 1, backgroundColor: t.bgBase },
	center: { flex: 1, alignItems: "center", justifyContent: "center", backgroundColor: t.bgBase },
	error: { ...type.footnote, color: t.red, paddingHorizontal: space.lg, paddingVertical: space.sm, backgroundColor: t.tintRed },
});
