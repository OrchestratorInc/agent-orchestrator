import { createElement, useRef, type ReactNode } from "react";

// Test doubles for the native form views (SwiftUI and Compose), for tests that
// render a form with react-test-renderer under node. Each view is a host
// element named after it that keeps every prop, so a test can read what a field
// was given and call its handlers. Use them from a `vi.mock` factory:
//
//   vi.mock("@expo/ui/swift-ui", async () => (await import("./nativeFormTestDoubles")).swiftUIDouble());

/** A host element for a view with slot statics, which a plain string cannot carry. */
function host(name: string) {
	return function NativeView({ children, ...props }: { children?: ReactNode }) {
		return createElement(name, props, children);
	};
}

/** `useNativeState` without the native side: a value with get and set, one per component. */
export function useTestNativeState<T>(initial: T) {
	const ref = useRef<{ value: T; get(): T; set(value: T): void } | null>(null);
	if (!ref.current) {
		const state = { value: initial, get: () => state.value, set: (value: T) => { state.value = value; } };
		ref.current = state;
	}
	return ref.current;
}

/** Modifier factories that record their name and arguments, e.g. `{ $type: "tint", args: ["#fff"] }`. */
export function modifierDouble(names: readonly string[]) {
	return Object.fromEntries(names.map((name) => [name, (...args: unknown[]) => ({ $type: name, args })]));
}

export function swiftUIDouble() {
	return {
		Button: "Button",
		Form: "Form",
		HStack: "HStack",
		Image: "Image",
		Label: "Label",
		LabeledContent: "LabeledContent",
		Picker: "Picker",
		ProgressView: "ProgressView",
		SecureField: "SecureField",
		Section: "Section",
		Spacer: "Spacer",
		Text: "Text",
		TextField: "TextField",
		Toggle: "Toggle",
		VStack: "VStack",
		DisclosureGroup: Object.assign(host("DisclosureGroup"), { Label: host("DisclosureGroupLabel") }),
		useNativeState: useTestNativeState,
	};
}

export const swiftUIModifierNames = [
	"accessibilityLabel", "accessibilityValue", "autocorrectionDisabled", "background", "buttonStyle", "disabled",
	"font", "foregroundStyle", "frame", "keyboardType", "labelStyle", "lineLimit", "listRowBackground",
	"multilineTextAlignment", "onSubmit", "pickerStyle", "refreshable", "scrollContentBackground",
	"scrollDismissesKeyboard", "submitLabel", "tag", "textContentType", "textInputAutocapitalization",
	"textSelection", "tint", "truncationMode",
] as const;

export function composeDouble() {
	const withSlots = <S extends string>(name: string, slots: readonly S[]) =>
		Object.assign(host(name), Object.fromEntries(slots.map((slot) => [slot, host(`${name}.${slot}`)])) as Record<S, ReturnType<typeof host>>);
	return {
		AnimatedVisibility: "AnimatedVisibility",
		Column: "Column",
		Host: "Host",
		Icon: "Icon",
		IconButton: "IconButton",
		LazyColumn: "LazyColumn",
		PullToRefreshBox: "PullToRefreshBox",
		Row: "Row",
		Switch: "Switch",
		Text: "Text",
		TextButton: "TextButton",
		EnterTransition: { expandVertically: () => ({ plus: () => ({}) }) },
		ExitTransition: { shrinkVertically: () => ({ plus: () => ({}) }) },
		ListItem: withSlots("ListItem", ["HeadlineContent", "SupportingContent", "LeadingContent", "TrailingContent", "OverlineContent"] as const),
		OutlinedTextField: withSlots("OutlinedTextField", ["Label", "Placeholder", "SupportingText", "TrailingIcon", "LeadingIcon"] as const),
		useNativeState: useTestNativeState,
	};
}

export const composeModifierNames = [
	"animateContentSize", "background", "clickable", "clip", "combinedClickable", "fillMaxSize", "fillMaxWidth", "padding",
	"paddingAll", "semantics", "testID", "toggleable", "weight",
] as const;

export function composeModifierDouble() {
	return {
		...modifierDouble(composeModifierNames),
		Shapes: { RoundedCorner: (radii: unknown) => ({ $shape: "roundedCorner", radii }) },
	};
}
