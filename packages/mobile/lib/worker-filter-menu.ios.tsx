import { Host } from "@expo/ui";
import { Image, Menu, Section } from "@expo/ui/swift-ui";
import { accessibilityIdentifier, accessibilityLabel, buttonStyle, contentShape, frame, menuOrder, shapes } from "@expo/ui/swift-ui/modifiers";
import { Stack } from "expo-router";
import { startTransition, type ReactElement } from "react";
import { Choice } from "./model-controls.ios";
import { useTheme, useThemeState } from "./ThemeProvider";
import { iconSize, touchTarget } from "./tokens";
import { ALL_WORKER_PROJECTS, workerProjectLabel, workerProjectOptions } from "./worker-controls";
import type { WorkerFilterMenuOptions } from "./worker-filter-menu";

export type { WorkerFilterMenuOptions } from "./worker-filter-menu";

/**
 * The Workers filter on iOS: a SwiftUI menu on the circled filter glyph with a
 * 44pt target, held in the bar as a custom view. No glass and no fill behind
 * it: DESIGN.md keeps glass out of the working UI. Not `Stack.Toolbar.Menu`: UIKit sizes a bar
 * button's frame under the touch target and offers no way to grow it. Inside
 * are the projects, with the system's check on the current choice. Machines are
 * not in it: the board's row of machine chips is the one place to choose one.
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

function WorkerFilterButton({ selectedEnvironmentId, projects, selectedProjectId, onSelectProject }: WorkerFilterMenuOptions) {
	const t = useTheme();
	const { scheme } = useThemeState();
	const projectLabel = workerProjectLabel(projects, selectedProjectId, selectedEnvironmentId);
	const filtered = selectedProjectId !== ALL_WORKER_PROJECTS;
	return (
		<Host style={{ width: touchTarget, height: touchTarget }} colorScheme={scheme}>
			<Menu
				// In the ink and size of the bar's own buttons (the bell, the gear),
				// nothing behind it. A filtered board fills the circle, so the state
				// shows in its shape, not in colour alone.
				label={
					<Image
						systemName={filtered ? "line.3.horizontal.decrease.circle.fill" : "line.3.horizontal.decrease.circle"}
						size={iconSize.xl}
						color={t.textPrimary}
						modifiers={[frame({ width: touchTarget, height: touchTarget }), contentShape(shapes.rectangle())]}
					/>
				}
				modifiers={[
					buttonStyle("plain"),
					menuOrder("fixed"),
					accessibilityLabel(filtered ? `Filter workers, filtered to ${projectLabel}` : "Filter workers"),
					accessibilityIdentifier("workers-filter"),
				]}
			>
				{/* The projects themselves, under a title saying what they filter. */}
				<Section title="Project">
					{workerProjectOptions(projects, selectedEnvironmentId).map((project) => (
						<Choice
							key={project.id}
							label={project.label}
							systemImage="folder"
							selected={project.id === selectedProjectId}
							// Preparing a replacement board must not block the menu closing.
							onChoose={() => startTransition(() => onSelectProject(project.id))}
						/>
					))}
				</Section>
			</Menu>
		</Host>
	);
}
