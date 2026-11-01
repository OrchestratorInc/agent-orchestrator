import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const fixtures = vi.hoisted(() => ({
	theme: new Proxy({}, { get: () => "#000000" }),
	tap: vi.fn(),
}));

vi.mock("./ThemeProvider", () => ({
	useTheme: () => fixtures.theme,
	useThemedStyles: (make: (theme: object) => object) => make(fixtures.theme),
}));
vi.mock("./haptics", () => ({ haptics: { tap: fixtures.tap } }));
vi.mock("./ImageViewer", async () => {
	const React = await import("react");
	return { ImageViewer: (props: object) => React.createElement("ImageViewer", props) };
});
vi.mock("react-native", async () => {
	const React = await import("react");
	const host = (name: string) => ({ children, ...props }: { children?: React.ReactNode }) =>
		React.createElement(name, props, children);
	return {
		Image: host("Image"),
		Platform: { OS: "ios" },
		Pressable: host("Pressable"),
		ScrollView: host("ScrollView"),
		StyleSheet: { create: (styles: object) => styles, hairlineWidth: 1 },
		Text: host("Text"),
		View: host("View"),
	};
});

import { SpawnAttachmentRow } from "./spawn-attachment-row";
import { symbols } from "./symbols";
import { iconSize, touchTarget } from "./tokens";

let renderer: ReactTestRenderer | undefined;

beforeEach(() => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	const error = console.error;
	vi.spyOn(console, "error").mockImplementation((...args) => {
		if (String(args[0]).startsWith("react-test-renderer is deprecated")) return;
		error(...args);
	});
	fixtures.tap.mockClear();
});

afterEach(async () => {
	if (renderer) await act(async () => renderer?.unmount());
	renderer = undefined;
	vi.restoreAllMocks();
	vi.unstubAllGlobals();
});

const photo = { name: "IMG_0111.jpg", mimeType: "image/jpeg", data: "YQ==", bytes: 3, previewUri: "file:///IMG_0111.jpg" };
const notes = { name: "notes.pdf", mimeType: "application/pdf", data: "YQ==", bytes: 3 };

describe("SpawnAttachmentRow", () => {
	it("shows images as thumbnails that open full screen, and files as name chips", async () => {
		await act(async () => { renderer = create(<SpawnAttachmentRow attachments={[photo, notes]} onRemove={vi.fn()} />); });
		const root = renderer!.root;

		expect(root.findAll((node) => String(node.type) === "Image").map((node) => node.props.source)).toEqual([{ uri: "file:///IMG_0111.jpg" }]);
		expect(root.findAll((node) => String(node.type) === "Text").map((node) => node.props.children)).toEqual(["notes.pdf"]);
		expect(root.findAll((node) => String(node.type) === "ImageViewer")).toHaveLength(0);

		const open = root.findAll((node) => String(node.type) === "Pressable" && node.props.accessibilityLabel === "Open IMG_0111.jpg")[0];
		await act(async () => open.props.onPress());
		const viewer = root.findAll((node) => String(node.type) === "ImageViewer")[0];
		expect(viewer.props).toMatchObject({ visible: true, name: "IMG_0111.jpg", source: { uri: "file:///IMG_0111.jpg" } });
		// Opening a preview is navigation, not a moment for a haptic.
		expect(fixtures.tap).not.toHaveBeenCalled();

		await act(async () => viewer.props.onClose());
		expect(root.findAll((node) => String(node.type) === "ImageViewer")).toHaveLength(0);
	});

	it("keeps its scroll view out of the form sheet's scroll view lookup", async () => {
		await act(async () => { renderer = create(<SpawnAttachmentRow attachments={[photo]} onRemove={vi.fn()} />); });
		const wrapper = renderer!.root.findAll((node) => String(node.type) === "View" && node.props.collapsable === false)[0];
		expect(wrapper?.findAll((node) => String(node.type) === "ScrollView")).toHaveLength(1);
	});

	it("removes the tapped attachment", async () => {
		const onRemove = vi.fn();
		await act(async () => { renderer = create(<SpawnAttachmentRow attachments={[photo, notes]} onRemove={onRemove} />); });
		const remove = renderer!.root.findAll((node) => String(node.type) === "Pressable" && node.props.accessibilityLabel === "Remove IMG_0111.jpg")[0];
		await act(async () => remove.props.onPress());
		expect(onRemove).toHaveBeenCalledWith(photo);
		expect(fixtures.tap).toHaveBeenCalledOnce();
	});

	it("draws chrome as system symbols and reaches a full touch target to remove", async () => {
		await act(async () => { renderer = create(<SpawnAttachmentRow attachments={[photo, notes]} onRemove={vi.fn()} />); });
		const root = renderer!.root;
		const glyphs = root.findAllByType("SymbolView" as never).map((node) => node.props.name);
		expect(glyphs).toEqual([symbols.close, symbols.fileText, symbols.close]);

		const removes = root.findAll((node) => String(node.type) === "Pressable" && String(node.props.accessibilityLabel).startsWith("Remove "));
		const reach = (node: (typeof removes)[number], glyph: number) => glyph + node.props.hitSlop * 2;
		expect(reach(removes[0], iconSize.lg)).toBe(touchTarget);
		expect(reach(removes[1], iconSize.sm)).toBe(touchTarget);

		// The scroll view drops touches outside its bounds, so the row keeps room
		// for the slop the thumbnail's corner disc reaches past its top and right.
		const disc = removes[0].props.style({ pressed: false })[0];
		const content = root.findAll((node) => String(node.type) === "ScrollView")[0].props.contentContainerStyle;
		expect(disc).toMatchObject({ width: iconSize.lg, height: iconSize.lg });
		expect(content.paddingTop).toBeGreaterThanOrEqual(-disc.top + removes[0].props.hitSlop);
		expect(content.paddingRight).toBeGreaterThanOrEqual(-disc.right + removes[0].props.hitSlop);
	});
});
