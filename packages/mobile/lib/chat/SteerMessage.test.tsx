import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("react-native", () => ({ View: "View", Text: "Text", StyleSheet: { create: (styles: object) => styles } }));
vi.mock("../icons", () => ({ Feather: "Feather" }));
vi.mock("../ThemeProvider", () => {
	const theme = new Proxy({}, { get: () => "#000000" });
	return { useTheme: () => theme, useThemedStyles: (factory: (value: object) => object) => factory(theme) };
});

import { isAutomationSteer, SteerNote, steerNoteText } from "./SteerMessage";
import type { ActivityDetail } from "./types";

let renderer: ReactTestRenderer | undefined;
beforeEach(() => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	const error = console.error;
	vi.spyOn(console, "error").mockImplementation((...args) => {
		if (String(args[0]).startsWith("react-test-renderer is deprecated")) return;
		error(...args);
	});
});
afterEach(() => {
	act(() => renderer?.unmount());
	renderer = undefined;
	vi.restoreAllMocks();
});

function draw(detail: ActivityDetail) {
	act(() => { renderer = create(<SteerNote detail={detail} />); });
	const root = renderer!.root.findByType("View" as never);
	const styles = [root.props.style].flat() as Array<{ alignSelf?: string }>;
	return {
		text: renderer!.root.findByType("Text" as never).props.children as string,
		icon: renderer!.root.findByType("Feather" as never).props.name as string,
		align: styles.map((style) => style.alignSelf).find(Boolean),
	};
}

describe("the steered note", () => {
	it("marks the person's own steer under its bubble, at the bubble's edge, with a ↳", () => {
		expect(draw({ event: "steer", text: "Use staging", origin: "human" })).toEqual({ text: "Steered", icon: "corner-down-right", align: "flex-end" });
	});

	it("says the agent got a steer the person deleted while it was being taken", () => {
		expect(draw({ event: "steer", text: "x", deliveredBeforeDelete: true }).text).toBe("Steered: the agent took it before your delete could stop it");
	});

	it("marks another agent's steer at the start, under its sender's row", () => {
		expect(draw({ event: "steer", text: "Report", senderSessionId: "worker-1" })).toMatchObject({ text: "Steered", align: "flex-start" });
		expect(draw({ event: "steer", text: "CI is red", origin: "automation", senderDisplayName: "Orchestrator" }).align).toBe("flex-start");
	});

	it("reads an automation without a label as the person's, as the desktop does", () => {
		expect(isAutomationSteer({ event: "steer", origin: "automation" })).toBe(false);
		expect(isAutomationSteer({ event: "steer", senderSessionId: "  " })).toBe(false);
		expect(steerNoteText(undefined)).toBe("Steered");
	});
});
