import { createRequire } from "node:module";

const require = createRequire(import.meta.url);

const { buildBlockMap } = require("app-builder-lib/out/targets/blockmap/blockmap.js");

export async function writeBlockmap(filePath, destination = `${filePath}.blockmap`) {
	const { sha512, size } = await buildBlockMap(filePath, "gzip", destination);
	return { sha512, size };
}
