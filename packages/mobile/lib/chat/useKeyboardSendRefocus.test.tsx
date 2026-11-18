import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, expect, it, vi } from "vitest";

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true, IS_REACT_NATIVE_TEST_ENVIRONMENT: true });
const { focusedInput } = vi.hoisted(() => ({ focusedInput: { current: null as unknown } }));
vi.mock("react-native", () => ({ TextInput: { State: { currentlyFocusedInput: () => focusedInput.current } } }));

import { useKeyboardSendRefocus } from "./useKeyboardSendRefocus";

type Field = { focus: ReturnType<typeof vi.fn>; isFocused: ReturnType<typeof vi.fn> };
let latest!: ReturnType<typeof useKeyboardSendRefocus>;
function Composer({ field, submitting }: { field: Field; submitting: boolean }) {
	latest = useKeyboardSendRefocus({ current: field } as never, submitting);
	return null;
}
const makeField = (focused = false): Field => ({ focus: vi.fn(), isFocused: vi.fn(() => focused) });
let renderer: ReactTestRenderer;
beforeEach(() => { vi.useFakeTimers(); focusedInput.current = null; });
afterEach(() => { act(() => renderer.unmount()); vi.useRealTimers(); });

/** Start a keyboard send, then let it land. As in the composers, `begin()` and
 * the switch to submitting happen in one render. */
function send(field: Field) {
	act(() => { renderer = create(<Composer field={field} submitting={false} />); });
	act(() => { latest.begin(); renderer.update(<Composer field={field} submitting />); });
	act(() => renderer.update(<Composer field={field} submitting={false} />));
}

it("gives the field its focus back once the send lands, and holds until the field reports it", () => {
	const field = makeField();
	act(() => { renderer = create(<Composer field={field} submitting={false} />); });
	act(() => { latest.begin(); renderer.update(<Composer field={field} submitting />); });
	// In flight: the field is read-only, so nothing is focused yet.
	expect(field.focus).not.toHaveBeenCalled();
	expect(latest.holding).toBe(true);
	act(() => renderer.update(<Composer field={field} submitting={false} />));
	expect(field.focus).toHaveBeenCalledOnce();
	expect(latest.holding).toBe(true);
	act(() => latest.focused());
	expect(latest.holding).toBe(false);
});

it("leaves focus with another input the user moved to while the send was in flight", () => {
	const field = makeField();
	focusedInput.current = { other: true };
	send(field);
	expect(field.focus).not.toHaveBeenCalled();
	expect(latest.holding).toBe(false);
});

it("does nothing more when the field kept its focus", () => {
	const field = makeField(true);
	focusedInput.current = field;
	send(field);
	expect(field.focus).not.toHaveBeenCalled();
	expect(latest.holding).toBe(false);
});

it("stops holding when the field's focus event never arrives", () => {
	const field = makeField();
	send(field);
	expect(latest.holding).toBe(true);
	act(() => { vi.advanceTimersByTime(500); });
	expect(latest.holding).toBe(false);
});

it("refocuses once per keyboard send, even when another send runs before the focus event", () => {
	const field = makeField();
	send(field);
	expect(field.focus).toHaveBeenCalledOnce();
	// A send from the button, before the field has reported focus.
	act(() => renderer.update(<Composer field={field} submitting />));
	act(() => renderer.update(<Composer field={field} submitting={false} />));
	expect(field.focus).toHaveBeenCalledOnce();
	act(() => { vi.advanceTimersByTime(500); });
	expect(latest.holding).toBe(false);
});
