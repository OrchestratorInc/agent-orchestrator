import { useLocalSearchParams, useNavigation, useRouter } from "expo-router";
import { useEffect } from "react";
import { StyleSheet, Text, View } from "react-native";
import { backOr } from "../../../lib/backNavigation";
import { HostScope, useApp } from "../../../lib/store";
import type { Theme } from "../../../lib/theme";
import { useThemedStyles } from "../../../lib/ThemeProvider";
import { space, type } from "../../../lib/tokens";
import { Button, SheetHeader } from "../../../lib/ui";
import { plural } from "../../../lib/files/model";
import { QueuedComments } from "../../../lib/files/QueuedComments";
import { reviewKey } from "../../../lib/files/reviewStore";
import { useReviewSender } from "../../../lib/files/useReviewSender";

export { SheetErrorBoundary as ErrorBoundary } from "../../../lib/RouteErrorBoundary";

/**
 * The comments queued against a session's files, in a sheet over the page
 * that opened it: remove any before they go, then send them as one message.
 */
export default function ReviewCommentsSheet() {
	const { hostId } = useLocalSearchParams<{ hostId?: string }>();
	return hostId ? <HostScope key={hostId} hostId={hostId}><ReviewCommentsContent /></HostScope> : <ReviewCommentsContent />;
}

function ReviewCommentsContent() {
	const { sessionId } = useLocalSearchParams<{ sessionId: string }>();
	const id = String(sessionId ?? "");
	const router = useRouter();
	const navigation = useNavigation();
	const { config, currentHostId } = useApp();
	const styles = useThemedStyles(makeStyles);
	const review = useReviewSender(config, reviewKey(currentHostId, id), id);
	const count = review.comments.length;

	// Nothing left to send, whether it all went or the last one was removed:
	// the sheet has done its job.
	useEffect(() => {
		if (count || !navigation.isFocused()) return;
		backOr(router);
	}, [count, navigation, router]);

	// A review on its way cannot be called back, so the sheet stays until it lands.
	useEffect(() => {
		if (!review.sending) return;
		navigation.setOptions({ gestureEnabled: false });
		return () => navigation.setOptions({ gestureEnabled: true });
	}, [navigation, review.sending]);

	return (
		<View style={styles.sheet}>
			<View style={styles.header}>
				<SheetHeader title="Review comments" subtitle="Sent to the agent as one message, with the file and line for each." />
			</View>
			<QueuedComments comments={review.comments} onRemove={review.remove} disabled={review.sending} />
			<View style={styles.footer}>
				{review.error ? <Text accessibilityRole="alert" style={styles.error}>{review.error}</Text> : null}
				<Button title={`Send ${plural(count, "comment")}`} icon="send" loading={review.sending} disabled={!count} onPress={() => void review.send()} />
			</View>
		</View>
	);
}

const makeStyles = (t: Theme) => StyleSheet.create({
	sheet: { flex: 1, backgroundColor: t.bgSurface },
	header: { paddingHorizontal: space.xl, paddingTop: space.xl, paddingBottom: space.md },
	footer: { gap: space.sm, paddingHorizontal: space.xl, paddingTop: space.md, paddingBottom: space.xl },
	error: { ...type.footnote, color: t.red },
});
