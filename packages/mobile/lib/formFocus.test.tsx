import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { TextFieldRef } from "@expo/ui/swift-ui";
import { BLUR_SETTLE_MS, FormFocusContext, MAX_RELEASES, useFormFocus, useTrackedField, type FormFocus, type TrackedFieldState } from "./formFocus";

let renderer: ReactTestRenderer | undefined;
let focus: FormFocus;
let seen: TrackedFieldState | undefined;

/** What a field in the form reads through context. */
function Field() {
	seen = useTrackedField("name");
	return null;
}

function Form() {
	focus = useFormFocus();
	return <FormFocusContext value={focus}><Field /></FormFocusContext>;
}

/** A stand-in for a SwiftUI field: blur() resolves at once, like the native one, before focus is let go. */
function fakeField(id: string, { reportsBlur = true } = {}) {
	const tracked = focus.field(id);
	const ref = {
		blur: vi.fn(async () => {
			if (reportsBlur) setTimeout(() => tracked.onFocusChange(false), 16);
		}),
	} as unknown as TextFieldRef & { blur: ReturnType<typeof vi.fn> };
	tracked.ref(ref);
	return { tracked, ref };
}

beforeEach(() => {
	vi.useFakeTimers();
	act(() => { renderer = create(<Form />); });
});

afterEach(() => {
	act(() => renderer?.unmount());
	renderer = undefined;
	vi.useRealTimers();
});

