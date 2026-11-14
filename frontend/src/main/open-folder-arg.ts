import { statSync } from "node:fs";
import path from "node:path";

export function parseOpenFolderPathArg(argv: string[]): string | undefined {
	const bootstrapAppIndex = process.defaultApp ? 1 : -1;
	for (const [index, entry] of argv.entries()) {
		if (index === bootstrapAppIndex || entry.startsWith("-") || entry.includes("://")) continue;
		try {
			if (statSync(path.resolve(entry)).isDirectory()) return path.resolve(entry);
		} catch {
		}
	}
	return undefined;
}
