import { act, fireEvent, render, screen } from "@testing-library/react";
import { useEffect, useState } from "react";
import { describe, expect, it } from "vitest";
import { queryShown } from "../lib/dom-selectors";
import { KeepRecentAlive } from "./KeepRecentAlive";

const effects: string[] = [];

function Chat({ id }: { id: string }) {
	const [draft, setDraft] = useState("");
	useEffect(() => {
		effects.push(`mount ${id}`);
		return () => void effects.push(`unmount ${id}`);
	}, [id]);
	return (
		<div data-testid={`chat-${id}`}>
			<input aria-label="Message the agent" data-chat={id} value={draft} onChange={(event) => setDraft(event.target.value)} />
		</div>
	);
}

function Host({ id, max, show = true }: { id: string; max?: number; show?: boolean }) {
	return (
		<KeepRecentAlive activeKey={id} max={max} show={show}>
			{show ? <Chat key={id} id={id} /> : null}
		</KeepRecentAlive>
	);
}

describe("KeepRecentAlive", () => {
	it("keeps a chat's state while another is shown, and runs no effects while hidden", () => {
		effects.length = 0;
		const { rerender } = render(<Host id="a" />);
		fireEvent.change(screen.getByLabelText("Message the agent"), { target: { value: "half a thought" } });

		act(() => rerender(<Host id="b" />));
		expect(screen.getByTestId("chat-a")).toHaveStyle({ display: "none" });
		expect(screen.getByTestId("chat-b")).not.toHaveStyle({ display: "none" });
		expect(effects).toEqual(["mount a", "unmount a", "mount b"]);

		act(() => rerender(<Host id="a" />));
		expect(screen.getByTestId("chat-a")).not.toHaveStyle({ display: "none" });
		expect(screen.getByTestId("chat-b")).toHaveStyle({ display: "none" });
		expect((queryShown<HTMLInputElement>('[aria-label="Message the agent"]'))?.value).toBe("half a thought");
	});

	it("drops the least recently shown chat past the limit and never reorders the rest", () => {
		const { container, rerender } = render(<Host id="a" max={2} />);
		act(() => rerender(<Host id="b" max={2} />));
		act(() => rerender(<Host id="a" max={2} />));
		act(() => rerender(<Host id="c" max={2} />));

		const order = () => Array.from(container.querySelectorAll("[data-testid^='chat-']"), (element) => element.getAttribute("data-testid"));
		expect(order()).toEqual(["chat-a", "chat-c"]);
		act(() => rerender(<Host id="a" max={2} />));
		expect(order()).toEqual(["chat-a", "chat-c"]);
	});

	it("drops a key's own chat while it shows something else, and keeps the others hidden", () => {
		const { rerender } = render(<Host id="a" />);
		fireEvent.change(screen.getByLabelText("Message the agent"), { target: { value: "kept" } });
		act(() => rerender(<Host id="b" />));
		act(() => rerender(<Host id="b" show={false} />));
		expect(screen.queryByTestId("chat-b")).not.toBeInTheDocument();
		expect(screen.getByTestId("chat-a")).toHaveStyle({ display: "none" });

		act(() => rerender(<Host id="a" />));
		expect(queryShown<HTMLInputElement>('[aria-label="Message the agent"]')?.value).toBe("kept");
	});

	it("lets document-wide lookups find the shown composer, not a hidden one", () => {
		const { rerender } = render(<Host id="a" />);
		act(() => rerender(<Host id="b" />));
		expect(document.querySelector("[data-chat]")?.getAttribute("data-chat")).toBe("a");
		expect(queryShown("[data-chat]")?.getAttribute("data-chat")).toBe("b");
	});
});
