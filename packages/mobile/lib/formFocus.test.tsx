import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { TextFieldRef } from "@expo/ui/swift-ui";
import { BLUR_SETTLE_MS, useFormFocus, type FormFocus } from "./formFocus";

let renderer: ReactTestRenderer | undefined;
let focus: FormFocus;

function Form() {
	focus = useFormFocus();
	return null;
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
});
