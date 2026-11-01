import { Badge, BadgedBox, DropdownMenu, DropdownMenuItem, Icon, IconButton, Text } from "@expo/ui/jetpack-compose";
import { testID } from "@expo/ui/jetpack-compose/modifiers";
import { startTransition, useState, type ReactElement, type ReactNode } from "react";
import type { ImageSourcePropType } from "react-native";
import { haptics } from "./haptics";
import { machineAppearance } from "./machineAppearance";
import { machineMenuImage } from "./machine-menu-image.android";
import { useTheme } from "./ThemeProvider";
import { materialDrawable } from "./toolbarIcon.android";
import { font, iconSize } from "./tokens";
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

type Submenu = "environment" | "project";

/**
 * The Workers filter on Android: Compose's dropdown, drawn as the new task's
 * selectors are, with an Environment and a Project row each opening one level
 * down onto its choices. Not `Stack.Toolbar.Menu`: that paints its menus in the
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
	environments,
	selectedEnvironmentId,
	onSelectEnvironment,
	projects,
	selectedProjectId,
	onSelectProject,
	badgeColor,
}: WorkerFilterMenuOptions) {
	const t = useTheme();
	const [open, setOpen] = useState(false);
	const [submenu, setSubmenu] = useState<Submenu | null>(null);
	const close = () => { setOpen(false); setSubmenu(null); };
	const environmentLabel = workerEnvironmentLabel(environments, selectedEnvironmentId);
	const projectLabel = workerProjectLabel(projects, selectedProjectId, selectedEnvironmentId);
	const activeFilters = [
		selectedEnvironmentId !== ALL_WORKER_ENVIRONMENTS ? environmentLabel : null,
		selectedProjectId !== ALL_WORKER_PROJECTS ? projectLabel : null,
	].filter((label) => label !== null);
	const filtered = activeFilters.length > 0;
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
			contentDescription={filtered ? `Filter workers, filtered to ${activeFilters.join(", ")}` : "Filter workers"}
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
				{/* Each submenu row is titled with its current choice, so TalkBack
				    learns the choice from the title. */}
				<SubmenuRow
					label={environmentLabel}
					icon={materialDrawable("devices")}
					expanded={submenu === "environment"}
					onExpand={() => setSubmenu("environment")}
					onDismiss={() => setSubmenu(null)}
					testTag="worker-filter-environment"
				>
					{workerEnvironmentOptions(environments).map((environment) => (
						<MenuRow
							key={environment.id}
							label={environment.label}
							icon={machineMenuImage(environment.id === ALL_WORKER_ENVIRONMENTS ? "server" : machineAppearance({ id: environment.id, name: environment.label, appearance: environment.appearance }).symbol)}
							selected={environment.id === selectedEnvironmentId}
							onClick={() => choose(selectedEnvironmentId, onSelectEnvironment)(environment.id)}
						/>
					))}
				</SubmenuRow>
				<SubmenuRow
					label={projectLabel}
					icon={materialDrawable("folder")}
					expanded={submenu === "project"}
					onExpand={() => setSubmenu("project")}
					onDismiss={() => setSubmenu(null)}
					testTag="worker-filter-project"
				>
					{workerProjectOptions(projects, selectedEnvironmentId).map((project) => (
						<MenuRow
							key={project.id}
							label={project.label}
							selected={project.id === selectedProjectId}
							onClick={() => choose(selectedProjectId, onSelectProject)(project.id)}
						/>
					))}
				</SubmenuRow>
			</DropdownMenu.Items>
		</DropdownMenu>
	);
}

/** A row that opens its choices one level down, as the new task's agents open onto their models. */
function SubmenuRow({ label, icon, expanded, onExpand, onDismiss, testTag, children }: {
	label: string;
	icon: ImageSourcePropType;
	expanded: boolean;
	onExpand: () => void;
	onDismiss: () => void;
	testTag: string;
	children: ReactNode;
}) {
	const t = useTheme();
	return (
		<DropdownMenu expanded={expanded} onDismissRequest={onDismiss} color={t.bgElevated}>
			<DropdownMenu.Trigger>
				<DropdownMenuItem onClick={onExpand} elementColors={{ textColor: t.textPrimary }} modifiers={[testID(testTag)]}>
					<DropdownMenuItem.Text>
						<Text maxLines={1} overflow="ellipsis" style={{ typography: "bodyLarge", fontFamily: font.regular }}>{label}</Text>
					</DropdownMenuItem.Text>
					<DropdownMenuItem.LeadingIcon>
						<Icon source={icon} size={iconSize.xl} tint={t.textSecondary} />
					</DropdownMenuItem.LeadingIcon>
					<DropdownMenuItem.TrailingIcon>
						<Icon source={materialDrawable("chevron_right")} size={iconSize.xl} tint={t.textTertiary} />
					</DropdownMenuItem.TrailingIcon>
				</DropdownMenuItem>
			</DropdownMenu.Trigger>
			<DropdownMenu.Items>{children}</DropdownMenu.Items>
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
