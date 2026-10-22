// A bare Electron window for running renderer e2e specs in the Chromium that AO
// actually ships (AO_E2E_ELECTRON=1), which can differ from Playwright's own.
// The throwaway profile goes to a temp directory, never the OS app-data folder.
const { app, BrowserWindow } = require("electron");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");

app.setPath("userData", fs.mkdtempSync(path.join(os.tmpdir(), "ao-e2e-electron-")));
app.whenReady().then(() => {
	new BrowserWindow({ width: 1280, height: 720 }).loadURL("about:blank");
});
