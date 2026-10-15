// Litmus names the window it launched after the build it shows ("Litmus: PR build #6572",
// "Litmus: main for #6572") and gives it a Litmus Dock icon, so the two builds under test are
// never mistaken for the user's own AO. The unpackaged Electron under test loads this through
// NODE_OPTIONS=--require, so it applies to any checkout without touching the code under test.
// Plain Node processes in the dev chain (vite, forge) load it too and do nothing.
//
// A --require preload runs before Electron registers its own "electron" module, so the
// require is retried until it resolves (a few hundred ms), then the app is labelled.
if (process.versions && process.versions.electron && process.type === "browser") {
	let tries = 0;
	const attempt = () => {
		let electron;
		try {
			electron = require("electron");
		} catch (err) {
			if (++tries < 100) return setTimeout(attempt, 50);
			return console.error("litmus-window: electron not loadable: " + (err && err.message ? err.message : err));
		}
		try {
			const { app, nativeImage, BaseWindow } = electron;
			const leg = process.env.LITMUS_LEG;
			const pr = process.env.LITMUS_PR;
			const num = pr && /^\d+$/.test(pr) ? " #" + pr : "";
			const title = leg === "base" ? "Litmus: main" + (num ? " for" + num : "") : "Litmus: PR build" + num;
			app.setName(title);
			app.whenReady().then(() => {
				const icon = nativeImage.createFromPath(process.env.LITMUS_ICON || "");
				if (process.platform === "darwin" && app.dock && !icon.isEmpty()) app.dock.setIcon(icon);
				const label = () => {
					for (const w of BaseWindow.getAllWindows()) if (w.getTitle() !== title) w.setTitle(title);
				};
				label();
				setInterval(label, 1000).unref();
				console.error("litmus-window: labelled " + JSON.stringify(title));
			});
		} catch (err) {
			console.error("litmus-window: " + (err && err.message ? err.message : err));
		}
	};
	attempt();
}
