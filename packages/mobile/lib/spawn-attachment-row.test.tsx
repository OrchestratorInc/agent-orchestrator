import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const fixtures = vi.hoisted(() => ({
	theme: new Proxy({}, { get: () => "#000000" }),
}));

vi.mock("./ThemeProvider", () => ({
	useTheme: () => fixtures.theme,
	useThemedStyles: (make: (theme: object) => object) => make(fixtures.theme),
}));
vi.mock("./haptics", () => ({ haptics: { tap: vi.fn() } }));
vi.mock("./ImageViewer", async () => {
	const React = await import("react");
	return { ImageViewer: (props: object) => React.createElement("ImageViewer", props) };
});
vi.mock("./icons", async () => {
	const React = await import("react");
	return { Feather: (props: object) => React.createElement("Feather", props) };
});
vi.mock("react-native", async () => {
	const React = await import("react");
	const host = (name: string) => ({ children, ...props }: { children?: React.ReactNode }) =>
		React.createElement(name, props, children);
	return {
		Image: host("Image"),
		Pressable: host("Pressable"),
		ScrollView: host("ScrollView"),
		StyleSheet: { create: (styles: object) => styles, hairlineWidth: 1 },
		Text: host("Text"),
		View: host("View"),
	};
});

import { SpawnAttachmentRow } from "./spawn-attachment-row";

let renderer: ReactTestRenderer | undefined;

beforeEach(() => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	const error = console.error;
	vi.spyOn(console, "error").mockImplementation((...args) => {
		if (String(args[0]).startsWith("react-test-renderer is deprecated")) return;
		error(...args);
	});
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

		await act(async () => viewer.props.onClose());
		expect(root.findAll((node) => String(node.type) === "ImageViewer")).toHaveLength(0);
	});

	it("removes the tapped attachment", async () => {
		const onRemove = vi.fn();
		await act(async () => { renderer = create(<SpawnAttachmentRow attachments={[photo, notes]} onRemove={onRemove} />); });
		const remove = renderer!.root.findAll((node) => String(node.type) === "Pressable" && node.props.accessibilityLabel === "Remove IMG_0111.jpg")[0];
		await act(async () => remove.props.onPress());
		expect(onRemove).toHaveBeenCalledWith(photo);
	});
});
