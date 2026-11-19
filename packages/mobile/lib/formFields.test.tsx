import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, expect, it, vi } from "vitest";

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true, IS_REACT_NATIVE_TEST_ENVIRONMENT: true });

vi.mock("@expo/ui/swift-ui", async () => (await import("./nativeFormTestDoubles")).swiftUIDouble());
vi.mock("@expo/ui/swift-ui/modifiers", async () => {
	const doubles = await import("./nativeFormTestDoubles");
	return doubles.modifierDouble(doubles.swiftUIModifierNames);
});
vi.mock("./ThemeProvider", () => ({ useTheme: () => ({ red: "red" }) }));

import { FormSection } from "./formFields";

let renderer: ReactTestRenderer | undefined;
afterEach(() => {
	act(() => renderer?.unmount());
	renderer = undefined;
});

const footer = (props: { error?: string; hint?: string }) => {
	act(() => { renderer = create(<FormSection {...props}>{null}</FormSection>); });
	return renderer!.root.findByType("Section" as never).props.footer.props as { error: boolean; children?: string };
};

// The footer's text and its error colour come from one pair of props, so they
// can't drift apart.
it("shows an error in red, falling back to the hint when there is none or it is empty", () => {
	expect(footer({ error: "Enter a port from 1 to 65535.", hint: "Add a host to connect." })).toMatchObject({ error: true, children: "Enter a port from 1 to 65535." });
	expect(footer({ hint: "Add a host to connect." })).toMatchObject({ error: false, children: "Add a host to connect." });
	expect(footer({ error: "", hint: "Add a host to connect." })).toMatchObject({ error: false, children: "Add a host to connect." });
	expect(footer({})).toMatchObject({ error: false, children: undefined });
});
