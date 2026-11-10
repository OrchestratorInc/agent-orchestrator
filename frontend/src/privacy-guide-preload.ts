import { ipcRenderer } from "electron";

// The Cua Driver permission guide's only script: a drag of the CuaDriver tile
// becomes a native file drag of CuaDriver.app, which System Settings' privacy
// lists accept as a drop.
window.addEventListener("dragstart", (event) => {
	if (!(event.target instanceof Element) || !event.target.closest("[data-drag-app]")) return;
	event.preventDefault();
	ipcRenderer.send("privacyGuide:startDrag");
});
