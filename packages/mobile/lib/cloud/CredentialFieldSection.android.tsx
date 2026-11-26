import type { ReactNode } from "react";
import { SettingsSection } from "../SettingsForm";

export type CredentialFieldSectionProps = {
	title: string;
	/** Explains the field, under it. */
	footer?: string;
	/** What went wrong with what was entered. The field shows it here. */
	error?: string | null;
	children?: ReactNode;
};

// Android: a section of a SettingsForm holding a form field (lib/formFields).
// The outlined field shows its own error as supporting text, so the section
// keeps its explanation.
export function CredentialFieldSection({ title, footer, children }: CredentialFieldSectionProps) {
	return <SettingsSection title={title} footer={footer}>{children}</SettingsSection>;
}