describe("useFormFocus", () => {
	it("hands out the same tracking props for the same field", () => {
		expect(focus.field("name")).toBe(focus.field("name"));
		expect(focus.field("name")).not.toBe(focus.field("other"));
	});

	it("submits at once when no field holds focus", async () => {
		const { ref } = fakeField("name");
		const run = vi.fn();
		await focus.submit(run);
		expect(run).toHaveBeenCalledTimes(1);
		expect(ref.blur).not.toHaveBeenCalled();
	});

	it("blurs the focused field and submits only once it reports losing focus", async () => {
		const { tracked, ref } = fakeField("name");
		tracked.onFocusChange(true);
		const run = vi.fn();
		const done = focus.submit(run);
		await vi.advanceTimersByTimeAsync(0);
		expect(ref.blur).toHaveBeenCalledTimes(1);
		// blur() has resolved, but SwiftUI hasn't let go of the field yet.
		expect(run).not.toHaveBeenCalled();
		await vi.advanceTimersByTimeAsync(16);
		await done;
		expect(run).toHaveBeenCalledTimes(1);
	});

	it("goes ahead after BLUR_SETTLE_MS when the field never reports", async () => {
		const { tracked } = fakeField("name", { reportsBlur: false });
		tracked.onFocusChange(true);
		const run = vi.fn();
		const done = focus.submit(run);
		await vi.advanceTimersByTimeAsync(BLUR_SETTLE_MS - 1);
		expect(run).not.toHaveBeenCalled();
		await vi.advanceTimersByTimeAsync(1);
		await done;
		expect(run).toHaveBeenCalledTimes(1);
	});

	it("blurs whichever field holds focus", async () => {
		const name = fakeField("name");
		const search = fakeField("search");
		name.tracked.onFocusChange(true);
		name.tracked.onFocusChange(false);
		search.tracked.onFocusChange(true);
		const done = focus.submit(vi.fn());
		await vi.advanceTimersByTimeAsync(16);
		await done;
		expect(search.ref.blur).toHaveBeenCalledTimes(1);
		expect(name.ref.blur).not.toHaveBeenCalled();
	});

	it("ignores a second submit while the first is still waiting or running", async () => {
		const { tracked } = fakeField("name");
		tracked.onFocusChange(true);
		const run = vi.fn();
		const first = focus.submit(run);
		const second = focus.submit(run);
		await vi.advanceTimersByTimeAsync(16);
		await Promise.all([first, second]);
		expect(run).toHaveBeenCalledTimes(1);
		// Once settled, a later submit runs again.
		await focus.submit(run);
		expect(run).toHaveBeenCalledTimes(2);
	});

	it("drops a submit whose form closed while it waited for the field to let go", async () => {
		const { tracked } = fakeField("name", { reportsBlur: false });
		tracked.onFocusChange(true);
		const run = vi.fn();
		const done = focus.submit(run);
		// Cancel, or the sheet switching face, unmounts the form mid-wait.
		act(() => renderer?.unmount());
		renderer = undefined;
		await vi.advanceTimersByTimeAsync(BLUR_SETTLE_MS);
		await done;
		expect(run).not.toHaveBeenCalled();
	});

	it("forgets a field whose view went away", async () => {
		const { tracked, ref } = fakeField("name");
		tracked.onFocusChange(true);
		tracked.ref(null);
		const run = vi.fn();
		await focus.submit(run);
		expect(ref.blur).not.toHaveBeenCalled();
		expect(run).toHaveBeenCalledTimes(1);
	});

	it("locks the fields once focus is off, until the submit settles", async () => {
		const { tracked } = fakeField("name");
		tracked.onFocusChange(true);
		let finish!: () => void;
		const run = vi.fn(() => new Promise<void>((resolve) => { finish = resolve; }));
		let done!: Promise<void>;
		await act(async () => { done = focus.submit(run); });
		// Still letting go: nothing focused is changed under the user.
		expect(seen?.locked).toBe(false);
		await act(async () => { await vi.advanceTimersByTimeAsync(16); });
		expect(run).toHaveBeenCalledTimes(1);
		expect(focus.locked).toBe(true);
		expect(seen?.locked).toBe(true);
		await act(async () => { finish(); await done; });
		expect(seen?.locked).toBe(false);
	});

	it("unlocks the fields when the submit fails", async () => {
		const run = vi.fn(async () => { throw new Error("refused"); });
		await act(async () => { await focus.submit(run).catch(() => {}); });
		expect(run).toHaveBeenCalledTimes(1);
		expect(seen?.locked).toBe(false);
	});

	it("releases a field focused again while the first one let go", async () => {
		const name = fakeField("name");
		const search = fakeField("search");
		name.tracked.onFocusChange(true);
		const run = vi.fn();
		let done!: Promise<void>;
		await act(async () => { done = focus.submit(run); });
		// The user taps the search field just as the name field lets go.
		search.tracked.onFocusChange(true);
		await act(async () => { await vi.advanceTimersByTimeAsync(32); await done; });
		expect(search.ref.blur).toHaveBeenCalledTimes(1);
		expect(run).toHaveBeenCalledTimes(1);
	});

	it("keeps letting go across two handoffs before locking", async () => {
		const a = fakeField("a", { reportsBlur: false });
		const b = fakeField("b", { reportsBlur: false });
		const c = fakeField("c");
		a.tracked.onFocusChange(true);
		const run = vi.fn(() => {
			// Nothing may still hold focus once the fields lock and the submit runs.
			expect(focus.field("c")).toBe(c.tracked);
			expect(c.ref.blur).toHaveBeenCalledTimes(1);
		});
		let done!: Promise<void>;
		await act(async () => { done = focus.submit(run); });
		expect(a.ref.blur).toHaveBeenCalledTimes(1);
		// B takes focus while A lets go, then C while B lets go.
		await act(async () => { b.tracked.onFocusChange(true); });
		expect(b.ref.blur).toHaveBeenCalledTimes(1);
		await act(async () => { c.tracked.onFocusChange(true); });
		expect(run).not.toHaveBeenCalled();
		await act(async () => { await vi.advanceTimersByTimeAsync(16); await done; });
		expect(c.ref.blur).toHaveBeenCalledTimes(1);
		expect(run).toHaveBeenCalledTimes(1);
	});

	it("goes ahead after MAX_RELEASES handoffs", async () => {
		const ids = Array.from({ length: MAX_RELEASES + 2 }, (_, i) => `f${i}`);
		const fields = ids.map((id) => fakeField(id, { reportsBlur: false }));
		fields[0].tracked.onFocusChange(true);
		// Every blur hands focus straight to the next field, as if the user kept tapping.
		fields.forEach((field, i) => {
			field.ref.blur.mockImplementation(async () => { fields[i + 1]?.tracked.onFocusChange(true); });
		});
		const run = vi.fn();
		await act(async () => { await focus.submit(run); });
		expect(run).toHaveBeenCalledTimes(1);
		expect(fields.filter((field) => field.ref.blur.mock.calls.length).length).toBe(MAX_RELEASES);
	});

	it("tells a field it is unlocked when no submit runs", () => {
		expect(seen).toMatchObject({ locked: false });
		expect(seen?.ref).toBe(focus.field("name").ref);
	});
});

