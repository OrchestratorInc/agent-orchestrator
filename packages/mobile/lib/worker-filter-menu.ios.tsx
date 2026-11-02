import { Host } from "@expo/ui";
import { Image, Menu, Section } from "@expo/ui/swift-ui";
import { accessibilityIdentifier, accessibilityLabel, buttonStyle, contentShape, frame, menuOrder, shapes } from "@expo/ui/swift-ui/modifiers";
import { Stack } from "expo-router";
import { startTransition, type ReactElement } from "react";
import { glassCircle } from "./glass";
import { machineAppearance } from "./machineAppearance";
import { machineMenuImage } from "./machine-menu-image";
import { Choice } from "./model-controls.ios";
import { useTheme, useThemeState } from "./ThemeProvider";
import { iconSize, touchTarget } from "./tokens";
import {
	ALL_WORKER_ENVIRONMENTS,
	ALL_WORKER_PROJECTS,
	workerEnvironmentLabel,
	workerEnvironmentOptions,
	workerProjectLabel,
	workerProjectOptions,
} from "./worker-controls";
import type { WorkerFilterMenuOptions } from "./worker-filter-menu";

export type { WorkerFilterMenuOptions } from "./worker-filter-menu";

/**
 * The Workers filter on iOS: a SwiftUI menu in a 44pt glass circle, held in
 * the bar as a custom view. Not `Stack.Toolbar.Menu`: UIKit sizes a bar
 * button's frame under the touch target and offers no way to grow it. The
 * menus inside are the same as before, a Machine and a Project submenu each
 * with the system's check on the current choice.
 *
 * Still a function handing Stack.Toolbar its own item type; memoise the result.
 */
export function workerFilterMenu(options: WorkerFilterMenuOptions): ReactElement {
	return (
		<Stack.Toolbar.View hidesSharedBackground>
			<WorkerFilterButton {...options} />
		</Stack.Toolbar.View>
	);
}

function WorkerFilterButton({ environments, selectedEnvironmentId, onSelectEnvironment, projects, selectedProjectId, onSelectProject }: WorkerFilterMenuOptions) {
	const t = useTheme();
	const { scheme } = useThemeState();
	const environmentLabel = workerEnvironmentLabel(environments, selectedEnvironmentId);
	const projectLabel = workerProjectLabel(projects, selectedProjectId, selectedEnvironmentId);
	const activeFilters = [
		selectedEnvironmentId !== ALL_WORKER_ENVIRONMENTS ? environmentLabel : null,
		selectedProjectId !== ALL_WORKER_PROJECTS ? projectLabel : null,
	].filter((label) => label !== null);
	const filtered = activeFilters.length > 0;
	return (
		<Host style={{ width: touchTarget, height: touchTarget }} colorScheme={scheme}>
			<Menu
				// A filtered board shows it in the glyph, filled, not in colour alone.
				label={
					<Image
						systemName={filtered ? "line.3.horizontal.decrease.circle.fill" : "line.3.horizontal.decrease.circle"}
						size={iconSize.lg}
						color={t.textSecondary}
						modifiers={[frame({ width: touchTarget, height: touchTarget }), glassCircle(), contentShape(shapes.rectangle())]}
					/>
				}
				modifiers={[
					buttonStyle("plain"),
					menuOrder("fixed"),
					accessibilityLabel(filtered ? `Filter workers, filtered to ${activeFilters.join(", ")}` : "Filter workers"),
					accessibilityIdentifier("workers-filter"),
				]}
			>
				{/* Each submenu is titled with its current choice, under a section
				    saying what it filters by. */}
				<Section title="Machine">
					<Menu label={environmentLabel} systemImage="tray.2">
						{workerEnvironmentOptions(environments).map((environment) => (
							<Choice
								key={environment.id}
								label={environment.label}
								systemImage={machineMenuImage(environment.id === ALL_WORKER_ENVIRONMENTS ? "server" : machineAppearance({ id: environment.id, name: environment.label, appearance: environment.appearance }).symbol)}
								selected={environment.id === selectedEnvironmentId}
								// Preparing a replacement board must not block the menu closing.
								onChoose={() => startTransition(() => onSelectEnvironment(environment.id))}
							/>
						))}
					</Menu>
				</Section>
				<Section title="Project">
					<Menu label={projectLabel} systemImage="folder">
						{workerProjectOptions(projects, selectedEnvironmentId).map((project) => (
							<Choice
								key={project.id}
								label={project.label}
								systemImage="folder"
								selected={project.id === selectedProjectId}
								onChoose={() => startTransition(() => onSelectProject(project.id))}
							/>
						))}
					</Menu>
				</Section>
			</Menu>
		</Host>
	);
}
