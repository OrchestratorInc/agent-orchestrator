/**
 * One row of a native form. On iOS each renders as a row of a SwiftUI `Form`
 * section; on Android as a Material 3 control inside a Compose column. Neither
 * is a host of its own, so they always sit inside a form's native container.
 */
export type FormTextFieldProps = {
	/** The visible label, which is also what screen readers announce. */
	label: string;
	value: string;
	onChangeText: (text: string) => void;
	placeholder?: string;
	keyboardType?: "default" | "url" | "number-pad";
	autoCapitalize?: "none" | "sentences" | "words";
	autoCorrect?: boolean;
	maxLength?: number;
	autoFocus?: boolean;
	returnKeyType?: "next" | "done";
	onSubmitEditing?: () => void;
	editable?: boolean;
	/**
	 * What is wrong with the value. Android shows it under the field as Material
	 * supporting text in the error colour. iOS has no per-field error, so the
	 * form puts it in the section footer instead.
	 */
	error?: string;
	testID?: string;
};

export type FormPasswordFieldProps = Omit<FormTextFieldProps, "keyboardType" | "autoCapitalize" | "autoCorrect" | "maxLength">;

export type FormToggleProps = {
	label: string;
	value: boolean;
	onValueChange: (value: boolean) => void;
	disabled?: boolean;
	testID?: string;
};
