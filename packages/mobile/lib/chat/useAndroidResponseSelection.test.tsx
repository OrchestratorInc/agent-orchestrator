import { act, create } from "react-test-renderer";
import { expect, it, vi } from "vitest";

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true, IS_REACT_NATIVE_TEST_ENVIRONMENT: true });
const { native, listeners, platform } = vi.hoisted(() => ({
	native: { attach: vi.fn(), detach: vi.fn() },
	listeners: new Set<(event: { tag: number; text: string }) => void>(),
	platform: { OS: "android" },
}));
vi.mock("react-native", () => ({
	NativeModules: { AOResponseSelection: native }, Platform: platform,
	findNodeHandle: (ref: { tag: number }) => ref.tag,
	DeviceEventEmitter: { addListener: (_name: string, listener: (event: { tag: number; text: string }) => void) => {
		listeners.add(listener); return { remove: () => listeners.delete(listener) };
	} },
}));
import { useAndroidResponseSelection } from "./useAndroidResponseSelection";
function NativeText(_props: { onLayout: ReturnType<typeof useAndroidResponseSelection> }) { return null; }
function Response({ onAdd }: { onAdd: ((text: string) => void) | null }) {
	const onLayout = useAndroidResponseSelection(onAdd);
	return <NativeText onLayout={onLayout} />;
}

it("attaches to the native text tag, quotes only its own selection, and removes the action on unmount", () => {
	const first = vi.fn(), latest = vi.fn();
	let renderer!: ReturnType<typeof create>;
	act(() => { renderer = create(<Response onAdd={first} />); });
	act(() => renderer.root.findByType(NativeText).props.onLayout({ currentTarget: { tag: 42 } }));
	expect(native.attach).toHaveBeenCalledWith(42);
	act(() => renderer.update(<Response onAdd={latest} />));
	act(() => { for (const listener of listeners) {
		listener({ tag: 11, text: "Different response" });
		listener({ tag: 42, text: "Exact\n• selected 👋" });
	} });
	expect(first).not.toHaveBeenCalled();
	expect(latest).toHaveBeenCalledExactlyOnceWith("Exact\n• selected 👋");
	act(() => renderer.unmount());
	expect(native.detach).toHaveBeenCalledWith(42);
	expect(listeners.size).toBe(0);
});
