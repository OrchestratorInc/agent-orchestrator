// Copies a Claude session with the bundled Agent SDK and prints the new id.
// Usage: node fork_session.mjs <sdk.mjs path> <source session id> <source dir> [up-to message uuid]
import { pathToFileURL } from "node:url";

const [sdkPath, sessionId, dir, upToMessageId] = process.argv.slice(2);
// A file URL, so a Windows drive path or a "#" in the path still loads.
const sdk = await import(pathToFileURL(sdkPath).href);
const options = upToMessageId ? { dir, upToMessageId } : { dir };
const result = await sdk.forkSession(sessionId, options);
process.stdout.write(JSON.stringify({ sessionId: result.sessionId }) + "\n");
