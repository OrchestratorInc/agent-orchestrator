import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, expect, it, vi } from "vitest";

vi.mock("react-native", () => ({
	Text: "Text",
	Pressable: "Pressable",
	StyleSheet: { create: (styles: object) => styles },
}));
const router = vi.hoisted(() => ({ push: vi.fn() }));
vi.mock("expo-router", () => ({ useRouter: () => router }));
vi.mock("../icons", () => ({ Feather: "Feather" }));
vi.mock("../haptics", () => ({ haptics: { tap: () => {} } }));
vi.mock("../pressFeedback", () => ({ usePressFeedback: () => ({ android_ripple: undefined, pressed: {} }) }));
vi.mock("../store", () => ({ useApp: () => ({ currentHostId: "lab-host" }) }));
vi.mock("../ThemeProvider", () => {
	const theme = new Proxy({}, { get: () => "#000" });
	return { useTheme: () => theme, useThemedStyles: (factory: (t: unknown) => unknown) => factory(theme) };
});

import { ReportedArtifactRow } from "./ReportedArtifactRow";

let renderer: ReactTestRenderer | undefined;

beforeEach(() => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	router.push.mockClear();
	act(() => {
		renderer = create(<ReportedArtifactRow sessionId="ao-1" artifact={{ path: "q3/report.html", name: "report.html" }} />);
	});
});

afterEach(() => {
	act(() => renderer?.unmount());
	vi.unstubAllGlobals();
});

const row = () => renderer!.root.findByType("Pressable" as never);
const texts = () => renderer!.root.findAllByType("Text" as never).map((node) => node.props.children);

it("names the artifact in one row, with no page in it", () => {
	expect(texts()).toEqual(["Artifact", "report.html"]);
	expect(row().props.accessibilityRole).toBe("button");
	expect(row().props.accessibilityLabel).toBe("Open artifact report.html");
});

it("opens the artifact viewer for that path on the session's host", () => {
	act(() => row().props.onPress());
	expect(router.push).toHaveBeenCalledExactlyOnceWith({
		pathname: "/artifact/[sessionId]",
		params: { sessionId: "ao-1", path: "q3/report.html", hostId: "lab-host" },
	});
});
