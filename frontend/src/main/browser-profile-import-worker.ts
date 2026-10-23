import { answerSourceReadRequest, type SourceReadRequest } from "./browser-profile-import";

// Utility-process entry for browser-profile import reads (see
// processSourceReader). Each process answers one request; the main process
// then stops it.
process.parentPort.once("message", async ({ data }: { data: SourceReadRequest }) => {
	process.parentPort.postMessage(await answerSourceReadRequest(data));
});
