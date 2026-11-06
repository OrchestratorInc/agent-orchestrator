import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render as renderView, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { aoBridge } from "../lib/bridge";
import type { CloudCpSessionLive, CloudCpShareLink } from "../lib/cloud-cp";
import type { SessionPerson } from "../lib/session-people";
import { useUiStore } from "../stores/ui-store";
import { SessionPeopleControl, SessionPeopleStack } from "./SessionPeople";

const mocks = vi.hoisted(() => ({
	live: undefined as CloudCpSessionLive | undefined,
	dialog: vi.fn(),
	teams: true,
	links: [] as CloudCpShareLink[],
	createShareLink: vi.fn(),
}));
vi.mock("../hooks/useTeamsEnabled", () => ({ useTeamsEnabled: () => mocks.teams }));
vi.mock("../hooks/useCloudCp", () => ({
	useCloudCp: () => ({
		userId: "me",
		ready: true,
		baseUrl: "https://cp.test",
		client: { listShareLinks: async () => ({ links: mocks.links }), createShareLink: mocks.createShareLink, revokeShareLink: vi.fn() },
	}),
}));
vi.mock("../hooks/useCloudSessionLive", () => ({ useCloudSessionLive: () => mocks.live }));
vi.mock("./ShareSessionDialog", () => ({
	ShareSessionDialog: (props: { open: boolean; people?: boolean }) => {
		mocks.dialog(props.open);
		return props.open ? <div data-testid="share-dialog" data-people={String(props.people)} /> : null;
	},
}));

function render(ui: React.ReactElement) {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	return renderView(<QueryClientProvider client={client}>{ui}</QueryClientProvider>);
}

const person = (userId: string, name: string, state: SessionPerson["state"], present = true): SessionPerson => ({ userId, name, present, state });

function live(viewers: { userId: string; name: string; avatarUrl?: string }[], holder?: string): CloudCpSessionLive {
	return {
		type: "session.live",
		viewers: viewers.map((viewer) => ({ ...viewer, role: "editor" as const })),
		composer: { holder: holder ? { userId: holder, name: holder } : null, epoch: 1, waitlist: [] },
		canTakeOver: false, you: "me", chatSettings: {}, canConfigure: false,
	};
}

describe("SessionPeopleStack", () => {
	it("shows at most two circles, the writer first and ringed, and folds the rest into +N", () => {
		const { container } = render(
			<SessionPeopleStack people={[
				person("a", "Vaibhaav Tiwari", "idle"),
				person("b", "Vaanya Gupta", "working"),
				person("c", "Carol", "idle"),
				person("d", "Dan", "waiting"),
			]} />,
		);
		const circles = [...container.querySelectorAll("[data-session-people] > span")].map((circle) => circle.textContent);
		expect(circles).toEqual(["VG", "D", "+2"]);
		expect(container.querySelector("[data-person-state=working]")?.className).toContain("ring-status-working");
	});

	it("shows sign-in photos and falls back to initials when missing or broken", () => {
		const writer = { ...person("a", "Ada Lovelace", "working"), avatarUrl: "https://lh3.googleusercontent.com/ada" };
		const { container, rerender } = render(<SessionPeopleStack people={[writer, person("b", "Bob", "idle")]} />);
		const photo = container.querySelector("img")!;
		expect(photo.getAttribute("src")).toBe(writer.avatarUrl);
		expect(photo.getAttribute("referrerpolicy")).toBe("no-referrer");
		expect(container.querySelector("[data-person-state=working]")?.className).toContain("ring-status-working");
		expect(container.querySelector("[data-person-state=idle]")?.textContent).toBe("B");
		fireEvent.error(photo);
		expect(container.querySelector("img")).toBeNull();
		expect(container.querySelector("[data-person-state=working]")?.textContent).toBe("AL");
		rerender(<SessionPeopleStack people={[{ ...writer, avatarUrl: "https://lh3.googleusercontent.com/updated" }]} />);
		expect(container.querySelector("img")?.getAttribute("src")).toBe("https://lh3.googleusercontent.com/updated");
	});

	it("lists everyone and what they are doing on hover", async () => {
		render(<SessionPeopleStack people={[person("me", "Mohak", "idle"), person("b", "Bob", "working"), person("c", "Carol", "waiting")]} me="me" />);
		fireEvent.focus(screen.getByRole("group"));
		await waitFor(() => expect(document.querySelector("[data-session-people-list]")).not.toBeNull());
		const rows = [...document.querySelectorAll("[data-session-people-list] li")].map((row) => row.textContent);
		expect(rows).toEqual(["Bobworking", "Carolin line", "Mohak (you)idle"]);
	});
});

