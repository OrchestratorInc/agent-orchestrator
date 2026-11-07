import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { CloudCpError } from "../lib/cloud-cp/errors";
import type { CloudCpSessionFollower } from "../lib/cloud-cp/types";
import { recordCloudInboxSupport, resetCloudInboxSupport } from "../lib/cloud-notifications";
import { SessionFollowMenu } from "./SessionFollowMenu";

const { getSessionFollow, setSessionFollow, listSessionFollowers } = vi.hoisted(() => ({
	getSessionFollow: vi.fn(),
	setSessionFollow: vi.fn(),
	listSessionFollowers: vi.fn(),
}));

vi.mock("../hooks/useCloudCp", () => ({
	useCloudCp: () => ({
		client: { getSessionFollow, setSessionFollow, listSessionFollowers },
		ready: true,
		baseUrl: "https://cloud.test",
		userId: "me",
	}),
}));

const mine = (level: CloudCpSessionFollower["level"]): CloudCpSessionFollower => ({ userId: "me", level, reasons: level ? ["creator", "prompted"] : [], manual: false });

function renderMenu() {
	return render(
		<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
			<SessionFollowMenu orgId="org-1" sessionId="session-1" />
		</QueryClientProvider>,
	);
}

beforeEach(() => {
	resetCloudInboxSupport();
	recordCloudInboxSupport("https://cloud.test", "supported");
	getSessionFollow.mockReset().mockResolvedValue(mine("participating"));
	setSessionFollow.mockReset().mockImplementation(async (_org: string, _session: string, level: CloudCpSessionFollower["level"]) => ({ ...mine(level), manual: true }));
	listSessionFollowers.mockReset().mockResolvedValue({
		items: [
			mine("participating"),
			{ userId: "ben", name: "Ben Ortiz", level: "participating", reasons: ["mentioned"], manual: false },
		],
	});
});

afterEach(() => {
	resetCloudInboxSupport();
});

describe("SessionFollowMenu", () => {
	it("shows how the person follows the session and who else is notified", async () => {
		renderMenu();
		const trigger = await screen.findByRole("button", { name: "Notifications for this session: Following" });
		expect(getSessionFollow).toHaveBeenCalledWith("org-1", "session-1");
		expect(listSessionFollowers).not.toHaveBeenCalled();

		await userEvent.click(trigger);
		const menu = await screen.findByRole("menu");
		const levels = within(menu).getAllByRole("menuitemradio");
		expect(levels.map((item) => item.textContent)).toEqual([
			"Needs you onlyApprovals, mentions, handoffs",
			"ParticipatingPlus PR, CI and failures",
			"All activityPlus every finished turn",
			"MutedUntil someone mentions you or hands it to you",
		]);
		expect(within(menu).getByRole("menuitemradio", { name: /^Participating/ })).toHaveAttribute("aria-checked", "true");
		expect(await within(menu).findByText("Who gets notified · 2")).toBeInTheDocument();
		const people = within(menu).getAllByRole("listitem").map((item) => item.textContent);
		expect(people).toEqual(["YouOwner, Prompted", "Ben OrtizMentioned"]);
	});

	it("sets the person's level", async () => {
		renderMenu();
		await userEvent.click(await screen.findByRole("button", { name: /Notifications for this session/ }));
		await userEvent.click(await screen.findByRole("menuitemradio", { name: /^Muted/ }));

		expect(setSessionFollow).toHaveBeenCalledWith("org-1", "session-1", "muted");
		expect(await screen.findByRole("button", { name: "Notifications for this session: Muted" })).toBeInTheDocument();
	});

	it("offers Follow to someone who doesn't follow the session", async () => {
		getSessionFollow.mockResolvedValue(mine(""));
		renderMenu();
		expect(await screen.findByRole("button", { name: "Notifications for this session: Follow" })).toBeInTheDocument();
	});

	it("is hidden on a control plane from before team notifications", async () => {
		resetCloudInboxSupport();
		recordCloudInboxSupport("https://cloud.test", "unsupported");
		const { container } = renderMenu();
		await waitFor(() => expect(getSessionFollow).not.toHaveBeenCalled());
		expect(container).toBeEmptyDOMElement();
	});

	it("waits for the control plane to say it has team notifications", () => {
		resetCloudInboxSupport();
		const { container } = renderMenu();
		expect(getSessionFollow).not.toHaveBeenCalled();
		expect(container).toBeEmptyDOMElement();
	});

	it("stays hidden when the follow route is missing", async () => {
		getSessionFollow.mockRejectedValue(new CloudCpError("404 page not found", { status: 404 }));
		const { container } = renderMenu();
		await waitFor(() => expect(getSessionFollow).toHaveBeenCalledTimes(1));
		expect(container).toBeEmptyDOMElement();
	});
});
