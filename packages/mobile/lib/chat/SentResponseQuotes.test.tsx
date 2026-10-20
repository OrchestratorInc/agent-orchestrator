import { act, create } from "react-test-renderer";
import { expect, it, vi } from "vitest";

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
vi.mock("react-native", () => ({ Text: "Text", View: "View" }));
vi.mock("../ThemeProvider", () => ({ useTheme: () => ({ textSecondary: "gray", accentTint: "selection" }) }));
import { SentResponseQuotes } from "./SentResponseQuotes";

it("shows desktop excerpt content as full selected text, without an annotation chip or unrelated resources", () => {
	let renderer!: ReturnType<typeof create>;
	act(() => { renderer = create(<SentResponseQuotes content={[
		{ type: "excerpt", text: "Title\n• Selected bullet", sourceMessageId: "source", sourceRevision: 2 },
		{ type: "resource", text: "private resource" },
		{ type: "excerpt", text: "Second selection" },
	]} />); });
	const selections = renderer.root.findAll((node) => node.props.selectable === true);
	expect(selections.map((node) => node.children.join(""))).toEqual(["Title\n• Selected bullet", "Second selection"]);
	expect(selections.every((node) => node.props.numberOfLines === undefined)).toBe(true);
	act(() => renderer.unmount());
});
