import { DropdownMenu, DropdownMenuItem, Host, Icon, OutlinedButton, Text } from "@expo/ui/jetpack-compose";
import { padding, testID } from "@expo/ui/jetpack-compose/modifiers";
import { startTransition, useState } from "react";
import type { EnvironmentPickerProps } from "./environment-picker.types";
import { haptics } from "./haptics";
import { machineMenuImage } from "./machine-menu-image.android";
import { machineAppearance } from "./machineAppearance";
import { useTheme, useThemeState } from "./ThemeProvider";
import { materialDrawable } from "./toolbarIcon.android";
import { space } from "./tokens";
import { ALL_WORKER_ENVIRONMENTS, workerEnvironmentOptions } from "./worker-controls";

export type { EnvironmentPickerProps } from "./environment-picker.types";

/** Material's glyph size inside a button. */
const BUTTON_ICON_SIZE = 18;
/** Material's glyph size in a menu item. */
const MENU_ICON_SIZE = 24;

/**
 * Moves the Workers board to another machine: Material 3's outlined button,
 * opening a dropdown of every environment, each with its machine's symbol and a
 * check on the current one.
 * Compose gives the button its 48dp target and ripple, and the menu its
 * placement, motion and dismissal.
 */
export function EnvironmentPicker({ environments, selectedEnvironmentId, onSelectEnvironment }: EnvironmentPickerProps) {
	const t = useTheme();
	const { scheme } = useThemeState();
	const [open, setOpen] = useState(false);
	const select = (environmentId: string) => {
		setOpen(false);
		if (environmentId === selectedEnvironmentId) return;
		haptics.select();
		// Preparing a replacement board must not block the menu's dismissal.
		startTransition(() => onSelectEnvironment(environmentId));
	};
	return (
		<Host matchContents colorScheme={scheme}>
			<DropdownMenu expanded={open} onDismissRequest={() => setOpen(false)} color={t.bgElevated}>
				<DropdownMenu.Trigger>
					<OutlinedButton onClick={() => setOpen(true)} colors={{ contentColor: t.accent }} modifiers={[testID("environment-picker")]}>
						<Icon source={materialDrawable("devices")} size={BUTTON_ICON_SIZE} tint={t.accent} modifiers={[padding(0, 0, space.sm, 0)]} />
						<Text style={{ typography: "labelLarge", fontFamily: "DMSans_500Medium" }}>Switch machine</Text>
					</OutlinedButton>
				</DropdownMenu.Trigger>
				<DropdownMenu.Items>
					{workerEnvironmentOptions(environments).map((option) => {
						const selected = option.id === selectedEnvironmentId;
						return (
							<DropdownMenuItem
								key={option.id}
								onClick={() => select(option.id)}
								elementColors={{ textColor: t.textPrimary, leadingIconColor: t.textSecondary, trailingIconColor: t.accent }}
								modifiers={[testID(`environment-picker-${option.id}`)]}
							>
								<DropdownMenuItem.LeadingIcon>
									<Icon source={machineMenuImage(option.id === ALL_WORKER_ENVIRONMENTS ? "server" : machineAppearance({ id: option.id, name: option.label, appearance: option.appearance }).symbol)} size={MENU_ICON_SIZE} />
								</DropdownMenuItem.LeadingIcon>
								<DropdownMenuItem.Text>
									<Text maxLines={1} overflow="ellipsis" style={{ typography: "bodyLarge", fontFamily: "DMSans_400Regular" }}>
										{option.label}
									</Text>
								</DropdownMenuItem.Text>
								{/* The check, named, so TalkBack says which one is current. */}
								{selected ? (
									<DropdownMenuItem.TrailingIcon>
										<Icon source={materialDrawable("check")} size={MENU_ICON_SIZE} contentDescription="Current" />
									</DropdownMenuItem.TrailingIcon>
								) : null}
							</DropdownMenuItem>
						);
					})}
				</DropdownMenu.Items>
			</DropdownMenu>
		</Host>
	);
}
