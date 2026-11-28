import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

const apiMocks = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }));

vi.mock("../lib/api-client", () => ({
	apiClient: { GET: apiMocks.get, POST: apiMocks.post },
	apiErrorMessage: vi.fn((_error: unknown, fallback: string) => fallback),
}));

import { checkRequirementsAgain, InstallDependencyDialog, isActiveInstallJob } from "./InstallDependencyDialog";

describe("isActiveInstallJob", () => {
	it.each(["running", "installing", "verifying"])("treats %s as active", (status) => {
		expect(isActiveInstallJob({ target: "codex", status } as never)).toBe(true);
	});

	it.each(["succeeded", "failed", "unsupported", "interrupted"])("treats %s as terminal", (status) => {
		expect(isActiveInstallJob({ target: "codex", status } as never)).toBe(false);
	});
});

describe("checkRequirementsAgain", () => {
	it("forces an agent refresh before refetching startup requirements", async () => {
		const order: string[] = [];
		apiMocks.post.mockImplementation(async () => {
			order.push("refresh");
			return { data: {}, error: undefined };
		});
		const refetch = vi.fn(async () => {
			order.push("refetch");
		});

		await checkRequirementsAgain(refetch);

		expect(apiMocks.post).toHaveBeenCalledWith("/api/v1/agents/refresh");
		expect(refetch).toHaveBeenCalledOnce();
		expect(order).toEqual(["refresh", "refetch"]);
	});
});

describe("InstallDependencyDialog", () => {
	it("uses the OpenCode 2 installer preview notice and legacy install endpoint", async () => {
		apiMocks.get.mockImplementation(async (path: string) => {
			if (path === "/api/v1/system/install/{target}") {
				return {
					data: { target: "opencode-v2", status: "idle", notice: "The daemon says this replaces OpenCode 1." },
					error: undefined,
				};
			}
			return { data: undefined, error: undefined };
		});
		apiMocks.post.mockResolvedValue({ data: { target: "opencode-v2", status: "installing" }, error: undefined });

		render(
			<InstallDependencyDialog
				requirements={[{ id: "harness", label: "Agent harness", required: true, satisfied: false }] as never}
				onRefetchRequirements={vi.fn()}
			/>,
		);

		const notice = await screen.findByText("The daemon says this replaces OpenCode 1.");
		expect(notice).toHaveAttribute("role", "status");
		// Every agent row carries its own Install; the notice sits under OpenCode 2's.
		const row = screen.getByText("OpenCode 2").closest(".settings-row-bar")?.parentElement as HTMLElement;
		expect(row).toContainElement(notice);
		await userEvent.click(within(row).getByRole("button", { name: "Install" }));

		expect(apiMocks.post).toHaveBeenCalledWith("/api/v1/system/install/{target}", {
			params: { path: { target: "opencode-v2" } },
		});
	});

	it("shows the command a running install is executing", async () => {
		apiMocks.get.mockImplementation(async (path: string) => {
			if (path === "/api/v1/system/install/{target}") {
				return { data: { target: "tmux", status: "running", command: "brew install tmux" }, error: undefined };
			}
			return { data: undefined, error: undefined };
		});
		render(
			<InstallDependencyDialog
				requirements={[{ id: "tmux", label: "tmux", required: true, satisfied: false }] as never}
				onRefetchRequirements={vi.fn()}
			/>,
		);

		// The dialog checks each install's plan as it opens; one already running shows what it runs.
		expect(await screen.findByText(/brew install tmux/)).toBeInTheDocument();
	});

	it("shows a row busy from the click, so a second click can't start the install again", async () => {
		apiMocks.get.mockImplementation(async (path: string) => {
			if (path === "/api/v1/system/install/{target}") return { data: { target: "tmux", status: "idle" }, error: undefined };
			return { data: undefined, error: undefined };
		});
		let answer: (value: unknown) => void = () => undefined;
		apiMocks.post.mockReset().mockReturnValue(new Promise((resolve) => { answer = resolve; }));
		render(
			<InstallDependencyDialog
				requirements={[{ id: "tmux", label: "tmux", required: true, satisfied: false }] as never}
				onRefetchRequirements={vi.fn()}
			/>,
		);

		await userEvent.click(await screen.findByRole("button", { name: "Install" }));
		// The daemon hasn't answered: the row says it's installing, with no Install to press again.
		expect(screen.getByText("Installing…")).toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "Install" })).toBeNull();
		expect(apiMocks.post).toHaveBeenCalledOnce();
		answer({ data: { target: "tmux", status: "succeeded" }, error: undefined });
	});

	it("surfaces a failed Check again refresh", async () => {
		apiMocks.post.mockResolvedValue({ data: undefined, error: { message: "daemon unavailable" } });
		const refetch = vi.fn();

		render(<InstallDependencyDialog requirements={[]} onRefetchRequirements={refetch} />);
		await userEvent.click(screen.getByRole("button", { name: "Check again" }));

		expect(await screen.findByRole("alert")).toHaveTextContent("Could not refresh agent inventory.");
		expect(refetch).not.toHaveBeenCalled();
	});
});
