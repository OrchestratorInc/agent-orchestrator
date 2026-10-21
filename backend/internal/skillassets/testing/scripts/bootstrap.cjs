// This wrapper stays in ignored build output, never in AO frontend source.
const { app, ipcMain, BaseWindow } = require('electron');
const fs = require('node:fs');
const { randomBytes } = require('node:crypto');

if (app.isPackaged) throw new Error('Testing target must be unpackaged');
// This opt-in fixture changes only windows created by this owned Electron.
// Keep Cua's current-Space admission checks intact on hosts using several Spaces.
if (process.env.AO_TESTING_STICKY_WINDOWS === '1') {
  const configured = new WeakSet();
  app.on('web-contents-created', (_event, contents) => {
    contents.on('did-finish-load', () => {
      if (!contents.getURL().startsWith('app://renderer/')) return;
      for (const window of BaseWindow.getAllWindows()) {
        if (window.isDestroyed() || configured.has(window)) continue;
        configured.add(window);
        const pin = () => {
          if (window.isDestroyed()) return;
          window.setVisibleOnAllWorkspaces(true, { visibleOnFullScreen: true });
          console.log(`Testing target Space fixture active: ${window.isVisibleOnAllWorkspaces()}`);
        };
        // AO shows BaseWindow after this load event. Apply after that show too.
        window.on('show', pin);
        if (window.isVisible()) pin();
      }
    });
  });
}
if (process.env.AO_TARGET_MARKER_ORACLE) {
  const marker = `SCREEN-${randomBytes(8).toString('hex').toUpperCase()}`;
  fs.writeFileSync(process.env.AO_TARGET_MARKER_ORACLE, JSON.stringify({ marker }), { mode: 0o600 });
  app.on('web-contents-created', (_event, contents) => {
    contents.on('did-finish-load', () => {
      if (!contents.getURL().startsWith('app://renderer/')) return;
      const source = `(() => {
        if (document.getElementById('ao-testing-marker')) return;
        const badge = document.createElement('div');
        badge.id = 'ao-testing-marker';
        badge.textContent = ${JSON.stringify(marker)};
        badge.style.cssText = 'position:fixed;right:20px;bottom:20px;z-index:2147483647;background:#111;color:#fff;padding:12px 16px;border:2px solid #fff;font:20px monospace;pointer-events:none';
        document.body.appendChild(badge);
      })()`;
      void contents.executeJavaScript(source).then(() => {
        console.log('Testing target visual fixture rendered');
      }).catch(() => {
        // Never log the marker or injected source, including on failure.
        console.error('Testing target visual fixture could not be rendered');
      });
    });
  });
}
// Only the private controller can request the existing daemon start handler.
// This directory is fresh attempt state, outside the target checkout.
if (process.env.AO_TESTING_CONTROL_DIR) {
  const path = require('node:path');
  const dir = process.env.AO_TESTING_CONTROL_DIR;
  const handlers = new Map();
  const original = ipcMain.handle.bind(ipcMain);
  ipcMain.handle = (name, handler) => {
    if (name === 'daemon:start') handlers.set(name, handler);
    return original(name, handler);
  };
  let busy = false;
  const watcher = fs.watch(dir, async (_event, file) => {
    if (file !== 'request.json' || busy) return;
    busy = true;
    try {
      const request = JSON.parse(fs.readFileSync(path.join(dir, file), 'utf8'));
      if (!/^[a-f0-9]{32}$/.test(request.id) || request.action !== 'restart_daemon') return;
      const handler = handlers.get('daemon:start');
      if (!handler) throw new Error('This revision has no daemon:start handler');
      await handler();
      fs.writeFileSync(path.join(dir, request.id + '.json'), JSON.stringify({ ok: true }), { mode: 0o600, flag: 'wx' });
    } catch (error) {
      console.error('Testing controller daemon start failed: ' + error.message);
    } finally { busy = false; }
  });
  app.on('will-quit', () => watcher.close());
}
require('./ao-main.cjs');
