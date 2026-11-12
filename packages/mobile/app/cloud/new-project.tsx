import { useRouter } from "expo-router";
import { useCallback, type ReactNode } from "react";
import { StyleSheet, View } from "react-native";
import { backOr } from "../../lib/backNavigation";
import { CloudSignIn } from "../../lib/cloud/components";
import { useCloud } from "../../lib/cloud/CloudProvider";
import { CLOUD_HOST_ID } from "../../lib/cloud/mapping";
import { NewProjectBar, NewProjectForm } from "../../lib/cloud/NewProjectForm";
import { useNewCloudProject } from "../../lib/cloud/useNewCloudProject";
import { Spinner } from "../../lib/Spinner";
import { useTheme } from "../../lib/ThemeProvider";
import { Button, EmptyState } from "../../lib/ui";

export { SheetErrorBoundary as ErrorBoundary } from "../../lib/RouteErrorBoundary";

/**
 * A new Orchestrator Cloud project, from a repository the Orchestrator GitHub
 * App can reach or with no repository at all (see useNewCloudProject). On iOS
 * a form sheet, a SwiftUI Form under the native bar, which holds Cancel and
 * Create; on Android a page, Material fields under the top app bar, which
 * holds back and Create.
 */
export default function CloudNewProjectScreen() {
	const router = useRouter();
	const t = useTheme();
	const cloud = useCloud();

	// Leave the form first, then open the page, as the conversation's actions
	// sheet does: a page pushed from a sheet lands in the stack under it, out of
	// sight, and on Android back from the project should not return to a form
	// already used. The navigator applies the two in order.
	const leaveFor = useCallback((open: () => void) => {
		backOr(router, "/projects");
		open();
	}, [router]);
	const openProject = useCallback((id: string) => {
		leaveFor(() => router.push({ pathname: "/project/[id]", params: { id, hostId: CLOUD_HOST_ID } }));
	}, [leaveFor, router]);
	// Coding agents are connected in Orchestrator Cloud's settings.
	const connectAgent = useCallback(() => leaveFor(() => router.push("/settings/cloud")), [leaveFor, router]);
	const cancel = useCallback(() => backOr(router, "/projects"), [router]);

	const project = useNewCloudProject({ onCreated: openProject });

	let body: ReactNode;
	if (project.status === "signed-out") {
		body = <CloudSignIn />;
	} else if (!project.org && cloud.status === "error") {
		body = (
			<EmptyState
				icon="warning"
				title="Couldn't reach Orchestrator Cloud"
				message={cloud.error ?? undefined}
				action={<Button title="Try again" icon="refresh" onPress={() => void cloud.refresh()} />}
			/>
		);
	} else if (!project.org) {
		body = <View style={styles.center}><Spinner size="large" accessibilityLabel="Loading Orchestrator Cloud" /></View>;
	} else {
		body = <NewProjectForm project={project} onConnectAgent={connectAgent} />;
	}

	return (
		<View style={[styles.screen, { backgroundColor: t.bgBase }]}>
			<NewProjectBar
				canCreate={project.canCreate}
				busy={project.busy}
				onCancel={cancel}
				onCreate={() => void project.create()}
			/>
			{body}
		</View>
	);
}

const styles = StyleSheet.create({
	screen: { flex: 1 },
	center: { flex: 1, alignItems: "center", justifyContent: "center" },
});
