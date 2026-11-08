import { render, waitFor } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { AgentOutline } from "./AgentOutline";

describe("AgentOutline", () => {
	it("traces an agent's logo as unfilled shapes in one stroke", async () => {
		const { container } = render(<AgentOutline agent="claude-code" />);
		await waitFor(() => expect(container.querySelector("svg:not(.lucide) path")).not.toBeNull());
		const svg = container.querySelector("svg:not(.lucide)")!;
		expect(svg.getAttribute("fill")).toBe("none");
		expect(svg.getAttribute("stroke")).toBe("currentColor");
		for (const shape of Array.from(svg.querySelectorAll("path"))) {
			expect(shape.getAttribute("fill")).toBeNull();
			expect(shape.getAttribute("vector-effect")).toBe("non-scaling-stroke");
		}
	});

	it("drops a logo's gradients and keeps only its shapes", async () => {
		const { container } = render(<AgentOutline agent="codex" />);
		await waitFor(() => expect(container.querySelector("svg:not(.lucide) path")).not.toBeNull());
		expect(container.querySelector("linearGradient, defs, image")).toBeNull();
	});

	it("uses a light-background mark for agents that ship a pair", async () => {
		const { container } = render(<AgentOutline agent="opencode-v2" />);
		await waitFor(() => expect(container.querySelector("svg:not(.lucide) path")).not.toBeNull());
	});

	it("falls back to a generic outline icon for an agent without a mark", async () => {
		const { container } = render(<AgentOutline agent="no-such-agent" />);
		await waitFor(() => expect(container.querySelector("svg.lucide")).not.toBeNull());
	});
});
