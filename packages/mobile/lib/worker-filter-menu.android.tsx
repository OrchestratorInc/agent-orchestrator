import { Badge, BadgedBox, DropdownMenu, DropdownMenuItem, Icon, IconButton, Text } from "@expo/ui/jetpack-compose";
import { testID } from "@expo/ui/jetpack-compose/modifiers";
import { startTransition, useState, type ReactElement } from "react";
import type { ImageSourcePropType } from "react-native";
import { haptics } from "./haptics";
import { useTheme } from "./ThemeProvider";
import { materialDrawable } from "./toolbarIcon.android";
import { font, iconSize } from "./tokens";
import { ALL_WORKER_PROJECTS, workerProjectLabel, workerProjectOptions } from "./worker-controls";
import type { WorkerFilterMenuOptions } from "./worker-filter-menu";

export type { WorkerFilterMenuOptions } from "./worker-filter-menu";

/**
 * The Workers filter on Android: Compose's dropdown, drawn as the new task's
 * selectors are, listing the projects. Machines are not in it: the board's row
 * of machine chips is the one place to choose one. Not `Stack.Toolbar.Menu`: that paints its menus in the
 * header's colour, and the board's header is clear, so the menu and its
 * submenu came up see-through, one over the other.
 *
 * The header's toolbar lays its items out in a Compose row on Android, so this
 * is a Compose component handed to it in place of the toolbar menu.
 */
export function workerFilterMenu(options: WorkerFilterMenuOptions): ReactElement {
	return <WorkerFilterMenu {...options} />;
}

function WorkerFilterMenu({
	selectedEnvironmentId,
	projects,
	selectedProjectId,
	onSelectProject,
	badgeColor,
}: WorkerFilterMenuOptions) {
	const t = useTheme();
	const [open, setOpen] = useState(false);
	const close = () => setOpen(false);
	const projectLabel = workerProjectLabel(projects, selectedProjectId, selectedEnvironmentId);
	const filtered = selectedProjectId !== ALL_WORKER_PROJECTS;
	// Only a real change is worth a tick. Preparing a replacement board must not
	// hold up the menu closing.
	const choose = (current: string, apply: (id: string) => void) => (id: string) => {
		close();
		if (id === current) return;
		haptics.select();
		startTransition(() => apply(id));
	};
	const glyph = (
		<Icon
			source={materialDrawable("filter_list")}
			size={iconSize.xl}
			tint={t.textSecondary}
			contentDescription={filtered ? `Filter workers, filtered to ${projectLabel}` : "Filter workers"}
		/>
	);

	return (
		<DropdownMenu expanded={open} onDismissRequest={close} color={t.bgElevated}>
			<DropdownMenu.Trigger>
				<IconButton onClick={() => setOpen(true)} modifiers={[testID("worker-filter")]}>
					{/* A filtered board shows it in the glyph, with Material's dot. */}
					{filtered ? (
						<BadgedBox>
							{glyph}
							<BadgedBox.Badge>
								<Badge containerColor={badgeColor as string} />
							</BadgedBox.Badge>
						</BadgedBox>
					) : glyph}
				</IconButton>
			</DropdownMenu.Trigger>
			<DropdownMenu.Items>
				{workerProjectOptions(projects, selectedEnvironmentId).map((project) => (
					<MenuRow
						key={project.id}
						label={project.label}
						icon={materialDrawable("folder")}
						selected={project.id === selectedProjectId}
						onClick={() => choose(selectedProjectId, onSelectProject)(project.id)}
					/>
				))}
			</DropdownMenu.Items>
		</DropdownMenu>
	);
}

function MenuRow({ label, icon, selected, onClick }: { label: string; icon?: ImageSourcePropType; selected: boolean; onClick: () => void }) {
	const t = useTheme();
	return (
		<DropdownMenuItem onClick={onClick} elementColors={{ textColor: t.textPrimary }}>
			<DropdownMenuItem.Text>
				<Text maxLines={2} overflow="ellipsis" style={{ typography: "bodyLarge", fontFamily: font.regular }}>{label}</Text>
			</DropdownMenuItem.Text>
			{icon ? (
				<DropdownMenuItem.LeadingIcon>
					<Icon source={icon} size={iconSize.xl} tint={t.textSecondary} />
				</DropdownMenuItem.LeadingIcon>
			) : null}
			{selected ? (
				<DropdownMenuItem.TrailingIcon>
					<Icon source={materialDrawable("check")} size={iconSize.xl} tint={t.accent} contentDescription="Selected" />
				</DropdownMenuItem.TrailingIcon>
			) : null}
		</DropdownMenuItem>
	);
}
