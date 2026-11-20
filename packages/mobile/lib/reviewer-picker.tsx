import { DropdownMenu, DropdownMenuItem, Icon, ListItem, Text } from "@expo/ui/jetpack-compose";
import { alpha, clickable, fillMaxWidth, testID } from "@expo/ui/jetpack-compose/modifiers";
import { useState, type ReactNode } from "react";
import { logoFor } from "./harnessLogoAssets";
import { projectDefaultLabel, reviewerLabel, withCurrentOption, type ReviewerChoiceRow, type ReviewerPickerProps } from "./reviewer-picker.types";
import { useTheme, useThemeState } from "./ThemeProvider";
import { materialDrawable } from "./toolbarIcon.android";
import { font, iconSize, stateOpacity } from "./tokens";

type Menu = "reviewer" | "model" | "effort";

/**
 * The reviewer, its model and its effort as Material list items, each opening a dropdown
 * menu of the choices with a check on the current one: the menu Android's own
 * settings use for picking one value, anchored to the row it changes.
 */
export function ReviewerPicker({ reviewers, selectedReviewer, effectiveReviewer, onSelectReviewer, model, onSelectModel, effort, onSelectEffort, busy }: ReviewerPickerProps) {
	const [open, setOpen] = useState<Menu | null>(null);
	const reviewer = selectedReviewer || effectiveReviewer;
	const reviewerText = selectedReviewer ? reviewerLabel(reviewers, selectedReviewer) : projectDefaultLabel(reviewers, effectiveReviewer);

	function choose(menu: Menu, id: string) {
		setOpen(null);
		if (menu === "reviewer") onSelectReviewer(id);
		else if (menu === "model") onSelectModel(id);
		else onSelectEffort(id);
	}

	const settingRow = (menu: Menu, row: ReviewerChoiceRow) => (
		<PickerRow title={row.title} value={row.valueLabel} open={open === menu} busy={busy} tag={`review-${menu}`} onOpen={() => setOpen(menu)} onDismiss={() => setOpen(null)}>
			{row.options.map((option) => (
				<Choice key={option.id || "-"} label={option.label} selected={option.id === row.value} onPress={() => choose(menu, option.id)} />
			))}
		</PickerRow>
	);

	return (
		<>
			<PickerRow title="Reviewer" value={reviewerText} leading={<HarnessMark harness={reviewer} />} open={open === "reviewer"} busy={busy} tag="review-reviewer" onOpen={() => setOpen("reviewer")} onDismiss={() => setOpen(null)}>
				<Choice label={projectDefaultLabel(reviewers, effectiveReviewer)} leading={<Icon source={materialDrawable("group")} size={iconSize.xl} />} selected={!selectedReviewer} onPress={() => choose("reviewer", "")} />
				{withCurrentOption(reviewers, selectedReviewer).map((agent) => (
					<Choice key={agent.id} label={agent.label} leading={<HarnessMark harness={agent.id} />} selected={agent.id === selectedReviewer} onPress={() => choose("reviewer", agent.id)} />
				))}
			</PickerRow>
			{model ? settingRow("model", model) : null}
			{effort ? settingRow("effort", effort) : null}
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

/** A harness's brand mark in its own colours, the variant drawn for the app's colour scheme. */
function HarnessMark({ harness }: { harness: string }) {
	const { scheme } = useThemeState();
	const source = logoFor(harness, scheme);
	if (!source) return <Icon source={materialDrawable("terminal")} size={iconSize.xl} />;
	return <Icon source={source} tint={null} size={iconSize.xl} />;
}
