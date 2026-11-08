import { Button, DropdownMenu, Host, HorizontalDivider, Icon, IconButton, RNHostView, Row, Spacer, Text } from "@expo/ui/jetpack-compose";
import { fillMaxWidth, height, padding, semantics, testID, weight } from "@expo/ui/jetpack-compose/modifiers";
import { useState } from "react";
import { HarnessMark, KEEP_KEYBOARD, MenuRow, useCloseMenuOnBack } from "../composeMenu.android";
import { EffortGaugeButton } from "../effort-gauge";
import { haptics } from "../haptics";
import { useTheme, useThemeState } from "../ThemeProvider";
import { materialDrawable } from "../toolbarIcon.android";
import { font, iconSize, space, touchTarget } from "../tokens";
import { COMPOSER_DISC } from "./composerStyles";
import type { ChatTurnSettingsControlProps } from "./ChatTurnSettingsControl.types";
import { splitModelChoices, turnSettingsParts, turnSettingsRows, turnSettingTopic, turnSettingWrite, type TurnSettingRow } from "./turnSettingsModel";

/** A setting's submenu is open by its row's id. */
type OpenMenu = "model" | "older" | "approval" | `setting:${string}`;

// The menus' width: wider than their longest label, so a row is an easy target
// and the menu reads as a panel rather than a strip, and under Material's 280.
const MENU_WIDTH = 240;

/**
 * The chat's controls row on Android, built as the new task's: the model in a
 * filled capsule led by its agent's mark, the effort as a speedometer beside
 * it that opens the composer's slider, and the approval mode as its shield at
 * the row's end. The model and the approval mode open the same Material 3
 * dropdown as the new task's, with the current choice ticked. The model's lists
 * models, the newest of each family first and older versions one level down,
 * then the provider's other settings, such as Fast mode or its agent, as iOS's
 * does: an on/off setting ticked while on, any other opening onto its choices.
 */
