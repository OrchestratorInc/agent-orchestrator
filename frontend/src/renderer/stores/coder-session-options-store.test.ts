import { describe, expect, it } from "vitest";
import { buildCoderRequestOptions, useCoderSessionOptionsStore } from "./coder-session-options-store";

const base = {
	templateId: "",
	supportedParams: [] as string[],
	size: "medium" as const,
	startupScript: "",
	extraRepos: [],
};

describe("buildCoderRequestOptions", () => {
	it("returns undefined for the default template with no extra repos", () => {
		expect(buildCoderRequestOptions(base)).toBeUndefined();
	});

	it("sends only the template id when the template declares no parameters", () => {
		// A paramless template (e.g. a pre-existing infra template) must never
		// carry size/startup, or Coder rejects the build.
		const coder = buildCoderRequestOptions({
			...base,
			templateId: "tpl-1",
			supportedParams: [],
			size: "large",
			startupScript: "make dev",
		});
		expect(coder).toEqual({ templateId: "tpl-1" });
	});

	it("sends size and startup only when the template declares them", () => {
		const coder = buildCoderRequestOptions({
			...base,
			templateId: "tpl-2",
			supportedParams: ["size", "startup_script"],
			size: "large",
			startupScript: "make dev",
		});
		expect(coder).toEqual({ templateId: "tpl-2", size: "large", startupScript: "make dev" });
	});

	it("sends size but not startup when only size is declared", () => {
		const coder = buildCoderRequestOptions({
			...base,
			templateId: "tpl-3",
			supportedParams: ["size"],
			size: "small",
			startupScript: "make dev",
		});
		expect(coder).toEqual({ templateId: "tpl-3", size: "small" });
	});

	it("omits an empty startup script even when declared", () => {
		const coder = buildCoderRequestOptions({
			...base,
			templateId: "tpl-4",
			supportedParams: ["size", "startup_script"],
			size: "medium",
			startupScript: "   ",
		});
		expect(coder).toEqual({ templateId: "tpl-4", size: "medium" });
	});

	it("carries extra repos independently of the template", () => {
		const coder = buildCoderRequestOptions({
			...base,
			templateId: "",
			extraRepos: [{ url: "https://github.com/owner/repo", branch: "main" }],
		});
		expect(coder).toEqual({ extraRepos: [{ url: "https://github.com/owner/repo", branch: "main" }] });
	});

	it("sends a workspace name prefix only when one is set", () => {
		expect(buildCoderRequestOptions({ ...base, templateId: "tpl-5", workspaceNamePrefix: " team " })).toEqual({
			templateId: "tpl-5",
			workspaceNamePrefix: "team",
		});
		expect(buildCoderRequestOptions({ ...base, templateId: "tpl-5", workspaceNamePrefix: "" })).toEqual({ templateId: "tpl-5" });
		expect(buildCoderRequestOptions({ ...base, workspaceNamePrefix: "team" })).toEqual({ workspaceNamePrefix: "team" });
	});

	it("sends template options only alongside a chosen template, and only when some changed", () => {
		expect(buildCoderRequestOptions({ ...base, templateId: "tpl-6", parameters: { region: "us" } })).toEqual({
			templateId: "tpl-6",
			parameters: { region: "us" },
		});
		expect(buildCoderRequestOptions({ ...base, templateId: "tpl-6", parameters: {} })).toEqual({ templateId: "tpl-6" });
		expect(buildCoderRequestOptions({ ...base, parameters: { region: "us" } })).toBeUndefined();
	});
});

describe("useCoderSessionOptionsStore", () => {
	it("clears template options when the template changes", () => {
		const store = useCoderSessionOptionsStore.getState();
		store.reset();
		store.setTemplate("tpl-1", []);
		useCoderSessionOptionsStore.getState().setParameters({ region: "us" });
		// Re-selecting the same template keeps them.
		useCoderSessionOptionsStore.getState().setTemplate("tpl-1", ["size"]);
		expect(useCoderSessionOptionsStore.getState().parameters).toEqual({ region: "us" });
		useCoderSessionOptionsStore.getState().setTemplate("tpl-2", []);
		expect(useCoderSessionOptionsStore.getState().parameters).toEqual({});
	});
});
