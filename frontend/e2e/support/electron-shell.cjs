const { app, BrowserWindow } = require("electron");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");

const profiles = path.join(os.homedir(), ".ao", "dev", "e2e-electron");
fs.mkdirSync(profiles, { recursive: true });
app.setPath("userData", fs.mkdtempSync(path.join(profiles, "annotations-")));
app.whenReady().then(() => {
	new BrowserWindow({ width: 1280, height: 720 }).loadURL("about:blank");
});
