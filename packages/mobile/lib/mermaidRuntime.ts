import { Asset } from "expo-asset";
import { File } from "expo-file-system";
import mermaidAsset from "../assets/generated/mermaid.webjs";

let localUri: Promise<string> | undefined;

/**
 * The Mermaid engine's source, read from the asset the build ships (see
 * metro.config.js). The asset is resolved once; its text is read per diagram
 * rather than held, since it is several megabytes.
 */
export async function loadMermaidRuntime(): Promise<string> {
	localUri ??= (async () => {
		const asset = Asset.fromModule(mermaidAsset);
		await asset.downloadAsync();
		if (!asset.localUri) throw new Error("The diagram engine isn't available.");
		return asset.localUri;
	})();
	try {
		return await new File(await localUri).text();
	} catch (cause) {
		localUri = undefined;
		throw cause;
	}
}
