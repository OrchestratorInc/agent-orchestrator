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
	const filtered = selectedEnvironmentId !== ALL_WORKER_ENVIRONMENTS || selectedProjectId !== ALL_WORKER_PROJECTS;
	const ios = Platform.OS === "ios";
	return (
		<Stack.Toolbar.Menu
			// A filtered board shows it in the glyph, not in colour alone: iOS fills
			// the symbol, Android badges it with Material's dot.
			icon={toolbarIcon(filtered ? { ios: "line.3.horizontal.decrease.circle.fill", android: "filter_list" } : "filter")}
			accessibilityLabel={filtered ? "Filter workers, filters on" : "Filter workers"}
		>
			{filtered && !ios ? <Stack.Toolbar.Badge style={{ backgroundColor: badgeColor }} /> : null}
			<Stack.Toolbar.Menu title="Environment" icon={toolbarIcon({ ios: "tray.2", android: "devices" })}>
				{workerEnvironmentOptions(environments).map((environment) => (
					<Stack.Toolbar.MenuAction
						key={environment.id}
						// Android menus here take only bundled drawables, and the machine
						// glyphs have none; the check alone marks the choice there.
						icon={ios ? machineMenuImage(environment.id === ALL_WORKER_ENVIRONMENTS ? "server" : machineAppearance({ id: environment.id, name: environment.label, appearance: environment.appearance }).symbol) : undefined}
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
			{/* Titled with the selection on its own: naming the menu in front of it
			    pushed the row past the width the menu reserves, and it wrapped. */}
			<Stack.Toolbar.Menu title={workerProjectLabel(projects, selectedProjectId, selectedEnvironmentId)} icon={toolbarIcon("folder")}>
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
