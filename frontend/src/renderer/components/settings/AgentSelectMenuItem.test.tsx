import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { AgentSelectMenuItem } from "./AgentSelectMenuItem";

const explanation = "Couldn’t check sign-in.";

function renderUnknownAgent() {
	return render(<div role="menuitem" tabIndex={0}>
		<AgentSelectMenuItem label="Kiro" selected={false} statusIndicator="auth-unknown" />
	</div>);
}

describe("AgentSelectMenuItem", () => {
	it("shows a neutral status icon instead of unknown-auth text", () => {
		renderUnknownAgent();
		expect(screen.queryByText("Auth unknown")).not.toBeInTheDocument();
		expect(screen.getByText("Kiro")).toHaveClass("text-control", "text-foreground");
		expect(screen.getByRole("img", { name: explanation })).toHaveClass("text-settings-muted");
		expect(screen.getByRole("menuitem")).not.toHaveAttribute("aria-disabled", "true");
	});

	it("explains the status when keyboard focus reaches the agent", async () => {
		renderUnknownAgent();
		act(() => screen.getByRole("menuitem").focus());
		expect(await screen.findByRole("tooltip")).toHaveTextContent(explanation);
		act(() => screen.getByRole("menuitem").blur());
		await waitFor(() => expect(screen.queryByRole("tooltip")).not.toBeInTheDocument());
	});

	it("explains the status when hovering over the icon", async () => {
		renderUnknownAgent();
		fireEvent.pointerMove(screen.getByRole("img"), { pointerType: "mouse" });
		expect(await screen.findByRole("tooltip")).toHaveTextContent(explanation);
	});

	it("preserves definite authentication failure text", () => {
		render(<AgentSelectMenuItem label="Codex" selected={false} status="Needs auth" statusTone="warning" disabled />);
		expect(screen.getByText("Needs auth")).toHaveClass("text-warning");
		expect(screen.queryByRole("img")).not.toBeInTheDocument();
	});
});
