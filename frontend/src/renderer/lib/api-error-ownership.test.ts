// @vitest-environment node
import { readdir, readFile } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";
import ts from "typescript";
import { expect, it } from "vitest";

// CI source guard: API display wrappers must retain transport ownership.
function unsafeWrappers(source: string, filename = "source.ts"): number[] {
	const file = ts.createSourceFile(filename, source, ts.ScriptTarget.Latest, true);
	const names = new Set<string>();
	const namespaces = new Set<string>();
	for (const node of file.statements) {
		if (!ts.isImportDeclaration(node) || !ts.isStringLiteral(node.moduleSpecifier) || !node.moduleSpecifier.text.endsWith("/api-client")) continue;
		const bindings = node.importClause?.namedBindings;
		if (bindings && ts.isNamespaceImport(bindings)) namespaces.add(bindings.name.text);
		if (bindings && ts.isNamedImports(bindings)) for (const binding of bindings.elements) {
			if ((binding.propertyName ?? binding.name).text === "apiErrorMessage") names.add(binding.name.text);
		}
	}
	const lines: number[] = [];
	function visit(node: ts.Node): void {
		if (ts.isNewExpression(node) && ts.isIdentifier(node.expression) && node.expression.text === "Error") {
			const argument = node.arguments?.[0];
			if (argument && ts.isCallExpression(argument)) {
				const callee = argument.expression;
				const apiMessage = ts.isIdentifier(callee) ? names.has(callee.text) : ts.isPropertyAccessExpression(callee) && ts.isIdentifier(callee.expression) && namespaces.has(callee.expression.text) && callee.name.text === "apiErrorMessage";
				if (apiMessage) lines.push(file.getLineAndCharacterOfPosition(node.getStart()).line + 1);
			}
		}
		ts.forEachChild(node, visit);
	}
	visit(file); return lines;
}

it("rejects direct and aliased API wrappers while allowing the ownership helper", () => {
	expect(unsafeWrappers('import { apiErrorMessage } from "./api-client"; new Error(apiErrorMessage(error));')).toEqual([1]);
	expect(unsafeWrappers('import { apiErrorMessage as message } from "../lib/api-client"; new Error(message(error, "fallback"));')).toEqual([1]);
	expect(unsafeWrappers('import * as api from "./api-client"; new Error(api.apiErrorMessage(error));')).toEqual([1]);
	expect(unsafeWrappers('throw apiErrorException(error);')).toEqual([]);
});

it("requires API error wrappers to preserve ownership throughout the renderer", async () => {
	const root = fileURLToPath(new URL("../", import.meta.url));
	const violations: string[] = [];
	for (const name of await readdir(root, { recursive: true })) {
		if (!/\.tsx?$/.test(name) || /\.(?:test|spec)\./.test(name) || name === path.join("lib", "api-error-exception.ts")) continue;
		for (const line of unsafeWrappers(await readFile(path.join(root, name), "utf8"), name)) violations.push(`${name}:${line}`);
	}
	expect(violations, "Use apiErrorException(error, fallback) instead of new Error(apiErrorMessage(...))").toEqual([]);
});
