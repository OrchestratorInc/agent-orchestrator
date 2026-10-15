import { execFile } from "node:child_process";
import path from "node:path";
import { promisify } from "node:util";

export type MacTerminalApp = { bundleId: string; name: string; appPath: string };

// Query Launch Services, rather than scanning app names or developer-tool
// categories (which would also match IDEs). Shell-role handlers can execute
// programs; folder support gives us a standard way to send the workspace.
// This script only reads registrations and bundle metadata, never runs an app.
export const MAC_TERMINAL_APPS_SCRIPT = `
ObjC.import("AppKit");
ObjC.import("CoreServices");
var ids = {};
["public.unix-executable", "com.apple.terminal.shell-script"].forEach(function(type) {
  var handlers = $.LSCopyAllRoleHandlersForContentType($(type), 8); // kLSRolesShell
  if (!handlers) return;
  var values = ObjC.deepUnwrap(ObjC.castRefToObject(handlers));
  if (Array.isArray(values)) values.forEach(function(id) { ids[id] = true; });
});
var apps = [];
Object.keys(ids).forEach(function(id) {
  try {
    var url = $.NSWorkspace.sharedWorkspace.URLForApplicationWithBundleIdentifier($(id));
    if (!url || url.isNil()) return;
    var bundle = $.NSBundle.bundleWithURL(url);
    var info = ObjC.deepUnwrap(bundle.infoDictionary);
    var folders = (info.CFBundleDocumentTypes || []).some(function(doc) {
      return (doc.LSItemContentTypes || []).some(function(type) {
        return type === "public.folder" || type === "public.directory";
      });
    });
    if (!folders) return;
    apps.push({ bundleId: info.CFBundleIdentifier, name: info.CFBundleDisplayName || info.CFBundleName,
      appPath: ObjC.unwrap(url.path) });
  } catch (_) { /* Ignore stale registrations or unreadable bundles. */ }
});
JSON.stringify(apps);
`;

const execFileAsync = promisify(execFile);

export async function discoverMacTerminalApps(
	run: (script: string) => Promise<string> = async (script) => {
		const { stdout } = await execFileAsync("/usr/bin/osascript", ["-l", "JavaScript", "-e", script], {
			timeout: 5_000,
			maxBuffer: 1024 * 1024,
		});
		return stdout;
	},
): Promise<MacTerminalApp[]> {
	const result: unknown = JSON.parse(await run(MAC_TERMINAL_APPS_SCRIPT));
	if (!Array.isArray(result)) throw new Error("Invalid terminal discovery response");
	return result.filter((entry): entry is MacTerminalApp =>
		entry !== null && typeof entry === "object" &&
		typeof entry.bundleId === "string" && /^[a-z0-9.-]+$/i.test(entry.bundleId) &&
		typeof entry.name === "string" && Boolean(entry.name.trim()) &&
		typeof entry.appPath === "string" && path.posix.isAbsolute(entry.appPath) && entry.appPath.endsWith(".app"),
	);
}
