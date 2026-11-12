import { useLocalSearchParams, useRouter } from "expo-router";
import { useCallback, useMemo, useState, type ReactNode } from "react";
import { StyleSheet, View } from "react-native";
import { backOr } from "../../lib/backNavigation";
import { CloudSignIn } from "../../lib/cloud/components";
import { useCloud } from "../../lib/cloud/CloudProvider";
import { CLOUD_HOST_ID } from "../../lib/cloud/mapping";
import { NewProjectBar, NewProjectForm } from "../../lib/cloud/NewProjectForm";
import { useCanCreateCloudProject } from "../../lib/cloud/useCanCreateCloudProject";
import { useNewCloudProject } from "../../lib/cloud/useNewCloudProject";
import { connectedSpawnHosts } from "../../lib/hostRoute";
import { LocalProjectForm } from "../../lib/localProject/LocalProjectForm";
import type { PlacementChoice, ProjectPlacement } from "../../lib/localProject/LocalProjectForm.types";
import { useNewLocalProject } from "../../lib/localProject/useNewLocalProject";
import { Spinner } from "../../lib/Spinner";
import { useApp } from "../../lib/store";
import { useTheme } from "../../lib/ThemeProvider";
import { Button, EmptyState } from "../../lib/ui";

export { SheetErrorBoundary as ErrorBoundary } from "../../lib/RouteErrorBoundary";

/**
 * A new project: on one of the paired machines, from a folder on it or a
 * repository cloned into it (see useNewLocalProject), or in Orchestrator
 * Cloud, from a repository the Orchestrator GitHub App can reach or with no
 * repository at all (see useNewCloudProject). A Local and Cloud switch heads
 * the form when both can make one; `placement` in the route picks the first
 * face. On iOS a form sheet, a SwiftUI Form under the native bar, which holds
 * Cancel and Create; on Android a page, Material fields under the top app bar,
 * which holds back and Create.
 */
export default function NewProjectScreen() {
	const router = useRouter();
	const t = useTheme();
	const params = useLocalSearchParams<{ placement?: string }>();
	const { hostStates } = useApp();
	const localOffered = useMemo(() => connectedSpawnHosts(hostStates).length > 0, [hostStates]);
	const cloudOffered = useCanCreateCloudProject();
	const [chosen, setChosen] = useState<ProjectPlacement | null>(params.placement === "cloud" || params.placement === "local" ? params.placement : null);
	// The face asked for while it can make a project; Local first otherwise,
	// and Cloud when no machine is connected, which says what it needs.
	const placement: ProjectPlacement = chosen === "cloud" && cloudOffered ? "cloud"
		: chosen === "local" && localOffered ? "local"
		: localOffered ? "local" : "cloud";
	const choice: PlacementChoice | undefined = localOffered && cloudOffered ? { value: placement, onChange: setChosen } : undefined;

	// Leave the form first, then open the page, as the conversation's actions
	// sheet does: a page pushed from a sheet lands in the stack under it, out of
	// sight, and on Android back from the project should not return to a form
	// already used. The navigator applies the two in order.
	const leaveFor = useCallback((open: () => void) => {
		backOr(router, "/projects");
		open();
	}, [router]);
	const openProject = useCallback((hostId: string, id: string) => {
		leaveFor(() => router.push({ pathname: "/project/[id]", params: { id, hostId } }));
	}, [leaveFor, router]);
	const cancel = useCallback(() => backOr(router, "/projects"), [router]);

	return (
		<View style={[styles.screen, { backgroundColor: t.bgBase }]}>
			{placement === "local"
				? <LocalBody placement={choice} onCreated={openProject} onCancel={cancel} />
				: <CloudBody placement={choice} onCreated={openProject} onCancel={cancel} leaveFor={leaveFor} />}
		</View>
	);
}

type BodyProps = {
	placement?: PlacementChoice;
	onCreated(hostId: string, projectId: string): void;
	onCancel(): void;
};

function LocalBody({ placement, onCreated, onCancel }: BodyProps) {
	const project = useNewLocalProject({ onCreated });
	return (
		<>
			<NewProjectBar canCreate={project.canCreate} busy={project.busy} onCancel={onCancel} onCreate={() => void project.create()} />
			<LocalProjectForm project={project} placement={placement} />
		</>
	);
}

function CloudBody({ placement, onCreated, onCancel, leaveFor }: BodyProps & { leaveFor(open: () => void): void }) {
	const router = useRouter();
	const cloud = useCloud();
	const openProject = useCallback((id: string) => onCreated(CLOUD_HOST_ID, id), [onCreated]);
	// Coding agents are connected in Orchestrator Cloud's settings.
	const connectAgent = useCallback(() => leaveFor(() => router.push("/settings/cloud")), [leaveFor, router]);
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
		body = <NewProjectForm project={project} onConnectAgent={connectAgent} placement={placement} />;
	}

	return (
		<>
			<NewProjectBar canCreate={project.canCreate} busy={project.busy} onCancel={onCancel} onCreate={() => void project.create()} />
			{body}
		</>
	);
}

const styles = StyleSheet.create({
	screen: { flex: 1 },
	center: { flex: 1, alignItems: "center", justifyContent: "center" },
});
