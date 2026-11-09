import { Stack } from "expo-router";
import { startTransition, type ReactElement } from "react";
import { Platform, type ColorValue } from "react-native";
import { haptics } from "./haptics";
import { toolbarIcon } from "./toolbarIcon";
import { ALL_WORKER_PROJECTS, workerProjectLabel, workerProjectOptions } from "./worker-controls";

export type WorkerFilterMenuOptions = {
	/** The machine the board's chips have chosen, which the project choices follow. */
	selectedEnvironmentId: string;
	projects: readonly { id: string; name: string; hostId?: string; hostName?: string }[];
	selectedProjectId: string;
	onSelectProject: (projectId: string) => void;
	/** The Android dot that marks a filtered board. */
	badgeColor: ColorValue;
};

/**
 * The Workers filter as one native header menu: the projects, with the system's
 * own check on the current choice. Machines are not in it: the board's row of
 * machine chips is the one place to choose a machine. The fallback for other
 * platforms and the shared tests: iOS draws its own in a 44pt circle
 * (./worker-filter-menu.ios), Android a Compose dropdown
 * (./worker-filter-menu.android).
 *
 * A function rather than a component: Stack.Toolbar reads its children by
 * element type, so what it is handed has to be the Stack.Toolbar.Menu itself.
 * Memoise the result; every new element registers the header's options again,
 * and a rebuild while the menu is open can close it under the finger.
 */
export function workerFilterMenu({
	selectedEnvironmentId,
	projects,
	selectedProjectId,
	onSelectProject,
	badgeColor,
}: WorkerFilterMenuOptions): ReactElement {
	const projectLabel = workerProjectLabel(projects, selectedProjectId, selectedEnvironmentId);
	const filtered = selectedProjectId !== ALL_WORKER_PROJECTS;
	const ios = Platform.OS === "ios";
	return (
		<Stack.Toolbar.Menu
			// A filtered board shows it in the glyph, not in colour alone: iOS fills
			// the symbol, Android badges it with Material's dot.
			icon={toolbarIcon(filtered ? { ios: "line.3.horizontal.decrease.circle.fill", android: "filter_list" } : "filter")}
			accessibilityLabel={filtered ? `Filter workers, filtered to ${projectLabel}` : "Filter workers"}
		>
			{filtered && !ios ? <Stack.Toolbar.Badge style={{ backgroundColor: badgeColor }} /> : null}
			<Stack.Toolbar.Menu inline title="Project">
				{workerProjectOptions(projects, selectedEnvironmentId).map((project) => (
					<Stack.Toolbar.MenuAction
						key={project.id}
						icon={ios ? "folder" : undefined}
						isOn={project.id === selectedProjectId}
						onPress={() => {
							haptics.select();
							startTransition(() => onSelectProject(project.id));
						}}
					>
						{project.label}
					</Stack.Toolbar.MenuAction>
				))}
			</Stack.Toolbar.Menu>
		</Stack.Toolbar.Menu>
	);
}
