import {
	FILE_ICON_BY_EXTENSION,
	FILE_ICON_BY_NAME,
	FILE_ICON_FALLBACK,
	FILE_ICON_SVGS,
	FOLDER_ICON_BY_NAME,
	FOLDER_ICON_FALLBACK,
} from "./fileTypeIcons.generated";

function baseName(path: string): string {
	return path.slice(path.lastIndexOf("/") + 1).toLowerCase();
}

/**
 * The desktop icon for a file: its exact name first (package.json, Dockerfile),
 * then its last two extensions (test.ts, d.ts), then its last one, then a plain
 * page — the order the Symbols theme itself resolves in.
 */
export function fileIconSvg(path: string): string {
	const name = baseName(path);
	const byName = FILE_ICON_BY_NAME[name];
	if (byName !== undefined) return FILE_ICON_SVGS[byName];
	const parts = name.split(".");
	if (parts.length > 2) {
		const compound = FILE_ICON_BY_EXTENSION[parts.slice(-2).join(".")];
		if (compound !== undefined) return FILE_ICON_SVGS[compound];
	}
	const extension = parts.length > 1 ? FILE_ICON_BY_EXTENSION[parts[parts.length - 1]] : undefined;
	return FILE_ICON_SVGS[extension ?? FILE_ICON_FALLBACK];
}

export function folderIconSvg(name: string): string {
	return FILE_ICON_SVGS[FOLDER_ICON_BY_NAME[baseName(name)] ?? FOLDER_ICON_FALLBACK];
}
