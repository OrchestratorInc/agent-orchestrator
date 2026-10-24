import { useRouter } from "expo-router";
import { StyleSheet, Text, View } from "react-native";
import { CloudAgentConnections } from "../../lib/cloud/CloudAgentConnections";
import { useCloudAuth } from "../../lib/cloud/authStore";
import { useThemedStyles } from "../../lib/ThemeProvider";
import type { Theme } from "../../lib/theme";
import { space, type } from "../../lib/tokens";
import { Button } from "../../lib/ui";

export { RouteErrorBoundary as ErrorBoundary } from "../../lib/RouteErrorBoundary";

export default function CloudAgentsScreen() {
	const auth = useCloudAuth();
	const styles = useThemedStyles(makeStyles);
	const router = useRouter();
	const ownerKey = JSON.stringify([auth.baseUrl.replace(/\/+$/, ""), auth.accountId, auth.sessionEpoch]);
	return <View style={styles.screen}>{auth.signedIn && auth.accountId ? <CloudAgentConnections
		key={ownerKey} client={auth.client} orgId={auth.orgId} ownerKey={ownerKey} isCurrent={auth.isCurrentSession}
	/> : <View style={styles.signIn}><Text style={styles.description}>Sign in to connect your Cloud coding agents.</Text><Button title="Sign in" onPress={() => router.push("/sheets/cloud-signin")} /></View>}</View>;
}

const makeStyles = (theme: Theme) => StyleSheet.create({
	screen: { flex: 1, backgroundColor: theme.bgBase, paddingHorizontal: space.lg, paddingTop: space.md },
	signIn: { gap: space.md },
	description: { ...type.body, color: theme.textSecondary },
});
