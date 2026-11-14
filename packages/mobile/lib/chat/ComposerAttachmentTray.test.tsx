import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const fixtures = vi.hoisted(() => ({
	theme: new Proxy({}, { get: () => "#000000" }),
	tap: vi.fn(),
}));

vi.mock("../ThemeProvider", () => ({
	useTheme: () => fixtures.theme,
	useThemedStyles: (make: (theme: object) => object) => make(fixtures.theme),
}));
vi.mock("../haptics", () => ({ haptics: { tap: fixtures.tap } }));
vi.mock("../ImageViewer", async () => {
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

import { ComposerAttachmentTray, removeSlop } from "./ComposerAttachmentTray";
import { iconSize, touchTarget } from "../tokens";

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

const photo = { key: "photo", name: "IMG_0111.jpg", previewUri: "file:///IMG_0111.jpg" };
const notes = { key: "notes", name: "notes.pdf" };

describe("ComposerAttachmentTray", () => {
	it("shows images as thumbnails that open full screen, and files as named tiles", async () => {
		await act(async () => { renderer = create(<ComposerAttachmentTray items={[photo, notes]} onRemove={vi.fn()} />); });
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

	// A send can land while a photo is open and take every attachment with it.
	// The tray stays mounted, so a stale selection must not reopen on the next pick.
	it("drops the open photo once its attachment is gone", async () => {
		await act(async () => { renderer = create(<ComposerAttachmentTray items={[photo]} onRemove={vi.fn()} />); });
		const open = renderer!.root.findAll((node) => String(node.type) === "Pressable" && node.props.accessibilityLabel === "Open IMG_0111.jpg")[0];
		await act(async () => open.props.onPress());
		expect(renderer!.root.findAll((node) => String(node.type) === "ImageViewer")).toHaveLength(1);

		await act(async () => renderer!.update(<ComposerAttachmentTray items={[]} onRemove={vi.fn()} />));
		expect(renderer!.toJSON()).toBeNull();

		await act(async () => renderer!.update(<ComposerAttachmentTray items={[photo]} onRemove={vi.fn()} />));
		expect(renderer!.root.findAll((node) => String(node.type) === "ImageViewer")).toHaveLength(0);
	});

	it("keeps its scroll view out of a form sheet's scroll view lookup", async () => {
		await act(async () => { renderer = create(<ComposerAttachmentTray items={[photo]} onRemove={vi.fn()} />); });
		const wrapper = renderer!.root.findAll((node) => String(node.type) === "View" && node.props.collapsable === false)[0];
		expect(wrapper?.findAll((node) => String(node.type) === "ScrollView")).toHaveLength(1);
	});

	it("removes the tapped attachment", async () => {
		const onRemove = vi.fn();
		await act(async () => { renderer = create(<ComposerAttachmentTray items={[photo, notes]} onRemove={onRemove} />); });
		const remove = renderer!.root.findAll((node) => String(node.type) === "Pressable" && node.props.accessibilityLabel === "Remove IMG_0111.jpg")[0];
		await act(async () => remove.props.onPress());
		expect(onRemove).toHaveBeenCalledWith("photo");
		expect(fixtures.tap).toHaveBeenCalledOnce();
	});

	// A tile clips touches to its own bounds (hit slop is not overflow), so the
	// disc sits far enough inside the corner that its whole target is in the tile.
	it("reaches a full touch target to remove, inside the tile", async () => {
		await act(async () => { renderer = create(<ComposerAttachmentTray items={[photo, notes]} onRemove={vi.fn()} />); });
		const root = renderer!.root;
		const removes = root.findAll((node) => String(node.type) === "Pressable" && String(node.props.accessibilityLabel).startsWith("Remove "));
		expect(removes).toHaveLength(2);
		for (const remove of removes) {
			const disc = remove.props.style({ pressed: false })[0];
			expect(remove.props.hitSlop).toBe(removeSlop);
			expect(disc.width).toBe(iconSize.xl);
			expect(disc.width + remove.props.hitSlop * 2).toBe(touchTarget);
			expect(disc.top).toBeGreaterThanOrEqual(remove.props.hitSlop);
			expect(disc.right).toBeGreaterThanOrEqual(remove.props.hitSlop);
		}
	});

	it("draws nothing without attachments", async () => {
		await act(async () => { renderer = create(<ComposerAttachmentTray items={[]} onRemove={vi.fn()} />); });
		expect(renderer!.toJSON()).toBeNull();
	});
});