describe("SessionPeopleControl", () => {
	beforeEach(() => {
		mocks.live = undefined;
		mocks.dialog.mockClear();
		mocks.teams = true;
		mocks.links = [];
		mocks.createShareLink.mockReset();
		useUiStore.setState({ globalToasts: [] });
	});
	afterEach(() => vi.restoreAllMocks());

	it("in a build without Teams, Share opens the dialog with links only", () => {
		mocks.teams = false;
		mocks.live = live([{ userId: "me", name: "Mohak" }]);
		render(<SessionPeopleControl orgId="org" projectId="project" sessionId="session" canShare />);
		fireEvent.click(screen.getByRole("button", { name: "Share" }));
		expect(screen.getByTestId("share-dialog").dataset.people).toBe("false");
	});

	it("in a build with Teams, Share opens the people and teams dialog", () => {
		mocks.live = live([{ userId: "me", name: "Mohak" }]);
		render(<SessionPeopleControl orgId="org" projectId="project" sessionId="session" canShare />);
		fireEvent.click(screen.getByRole("button", { name: "Share" }));
		expect(screen.getByTestId("share-dialog").dataset.people).toBe("true");
	});

	it("copies the link from the menu beside Share, naming who it lets in", async () => {
		const id = "0c927f48-7214-4fb1-833a-7c1af5b7bf80";
		const url = "https://orchestrator.inc/s/k7Hq2x9PmB4wR5tY8uI0oP";
		mocks.links = [{
			id, orgId: "org", projectId: "project", sessionId: "session", role: "viewer", status: "active", accessScope: "anyone",
			recipients: [], interaction: "view", modeCap: "read-only", deniedCommands: [], createdAt: "2026-11-01T00:00:00Z", updatedAt: "2026-11-01T00:00:00Z",
		}];
		vi.spyOn(aoBridge.sessionShareUrls, "recall").mockResolvedValue({ [id]: url });
		const writeText = vi.spyOn(aoBridge.clipboard, "writeText").mockResolvedValue(undefined);
		mocks.live = live([{ userId: "me", name: "Mohak" }]);
		render(<SessionPeopleControl orgId="org" projectId="project" sessionId="session" canShare />);
		await userEvent.click(screen.getByRole("button", { name: "More sharing options" }));
		const item = await screen.findByRole("menuitem", { name: "Copy link" });
		await waitFor(() => expect(item).not.toHaveAttribute("data-disabled"));
		await userEvent.click(item);
		await waitFor(() => expect(writeText).toHaveBeenCalledWith(url));
		const toast = useUiStore.getState().globalToasts.at(-1);
		expect(toast?.title).toBe("Link copied");
		expect(toast?.body).toContain("Anyone signed in to Orchestrator.inc with the link can watch this session");
		expect(toast?.action?.label).toBe("Manage");
	});

	it("opens the Share dialog instead of making a link when this device doesn't have it", async () => {
		mocks.links = [{
			id: "0c927f48-7214-4fb1-833a-7c1af5b7bf80", orgId: "org", projectId: "project", sessionId: "session", role: "editor", status: "active", accessScope: "anyone",
			recipients: [], interaction: "interact", modeCap: "standard", deniedCommands: [], createdAt: "2026-11-01T00:00:00Z", updatedAt: "2026-11-01T00:00:00Z",
		}];
		const writeText = vi.spyOn(aoBridge.clipboard, "writeText").mockResolvedValue(undefined);
		mocks.live = live([{ userId: "me", name: "Mohak" }]);
		render(<SessionPeopleControl orgId="org" projectId="project" sessionId="session" canShare />);
		await userEvent.click(screen.getByRole("button", { name: "More sharing options" }));
		const item = await screen.findByRole("menuitem", { name: /Copy new link…/ });
		await waitFor(() => expect(item).not.toHaveAttribute("data-disabled"));
		expect(item).toHaveTextContent("Made on another device");
		await userEvent.click(item);
		expect(await screen.findByTestId("share-dialog")).toBeTruthy();
		expect(mocks.createShareLink).not.toHaveBeenCalled();
		expect(writeText).not.toHaveBeenCalled();
	});

	it("says link access is off in the menu instead of copying", async () => {
		mocks.live = live([{ userId: "me", name: "Mohak" }]);
		render(<SessionPeopleControl orgId="org" projectId="project" sessionId="session" canShare />);
		await userEvent.click(screen.getByRole("button", { name: "More sharing options" }));
		await screen.findByText("Link access is off");
		expect(screen.getByRole("menuitem", { name: /Copy link/ })).toHaveAttribute("data-disabled");
	});

	it("shows the owner a Share button while nobody else is here", () => {
		mocks.live = live([{ userId: "me", name: "Mohak" }]);
		render(<SessionPeopleControl orgId="org" projectId="project" sessionId="session" canShare />);
		fireEvent.click(screen.getByRole("button", { name: "Share" }));
		expect(screen.getByTestId("share-dialog")).toBeTruthy();
	});

	it("shows who else is here instead, and the circles open the share dialog", () => {
		mocks.live = live([{ userId: "me", name: "Mohak" }, { userId: "b", name: "Bob", avatarUrl: "https://lh3.googleusercontent.com/bob" }], "b");
		const { container } = render(<SessionPeopleControl orgId="org" projectId="project" sessionId="session" canShare />);
		expect(screen.queryByRole("button", { name: "Share" })).toBeNull();
		expect(container.querySelector("[data-session-people] img")?.getAttribute("src")).toBe("https://lh3.googleusercontent.com/bob");
		fireEvent.click(container.querySelector("[data-session-people]")!);
		expect(screen.getByTestId("share-dialog")).toBeTruthy();
	});

	it("shows a guest only who else is here, never a way to share", () => {
		mocks.live = live([{ userId: "me", name: "Mohak" }]);
		const alone = render(<SessionPeopleControl orgId="org" projectId="project" sessionId="session" canShare={false} />);
		expect(alone.container.textContent).toBe("");
		alone.unmount();

		mocks.live = live([{ userId: "me", name: "Mohak" }, { userId: "b", name: "Bob" }]);
		const { container } = render(<SessionPeopleControl orgId="org" projectId="project" sessionId="session" canShare={false} />);
		expect(container.querySelector("button[data-session-people]")).toBeNull();
		expect(container.querySelector("[data-session-people]")?.textContent).toBe("B");
		expect(mocks.dialog).not.toHaveBeenCalled();
	});
});
