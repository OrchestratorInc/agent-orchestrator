import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { expect, it, vi } from "vitest";
import { AgentModelCombobox } from "./AgentModelCombobox";

it("compact 模型菜单用准确 ID 区分重名，搜索后仍可选中对应模型", async () => {
	const onChange = vi.fn();
	render(<AgentModelCombobox
		aria-label="Worker model" agentId="omp" value="" compact allowCustom onChange={onChange}
		models={[
			{ id: "codegpt/DeepSeek-V4.1-Flash", label: "DeepSeek V4.1 Flash", provider: "codegpt" },
			{ id: "codegpt/cn:deepseek-v4.1-flash", label: "DeepSeek V4.1 Flash", provider: "codegpt" },
			{ id: "codegpt/unique", label: "Unique model", provider: "codegpt" },
		]}
	/>);
	await userEvent.click(screen.getByRole("button", { name: "Worker model" }));
	expect(screen.getByRole("menuitem", { name: "DeepSeek V4.1 Flash codegpt/DeepSeek-V4.1-Flash" })).toBeInTheDocument();
	expect(screen.getByRole("menuitem", { name: "DeepSeek V4.1 Flash codegpt/cn:deepseek-v4.1-flash" })).toBeInTheDocument();
	expect(screen.getByRole("menuitem", { name: "Unique model" })).toBeInTheDocument();
	expect(screen.queryByText("codegpt/unique")).not.toBeInTheDocument();

	await userEvent.type(screen.getByRole("searchbox"), "cn:deepseek");
	await userEvent.click(screen.getByRole("menuitem", { name: "DeepSeek V4.1 Flash codegpt/cn:deepseek-v4.1-flash" }));
	expect(onChange).toHaveBeenCalledExactlyOnceWith("codegpt/cn:deepseek-v4.1-flash");
});
