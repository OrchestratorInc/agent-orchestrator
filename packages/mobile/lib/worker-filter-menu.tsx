import { Stack } from "expo-router";
import { startTransition, type ReactElement } from "react";
import { Platform, type ColorValue } from "react-native";
import { haptics } from "./haptics";
import { machineAppearance } from "./machineAppearance";
import { machineMenuImage } from "./machine-menu-image";
import { toolbarIcon } from "./toolbarIcon";
import {
	ALL_WORKER_ENVIRONMENTS,
	ALL_WORKER_PROJECTS,
	workerEnvironmentLabel,
	workerEnvironmentOptions,
	workerProjectLabel,
	workerProjectOptions,
	type WorkerEnvironment,
} from "./worker-controls";

export type WorkerFilterMenuOptions = {
	environments: readonly WorkerEnvironment[];
	selectedEnvironmentId: string;
	onSelectEnvironment: (environmentId: string) => void;
	projects: readonly { id: string; name: string; hostId?: string; hostName?: string }[];
	selectedProjectId: string;
	onSelectProject: (projectId: string) => void;
	/** The Android dot that marks a filtered board. */
	badgeColor: ColorValue;
};

/**
 * The Workers filter as one native header menu: an Environment submenu and a
 * Project submenu, each with the system's own check on the current choice. A
 * UIMenu on iOS, Compose's DropdownMenu on Android.
 *
 * A function rather than a component: Stack.Toolbar reads its children by
 * element type, so what it is handed has to be the Stack.Toolbar.Menu itself.
 * Memoise the result; every new element registers the header's options again,
 * and a rebuild while the menu is open can close it under the finger.
 */
export function workerFilterMenu({
	environments,
	selectedEnvironmentId,
	onSelectEnvironment,
	projects,
	selectedProjectId,
	onSelectProject,
	badgeColor,
}: WorkerFilterMenuOptions): ReactElement {
	const environmentLabel = workerEnvironmentLabel(environments, selectedEnvironmentId);
	const projectLabel = workerProjectLabel(projects, selectedProjectId, selectedEnvironmentId);
	const activeFilters = [
		selectedEnvironmentId !== ALL_WORKER_ENVIRONMENTS ? environmentLabel : null,
		selectedProjectId !== ALL_WORKER_PROJECTS ? projectLabel : null,
	].filter((label) => label !== null);
	const filtered = activeFilters.length > 0;
	const ios = Platform.OS === "ios";
	return (
		<Stack.Toolbar.Menu
			// A filtered board shows it in the glyph, not in colour alone: iOS fills
			// the symbol, Android badges it with Material's dot. The label names the
			// filters, since with one machine paired no chip row says it either.
			icon={toolbarIcon(filtered ? { ios: "line.3.horizontal.decrease.circle.fill", android: "filter_list" } : "filter")}
			accessibilityLabel={filtered ? `Filter workers, filtered to ${activeFilters.join(", ")}` : "Filter workers"}
		>
			{filtered && !ios ? <Stack.Toolbar.Badge style={{ backgroundColor: badgeColor }} /> : null}
			{/* Each submenu is titled with its current choice. Android's menu items
			    draw the check without a selected state, so TalkBack learns the
			    choice from the title. Naming the menu in front of it pushed the row
			    past the width the menu reserves, and it wrapped, so what each one
			    filters by heads it as an inline section's title instead: a row
			    reading "All machines" alone did not say it was a choice of machine. */}
			<Stack.Toolbar.Menu inline title="Machine">
				<Stack.Toolbar.Menu title={environmentLabel} icon={toolbarIcon({ ios: "tray.2", android: "devices" })}>
					{workerEnvironmentOptions(environments).map((environment) => (
						<Stack.Toolbar.MenuAction
							key={environment.id}
							// Each machine's own glyph: an SF Symbol on iOS, its bundled
							// Material drawable on Android, which takes only image sources.
							icon={machineMenuImage(environment.id === ALL_WORKER_ENVIRONMENTS ? "server" : machineAppearance({ id: environment.id, name: environment.label, appearance: environment.appearance }).symbol)}
							isOn={environment.id === selectedEnvironmentId}
							onPress={() => {
								haptics.select();
								// Preparing a replacement board must not block native menu dismissal.
								startTransition(() => onSelectEnvironment(environment.id));
							}}
						>
							{environment.label}
						</Stack.Toolbar.MenuAction>
					))}
				</Stack.Toolbar.Menu>
			</Stack.Toolbar.Menu>
			<Stack.Toolbar.Menu inline title="Project">
				<Stack.Toolbar.Menu title={projectLabel} icon={toolbarIcon("folder")}>
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
		</Stack.Toolbar.Menu>
	);
}
