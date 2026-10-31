import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { AgentAvatar } from "./AgentAvatar";
import fxDarkLogo from "../assets/agents/fx-dark.svg";
import fxLightLogo from "../assets/agents/fx-light.svg";
import geminiLogo from "../assets/agents/gemini.svg";
import memcodeLogo from "../assets/agents/memcode.png";
import unrealAgentDarkLogo from "../assets/agents/unreal-agent-dark.png";
import unrealAgentLightLogo from "../assets/agents/unreal-agent-light.png";

function themeSources(name: string) {
	const mark = screen.getByRole("img", { name });
	return Array.from(mark.querySelectorAll("img"), (img) => img.getAttribute("src"));
}

describe("AgentAvatar", () => {
	it("renders the bundled Memcode mark", () => {
		render(<AgentAvatar provider="memcode" />);
		expect(screen.getByRole("img", { name: "memcode" })).toHaveAttribute("src", memcodeLogo);
	});
	it("renders the official fx light and dark marks under one accessible name", () => {
		render(<AgentAvatar provider="fx" />);

		expect(themeSources("fx")).toEqual([fxLightLogo, fxDarkLogo]);
	});

	it("renders a single mark when the brand reads on both themes", () => {
		render(<AgentAvatar provider="gemini" />);

		expect(screen.getByRole("img", { name: "gemini" })).toHaveAttribute("src", geminiLogo);
	});

	it("keeps raster marks as a theme pair when no vector exists", () => {
		render(<AgentAvatar provider="unreal-agent" />);

		expect(themeSources("unreal-agent")).toEqual([unrealAgentLightLogo, unrealAgentDarkLogo]);
	});

	it("gives every harness a theme-safe brand mark", () => {
		const providers = ["prime-agent", "omp", "codewhale", "deepseek-harness", "mimo-code", "openhands", "amp"];
		render(
			<>
				{providers.map((provider) => (
					<AgentAvatar key={provider} provider={provider} />
				))}
			</>,
		);

		for (const provider of providers) {
			const mark = screen.getByRole("img", { name: provider });
			expect(mark.tagName === "IMG" || mark.querySelectorAll("img").length === 2).toBe(true);
		}
	});

	it("reuses the OpenCode brand marks for OpenCode 2", () => {
		render(
			<>
				<AgentAvatar provider="opencode" />
				<AgentAvatar provider="opencode-v2" />
			</>,
		);

		expect(themeSources("opencode-v2")).toEqual(themeSources("opencode"));
	});
	it("renders the official Hermes Agent icon", () => {
		render(<AgentAvatar provider="hermes" />);
		expect(screen.getByRole("img", { name: "hermes" })).toHaveAttribute("src", expect.stringContaining("hermes.png"));
	});
});
