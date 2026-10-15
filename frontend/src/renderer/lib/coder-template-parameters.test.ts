import { describe, expect, it } from "vitest";
import type { CloudCpCoderTemplateParameter } from "./cloud-cp/types";
import { changedParameterOverrides, editableTemplateParameters, parameterOverridesUpdate, withParameterValue } from "./coder-template-parameters";

const region: CloudCpCoderTemplateParameter = { name: "region", type: "string", mutable: true, required: false, defaultValue: "eu" };
const docker: CloudCpCoderTemplateParameter = { name: "docker", type: "bool", mutable: true, required: false, defaultValue: "true" };
const services: CloudCpCoderTemplateParameter = { name: "services", type: "list(string)", mutable: true, required: false, defaultValue: "[\"db\"]" };
const cpus: CloudCpCoderTemplateParameter = { name: "cpus", type: "number", mutable: true, required: false, defaultValue: "4" };
const parameters = [region, docker, services, cpus];

describe("coder template parameters", () => {
	it("leaves AO-managed parameters out of the editable list", () => {
		const size: CloudCpCoderTemplateParameter = { name: "size", type: "string", mutable: true, required: false };
		const startup: CloudCpCoderTemplateParameter = { name: "startup_script", type: "string", mutable: true, required: false };
		expect(editableTemplateParameters([size, region, startup]).map((p) => p.name)).toEqual(["region"]);
	});

	it("keeps only values that differ from the default, comparing by type", () => {
		let overrides = withParameterValue({}, region, "us");
		overrides = withParameterValue(overrides, docker, "TRUE");
		overrides = withParameterValue(overrides, services, "[ \"db\" ]");
		overrides = withParameterValue(overrides, cpus, "4.0");
		expect(overrides).toEqual({ region: "us" });
		expect(withParameterValue(overrides, region, "eu")).toEqual({});
	});

	it("drops saved overrides that equal the default and keeps unknown keys", () => {
		expect(changedParameterOverrides(parameters, { region: "eu", docker: "false", legacy: "x" })).toEqual({ docker: "false", legacy: "x" });
		expect(changedParameterOverrides(parameters, undefined)).toEqual({});
	});

	it("saves the whole draft map, since the endpoint replaces it", () => {
		const saved = { region: "us", docker: "false" };
		// One changed key still sends every override, so docker survives.
		expect(parameterOverridesUpdate(parameters, saved, { region: "ap", docker: "false" })).toEqual({ region: "ap", docker: "false" });
		// docker back to its default: absent from the map, so it is removed.
		expect(parameterOverridesUpdate(parameters, saved, { region: "us" })).toEqual({ region: "us" });
		expect(parameterOverridesUpdate(parameters, saved, saved)).toBeUndefined();
		// A saved key already at its default changes nothing; dropping an unknown one does.
		expect(parameterOverridesUpdate(parameters, { region: "eu" }, {})).toBeUndefined();
		expect(parameterOverridesUpdate(parameters, { gone: "1" }, {})).toEqual({});
	});
});
