import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { useUiStore } from "../stores/ui-store";
import { CloudSessionTabMenu } from "./CloudSessionTabMenu";

const support = vi.hoisted(() => ({ current: "supported" as "unknown" | "supported" | "unsupported" }));
vi.mock("../hooks/useTeamCollab", () => ({ useTeamCollabSupport: () => support.current }));

beforeEach(() => {
	localStorage.clear();
	support.current = "supported";
	useUiStore.setState({ hideAsides: false });
});

describe("CloudSessionTabMenu", () => {
	it("has one item, Hide asides, remembered on this device", async () => {
		const user = userEvent.setup();
		render(<CloudSessionTabMenu />);
		await user.click(screen.getByRole("button", { name: "Session actions" }));
		const item = await screen.findByRole("menuitemcheckbox", { name: "Hide asides" });
		expect(item).toHaveAttribute("aria-checked", "false");
		expect(screen.getAllByRole("menuitemcheckbox")).toHaveLength(1);
		await user.click(item);
		expect(useUiStore.getState().hideAsides).toBe(true);
		expect(localStorage.getItem("ao.hideAsides")).toBe("true");
		await user.click(screen.getByRole("button", { name: "Session actions" }));
		expect(await screen.findByRole("menuitemcheckbox", { name: "Hide asides" })).toHaveAttribute("aria-checked", "true");
	});

	it.each(["unknown", "unsupported"] as const)("shows nothing while asides are %s", (state) => {
		support.current = state;
		const { container } = render(<CloudSessionTabMenu />);
		expect(container).toBeEmptyDOMElement();
	});
});
