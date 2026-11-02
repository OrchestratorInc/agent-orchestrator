import { act, create } from "react-test-renderer";
import { expect, it, vi } from "vitest";

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true, IS_REACT_NATIVE_TEST_ENVIRONMENT: true });
type PasteEvent = { tag: number; images?: { mimeType?: string; bytes?: number; data?: string }[]; overflow?: string };
const { native, listeners } = vi.hoisted(() => ({
	native: { attach: vi.fn(), detach: vi.fn() },
	listeners: new Set<(event: PasteEvent) => void>(),
}));
vi.mock("react-native", () => ({
	NativeModules: { AOImagePaste: native },
	findNodeHandle: (ref: { tag: number }) => ref.tag,
	NativeEventEmitter: class {
		addListener(_name: string, listener: (event: PasteEvent) => void) {
			listeners.add(listener); return { remove: () => listeners.delete(listener) };
		}
	},
}));
import { pastedImageAssets, useImagePaste } from "./useImagePaste";
function NativeInput(_props: { onLayout: ReturnType<typeof useImagePaste> }) { return null; }
function Composer({ onPaste }: { onPaste: Parameters<typeof useImagePaste>[0] }) {
	const onLayout = useImagePaste(onPaste);
	return <NativeInput onLayout={onLayout} />;
}

it("maps native images onto picker assets, keeping a missing body as unreadable", () => {
	expect(pastedImageAssets(7, [{ mimeType: "image/png", bytes: 5, data: "aGVsbG8=" }, { mimeType: "image/jpeg", bytes: 0 }], 1)).toEqual([
		{ uri: "paste-7-1-0", fileName: "Pasted image", mimeType: "image/png", fileSize: 5, base64: "aGVsbG8=" },
		{ uri: "paste-7-1-1", fileName: "Pasted image", mimeType: "image/jpeg", fileSize: undefined, base64: undefined },
	]);
});

it("attaches to the field's tag, takes only its own pastes, and detaches when paste is turned off", () => {
	const first = vi.fn(), latest = vi.fn();
	let renderer!: ReturnType<typeof create>;
	act(() => { renderer = create(<Composer onPaste={first} />); });
	act(() => renderer.root.findByType(NativeInput).props.onLayout({ currentTarget: { tag: 42 } }));
	expect(native.attach).toHaveBeenCalledWith(42);
	act(() => renderer.update(<Composer onPaste={latest} />));
	expect(native.attach).toHaveBeenCalledTimes(1);
	act(() => { for (const listener of listeners) {
		listener({ tag: 11, images: [{ mimeType: "image/png", bytes: 5, data: "b3RoZXI=" }] });
		listener({ tag: 42, images: [] });
		listener({ tag: 42, images: [{ mimeType: "image/png", bytes: 5, data: "aGVsbG8=" }] });
	} });
	expect(first).not.toHaveBeenCalled();
	expect(latest).toHaveBeenCalledOnce();
	expect(latest.mock.calls[0]![0]).toMatchObject([{ mimeType: "image/png", base64: "aGVsbG8=" }]);
	expect(latest.mock.calls[0]![1]).toBeUndefined();
	act(() => { for (const listener of listeners) {
		listener({ tag: 42, images: [{ mimeType: "image/png", bytes: 5, data: "aGVsbG8=" }], overflow: "size" });
		listener({ tag: 42, images: [{ mimeType: "image/png", bytes: 5, data: "aGVsbG8=" }], overflow: "bogus" });
	} });
	expect(latest.mock.calls[1]![1]).toBe("size");
	expect(latest.mock.calls[2]![1]).toBeUndefined();
	act(() => renderer.update(<Composer onPaste={null} />));
	expect(native.detach).toHaveBeenCalledWith(42);
	expect(listeners.size).toBe(0);
});
