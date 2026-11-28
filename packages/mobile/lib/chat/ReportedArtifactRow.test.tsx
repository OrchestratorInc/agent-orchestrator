import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

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
// The sessions the store holds, set per test.
const app = vi.hoisted(() => ({ sessions: undefined as undefined | Array<{ id: string; artifacts?: Array<{ path: string; previousPaths?: string[] }> }> }));
vi.mock("../store", () => ({ useApp: () => ({ currentHostId: "lab-host", sessions: app.sessions }) }));
vi.mock("../ThemeProvider", () => {
	const theme = new Proxy({}, { get: () => "#000" });
	return { useTheme: () => theme, useThemedStyles: (factory: (t: unknown) => unknown) => factory(theme) };
});

import { currentArtifactPath, ReportedArtifactRow } from "./ReportedArtifactRow";

let renderer: ReactTestRenderer | undefined;

function mount() {
	act(() => {
		renderer = create(<ReportedArtifactRow sessionId="ao-1" artifact={{ path: "q3/report.html", name: "report.html" }} />);
	});
}

beforeEach(() => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	router.push.mockClear();
	app.sessions = undefined;
});

afterEach(() => {
	act(() => renderer?.unmount());
	vi.unstubAllGlobals();
});

const row = () => renderer!.root.findByType("Pressable" as never);
const texts = () => renderer!.root.findAllByType("Text" as never).map((node) => node.props.children);
const pushedPath = () => (router.push.mock.calls[0]?.[0] as { params: { path: string } }).params.path;

it("names the artifact in one row, with no page in it, and is named by its visible text", () => {
	mount();
	expect(texts()).toEqual(["Artifact", "report.html"]);
	expect(row().props.accessibilityRole).toBe("button");
	expect(row().props.accessibilityLabel).toBe("Artifact report.html");
});

it("opens the artifact viewer for that path on the session's host", () => {
	mount();
	act(() => row().props.onPress());
	expect(router.push).toHaveBeenCalledExactlyOnceWith({
		pathname: "/artifact/[sessionId]",
		params: { sessionId: "ao-1", path: "q3/report.html", hostId: "lab-host" },
	});
});

it("opens a file renamed since it was reported where it is now", () => {
	app.sessions = [{ id: "ao-1", artifacts: [{ path: "other.html" }, { path: "q3/final/report.html", previousPaths: ["q3/report.html"] }] }];
	mount();
	act(() => row().props.onPress());
	expect(pushedPath()).toBe("q3/final/report.html");
});

describe("currentArtifactPath", () => {
	it("prefers the file listed at the reported path over one renamed away from it", () => {
		expect(currentArtifactPath([{ path: "old/report.html", previousPaths: ["q3/report.html"] }, { path: "q3/report.html" }], "q3/report.html")).toBe("q3/report.html");
	});

	it("keeps the reported path when the list does not hold the file or is not loaded", () => {
		expect(currentArtifactPath([{ path: "other.html" }], "q3/report.html")).toBe("q3/report.html");
		expect(currentArtifactPath(undefined, "q3/report.html")).toBe("q3/report.html");
	});
});