export function ChatTurnSettingsControl({ snapshot, models, options, disabled, onSettings, onOption, effort }: ChatTurnSettingsControlProps) {
	const t = useTheme();
	const { scheme } = useThemeState();
	const [openMenu, setOpenMenu] = useState<OpenMenu | null>(null);
	const close = () => setOpenMenu(null);
	useCloseMenuOnBack(Boolean(openMenu), close);
	const rows = turnSettingsRows(snapshot, models, options);
	const select = (topic: string) => rows.find((row) => row.kind === "select" && turnSettingTopic(row) === topic);
	const modelRow = select("model");
	const effortRow = select("effort");
	const approvalRow = select("permissions");
	// The effort has its slider; everything else the provider offers is listed
	// under the models, or Android would have no way to reach it.
	const otherRows = rows.filter((row) => row !== modelRow && row !== effortRow && row !== approvalRow);
	const modelChoices = splitModelChoices(modelRow?.choices ?? []);
	const parts = turnSettingsParts(snapshot, models, options);
	const off = Boolean(disabled);

	// Only a real change is worth a tick and a write.
	const choose = (row: TurnSettingRow, value: string) => {
		close();
		if (row.choices.find((choice) => choice.value === value)?.selected) return;
		haptics.select();
		const write = turnSettingWrite(snapshot.settings, row, value);
		if (write.kind === "option") void onOption(write.id, write.value).catch(() => {});
		else void onSettings(write.settings).catch(() => {});
	};
	const toggle = (row: TurnSettingRow) => {
		close();
		haptics.select();
		const write = turnSettingWrite(snapshot.settings, row, !row.enabled);
		if (write.kind === "option") void onOption(write.id, write.value).catch(() => {});
		else void onSettings(write.settings).catch(() => {});
	};
	const settingOpen = openMenu?.startsWith("setting:") ?? false;

	return (
		// The composer's slot starts its child rather than stretching it, and a
		// Host sized only to its height would take no width there at all.
		<Host matchContents={{ vertical: true }} colorScheme={scheme} style={{ alignSelf: "stretch" }}>
			<Row verticalAlignment="center" modifiers={[fillMaxWidth(), height(touchTarget)]}>
				{/* The model and its agent's mark, in a capsule the circles' height,
				    filled in the card colour as the plus's circle is. */}
				<DropdownMenu expanded={openMenu === "model" || openMenu === "older" || settingOpen} properties={KEEP_KEYBOARD} onDismissRequest={close} color={t.bgElevated}>
					<DropdownMenu.Trigger>
						<Button
							onClick={() => setOpenMenu("model")}
							colors={{ containerColor: t.bgElevated, contentColor: t.textPrimary }}
							contentPadding={{ start: space.md, end: space.xs }}
							enabled={!off && Boolean(modelRow || otherRows.length)}
							// The capsule shows the model alone; TalkBack hears what it is a choice of.
							modifiers={[height(COMPOSER_DISC), testID("chat-model"), semantics({ contentDescription: parts.model ? `Model, ${parts.model}` : "Model" })]}
						>
							{snapshot.harness ? <HarnessMark harness={snapshot.harness} /> : null}
							<Text maxLines={1} overflow="ellipsis" style={{ typography: "labelLarge", fontFamily: font.medium }} modifiers={[padding(snapshot.harness ? space.sm : 0, 0, space.xxs, 0)]}>
								{parts.model || "Model"}
							</Text>
							<Icon source={materialDrawable("unfold_more")} size={iconSize.md} tint={t.textTertiary} />
						</Button>
					</DropdownMenu.Trigger>
					<DropdownMenu.Items>
						{modelChoices.latest.map((choice) => (
							<MenuRow key={choice.value} label={choice.label} selected={choice.selected} width={MENU_WIDTH} onClick={() => choose(modelRow!, choice.value)} />
						))}
						{modelChoices.older.length ? (
							<>
								<HorizontalDivider color={t.borderDefault} />
								<DropdownMenu expanded={openMenu === "older"} properties={KEEP_KEYBOARD} onDismissRequest={() => setOpenMenu("model")} color={t.bgElevated}>
									<DropdownMenu.Trigger>
										<MenuRow
											label="Older models"
											trailing={<Icon source={materialDrawable("chevron_right")} size={iconSize.xl} tint={t.textTertiary} />}
											width={MENU_WIDTH}
											onClick={() => setOpenMenu("older")}
										/>
									</DropdownMenu.Trigger>
									<DropdownMenu.Items>
										{modelChoices.older.map((choice) => (
											<MenuRow key={choice.value} label={choice.label} selected={choice.selected} width={MENU_WIDTH} onClick={() => choose(modelRow!, choice.value)} />
										))}
									</DropdownMenu.Items>
								</DropdownMenu>
							</>
						) : null}
						{otherRows.length ? <HorizontalDivider color={t.borderDefault} /> : null}
						{otherRows.map((row) => row.kind === "boolean" ? (
							<MenuRow key={row.id} label={row.label} selected={Boolean(row.enabled)} width={MENU_WIDTH} onClick={() => toggle(row)} />
						) : (
							<DropdownMenu key={row.id} expanded={openMenu === `setting:${row.id}`} properties={KEEP_KEYBOARD} onDismissRequest={() => setOpenMenu("model")} color={t.bgElevated}>
								<DropdownMenu.Trigger>
									{/* Titled with its current choice, so TalkBack hears it without opening. */}
									<MenuRow
										label={`${row.label}: ${row.value}`}
										trailing={<Icon source={materialDrawable("chevron_right")} size={iconSize.xl} tint={t.textTertiary} />}
										width={MENU_WIDTH}
										onClick={() => setOpenMenu(`setting:${row.id}`)}
									/>
								</DropdownMenu.Trigger>
								<DropdownMenu.Items>
									{row.choices.map((choice) => (
										<MenuRow key={choice.value} label={choice.label} selected={choice.selected} width={MENU_WIDTH} onClick={() => choose(row, choice.value)} />
									))}
								</DropdownMenu.Items>
							</DropdownMenu>
						))}
					</DropdownMenu.Items>
				</DropdownMenu>

				{/* The effort as a speedometer lit to the chosen level; a tap opens
				    the slider over the composer. */}
				{effort?.choices.length ? (
					<RNHostView matchContents>
						<EffortGaugeButton choices={effort.choices} open={effort.open} onPress={() => { close(); effort.onToggle(); }} disabled={off} testID="chat-effort" />
					</RNHostView>
				) : null}

				<Spacer modifiers={[weight(1)]} />

				{/* The approval mode as its symbol alone; the menu and TalkBack name it. */}
				{approvalRow ? (
					<DropdownMenu expanded={openMenu === "approval"} properties={KEEP_KEYBOARD} onDismissRequest={close} color={t.bgElevated}>
						<DropdownMenu.Trigger>
							<IconButton onClick={() => setOpenMenu("approval")} enabled={!off} colors={{ contentColor: t.textSecondary }} modifiers={[testID("chat-approval")]}>
								<Icon source={materialDrawable("verified_user")} size={iconSize.lg} contentDescription={`Approval, ${approvalRow.value}`} />
							</IconButton>
						</DropdownMenu.Trigger>
						<DropdownMenu.Items>
							{approvalRow.choices.map((choice) => (
								<MenuRow key={choice.value} label={choice.label} selected={choice.selected} width={MENU_WIDTH} onClick={() => choose(approvalRow, choice.value)} />
							))}
						</DropdownMenu.Items>
					</DropdownMenu>
				) : null}
			</Row>
		</Host>
	);
}
