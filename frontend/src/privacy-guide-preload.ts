import { ipcRenderer } from "electron";

window.addEventListener("dragstart", (event) => {
	if (!(event.target instanceof Element) || !event.target.closest("[data-drag-app]")) return;
	event.preventDefault();
	ipcRenderer.send("privacyGuide:startDrag");
});
