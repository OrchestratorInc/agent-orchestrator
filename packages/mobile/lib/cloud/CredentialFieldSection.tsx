import { FieldGroup } from "@expo/ui";
import { Text } from "@expo/ui/swift-ui";
import { accessibilityAddTraits, foregroundStyle, listRowBackground } from "@expo/ui/swift-ui/modifiers";
import type { ReactNode } from "react";
import { appFont } from "../swiftUIFont";
import { useTheme } from "../ThemeProvider";

export type CredentialFieldSectionProps = {
	title: string;
	/** Explains the field, under it. */
	footer?: string;
	/** What went wrong with what was entered. */
	error?: string | null;
	children?: ReactNode;
};

// iOS: a section of a SettingsForm holding a form field (lib/formFields). The
// Settings rows paint the theme's row surface themselves and a field does not,
// so the section paints it for its rows. iOS has no per-field error, so the
// footer says what went wrong, in red, in place of the explanation.
export function CredentialFieldSection({ title, footer, error, children }: CredentialFieldSectionProps) {
	const t = useTheme();
	const note = error || footer;
	return (
		<FieldGroup.Section modifiers={[listRowBackground(t.bgElevated)]}>
			<FieldGroup.SectionHeader>
				<Text modifiers={[appFont("footnote"), foregroundStyle(t.textSecondary), accessibilityAddTraits(["isHeader"])]}>{title}</Text>
			</FieldGroup.SectionHeader>
			{children}
			{/* Always mounted, empty or not: a footer that came and went rebuilt the
			    section and took the keyboard from the field (see SettingsSection). */}
			<FieldGroup.SectionFooter>
				<FooterNote note={note} error={Boolean(error)} />
			</FieldGroup.SectionFooter>
		</FieldGroup.Section>
	);
}

/** The explanation, or what went wrong in red; nothing at all without either. */
function FooterNote({ note, error }: { note?: string | null; error: boolean }) {
	const t = useTheme();
	if (!note) return null;
	return <Text modifiers={[appFont("footnote"), foregroundStyle(error ? t.red : t.textSecondary)]}>{note}</Text>;
}
