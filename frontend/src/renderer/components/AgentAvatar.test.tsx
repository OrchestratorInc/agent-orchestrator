import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { AgentAvatar } from "./AgentAvatar";
import reasonixLogo from "../assets/agents/reasonix.svg";

describe("AgentAvatar", () => {
	it("renders the Reasonix brand asset", () => {
		render(<AgentAvatar provider="reasonix" />);
		expect(screen.getByRole("img", { name: "reasonix" })).toHaveAttribute("src", reasonixLogo);
	});

	it("keeps the initial fallback for unknown agents", () => {
		render(<AgentAvatar provider="example-agent" />);
		expect(screen.getByRole("img", { name: "example-agent" })).toHaveTextContent("E");
	});

	it("renders the Prime Agent brand asset", () => {
		render(<AgentAvatar provider="prime-agent" />);

		expect(screen.getByRole("img", { name: "prime-agent" })).toHaveAttribute(
			"src",
			expect.stringContaining("prime-agent.png"),
		);
	});

	it("renders the OMP brand asset", () => {
		render(<AgentAvatar provider="omp" />);

		expect(screen.getByRole("img", { name: "omp" })).toHaveAttribute("src", expect.stringContaining("omp.png"));
	});
});
