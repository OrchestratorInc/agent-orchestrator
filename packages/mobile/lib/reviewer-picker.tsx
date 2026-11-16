import { Box, DropdownMenu, DropdownMenuItem, Icon, ListItem, Text } from "@expo/ui/jetpack-compose";
import { alpha, background, clickable, clip, fillMaxWidth, Shapes, size, testID } from "@expo/ui/jetpack-compose/modifiers";
import { useState, type ReactNode } from "react";
import { chipColorFor } from "./harnessLogo";
import { logoFor } from "./harnessLogoAssets";
import { projectDefaultLabel, reviewerLabel, withCurrentOption, type ReviewerPickerProps } from "./reviewer-picker.types";
import { useTheme } from "./ThemeProvider";
import { materialDrawable } from "./toolbarIcon.android";
import { font, iconSize, radius, stateOpacity } from "./tokens";

type Menu = "reviewer" | "model";

/**
 * The reviewer and its model as Material list items, each opening a dropdown
 * menu of the choices with a check on the current one: the menu Android's own
 * settings use for picking one value, anchored to the row it changes.
 */
export function ReviewerPicker({ reviewers, selectedReviewer, effectiveReviewer, onSelectReviewer, models, modelTitle, selectedModel, onSelectModel, busy }: ReviewerPickerProps) {
	const [open, setOpen] = useState<Menu | null>(null);
	const reviewer = selectedReviewer || effectiveReviewer;
	const reviewerText = selectedReviewer ? reviewerLabel(reviewers, selectedReviewer) : projectDefaultLabel(reviewers, effectiveReviewer);
	const modelText = selectedModel ? reviewerLabel(models, selectedModel) : "Provider default";

	function choose(menu: Menu, id: string) {
		setOpen(null);
		if (menu === "reviewer") onSelectReviewer(id);
		else onSelectModel(id);
	}

	return (
		<>
			<PickerRow title="Reviewer" value={reviewerText} leading={<HarnessMark harness={reviewer} />} open={open === "reviewer"} busy={busy} tag="review-reviewer" onOpen={() => setOpen("reviewer")} onDismiss={() => setOpen(null)}>
				<Choice label={projectDefaultLabel(reviewers, effectiveReviewer)} leading={<Icon source={materialDrawable("group")} size={iconSize.xl} />} selected={!selectedReviewer} onPress={() => choose("reviewer", "")} />
				{withCurrentOption(reviewers, selectedReviewer).map((agent) => (
					<Choice key={agent.id} label={agent.label} leading={<HarnessMark harness={agent.id} />} selected={agent.id === selectedReviewer} onPress={() => choose("reviewer", agent.id)} />
				))}
			</PickerRow>
			{models.length ? (
				<PickerRow title={modelTitle} value={modelText} open={open === "model"} busy={busy} tag="review-model" onOpen={() => setOpen("model")} onDismiss={() => setOpen(null)}>
					<Choice label="Provider default" selected={!selectedModel} onPress={() => choose("model", "")} />
					{withCurrentOption(models, selectedModel).map((model) => (
						<Choice key={model.id} label={model.label} selected={model.id === selectedModel} onPress={() => choose("model", model.id)} />
					))}
				</PickerRow>
			) : null}
		</>
	);
}

function PickerRow({ title, value, leading, open, busy, tag, onOpen, onDismiss, children }: { title: string; value: string; leading?: ReactNode; open: boolean; busy: boolean; tag: string; onOpen: () => void; onDismiss: () => void; children: ReactNode }) {
	const t = useTheme();
	return (
		<DropdownMenu expanded={open && !busy} onDismissRequest={onDismiss} color={t.bgElevated} modifiers={[fillMaxWidth()]}>
			<DropdownMenu.Trigger>
				<ListItem
					colors={{ containerColor: t.bgSurface, contentColor: t.textPrimary, supportingContentColor: t.textTertiary, trailingContentColor: t.textTertiary }}
					modifiers={[...(busy ? [alpha(stateOpacity.disabled)] : [clickable(onOpen)]), testID(tag)]}
				>
					<ListItem.HeadlineContent><Text style={{ typography: "bodyLarge", fontFamily: font.regular }}>{title}</Text></ListItem.HeadlineContent>
					<ListItem.SupportingContent><Text maxLines={1} overflow="ellipsis" style={{ typography: "bodyMedium", fontFamily: font.regular }}>{value}</Text></ListItem.SupportingContent>
					{leading ? <ListItem.LeadingContent>{leading}</ListItem.LeadingContent> : null}
					<ListItem.TrailingContent><Icon source={materialDrawable("expand_more")} size={iconSize.xl} /></ListItem.TrailingContent>
				</ListItem>
			</DropdownMenu.Trigger>
			<DropdownMenu.Items>{children}</DropdownMenu.Items>
		</DropdownMenu>
	);
}

function Choice({ label, leading, selected, onPress }: { label: string; leading?: ReactNode; selected: boolean; onPress: () => void }) {
	const t = useTheme();
	return (
		<DropdownMenuItem elementColors={{ textColor: t.textPrimary, trailingIconColor: t.textPrimary }} onClick={onPress}>
			<DropdownMenuItem.Text><Text>{label}</Text></DropdownMenuItem.Text>
			{leading ? <DropdownMenuItem.LeadingIcon>{leading}</DropdownMenuItem.LeadingIcon> : null}
			{selected ? (
				<DropdownMenuItem.TrailingIcon>
					<Icon source={materialDrawable("check")} size={iconSize.xl} contentDescription="Selected" />
				</DropdownMenuItem.TrailingIcon>
			) : null}
		</DropdownMenuItem>
	);
}

/** A harness's brand mark in its own colours, on a chip only where the mark needs one to show. */
function HarnessMark({ harness }: { harness: string }) {
	const source = logoFor(harness);
	if (!source) return <Icon source={materialDrawable("terminal")} size={iconSize.xl} />;
	const chip = chipColorFor(harness);
	if (!chip) return <Icon source={source} tint={null} size={iconSize.xl} />;
	return (
		<Box contentAlignment="center" modifiers={[size(iconSize.xl, iconSize.xl), clip(Shapes.RoundedCorner(radius.sm)), background(chip)]}>
			<Icon source={source} tint={null} size={iconSize.md} />
		</Box>
	);
}
