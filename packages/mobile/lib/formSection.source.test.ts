import { readdirSync, readFileSync, statSync } from "node:fs";
import { join } from "node:path";
import { expect, it } from "vitest";

// @expo/ui builds a different SwiftUI Section with and without a footer, so a
// footer that comes and goes rebuilds the section's fields and the field being
// typed in loses focus and the keyboard (#7906). A footer that can change goes
// through FormSection, which keeps the footer slot mounted.
const root = new URL("..", import.meta.url).pathname;

function sources(dir: string): string[] {
	return readdirSync(dir).flatMap((name) => {
		const path = join(dir, name);
		if (statSync(path).isDirectory()) return name === "node_modules" ? [] : sources(path);
		return /\.tsx$/.test(name) && !/\.(test|android)\.tsx$/.test(name) ? [path] : [];
	});
}

/** Each `footer={…}` expression on a SwiftUI Section, braces balanced. */
function sectionFooters(source: string): string[] {
	const footers: string[] = [];
	for (let start = source.indexOf("footer={"); start !== -1; start = source.indexOf("footer={", start + 1)) {
		let depth = 0;
		let end = start + "footer=".length;
		do {
			if (source[end] === "{") depth++;
			else if (source[end] === "}") depth--;
			end++;
		} while (depth > 0 && end < source.length);
		footers.push(source.slice(start + "footer={".length, end - 1));
	}
	return footers;
}

// The same holds through @expo/ui's FieldGroup (SettingsSection and the cloud
// credential fields use it): an empty SectionFooter is dropped, so the slot
// stays mounted only when it always holds an element, one that may draw nothing.
it("never mounts a FieldGroup section's footer slot only some of the time", () => {
	const offenders = [...sources(join(root, "lib")), ...sources(join(root, "app"))]
		.filter((path) => /(\?|&&)\s*\(?\s*<FieldGroup\.SectionFooter/.test(readFileSync(path, "utf8")))
		.map((path) => path.slice(root.length));
	expect(offenders).toEqual([]);
});

it("never gives a SwiftUI Section a footer that comes and goes", () => {
	const offenders = [...sources(join(root, "lib")), ...sources(join(root, "app"))]
		.filter((path) => /import \{[^}]*\bSection\b[^}]*\} from "@expo\/ui\/swift-ui"/.test(readFileSync(path, "utf8")))
		.flatMap((path) => sectionFooters(readFileSync(path, "utf8"))
			.filter((footer) => /\?|&&/.test(footer))
			.map((footer) => `${path.slice(root.length)}: footer={${footer.trim().slice(0, 80)}}`));
	expect(offenders).toEqual([]);
});
