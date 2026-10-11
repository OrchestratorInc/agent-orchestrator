import { render } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { useRestoreFocusOnClose } from "./useRestoreFocusOnClose";

let restore: (event: Event) => void = () => undefined;

function Harness({ open }: { open: boolean }) {
	restore = useRestoreFocusOnClose(open);
	return null;
}

function closeEvent() {
	return new Event("focus", { cancelable: true });
}

describe("useRestoreFocusOnClose", () => {
	it("returns focus to the element focused when the dialog opened", () => {
		const trigger = document.createElement("button");
		document.body.append(trigger);
		trigger.focus();
		const { rerender } = render(<Harness open={false} />);
		rerender(<Harness open />);
		const other = document.createElement("input");
		document.body.append(other);
		other.focus();
		const event = closeEvent();
		restore(event);
		expect(event.defaultPrevented).toBe(true);
		expect(document.activeElement).toBe(trigger);
		trigger.remove();
		other.remove();
	});

	it("falls back to the chat composer when the opener is gone", () => {
		const trigger = document.createElement("button");
		const composer = document.createElement("textarea");
		composer.setAttribute("aria-label", "Message the agent");
		document.body.append(trigger, composer);
		trigger.focus();
		const { rerender } = render(<Harness open={false} />);
		rerender(<Harness open />);
		trigger.remove();
		const event = closeEvent();
		restore(event);
		expect(event.defaultPrevented).toBe(true);
		expect(document.activeElement).toBe(composer);
		composer.remove();
	});

	it("leaves Radix default focus handling alone when there is nowhere to go", () => {
		const { rerender } = render(<Harness open={false} />);
		rerender(<Harness open />);
		const event = closeEvent();
		restore(event);
		expect(event.defaultPrevented).toBe(false);
	});
});
