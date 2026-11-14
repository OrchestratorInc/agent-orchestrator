package ao.mobile.oauthloopback;

import com.facebook.react.bridge.Promise;
import com.facebook.react.bridge.ReactApplicationContext;
import com.facebook.react.bridge.ReactContextBaseJavaModule;
import com.facebook.react.bridge.ReactMethod;
import java.io.IOException;
import java.io.InputStream;
import java.io.OutputStream;
import java.net.BindException;
import java.net.InetAddress;
import java.net.InetSocketAddress;
import java.net.ServerSocket;
import java.net.Socket;
import java.net.SocketTimeoutException;
import java.nio.charset.StandardCharsets;

/**
 * A one-shot HTTP listener on 127.0.0.1 for a coding agent's sign-in.
 *
 * Claude Code and Codex finish their browser sign-in by redirecting to
 * http://localhost:PORT/PATH?code=..., the redirect their OAuth clients are
 * registered for. The sign-in runs in a Custom Tab, which shares the person's
 * Chrome sessions; this listener receives the redirect, answers it with a
 * redirect back into the app, and hands the request target to JavaScript.
 */
public final class OAuthLoopbackModule extends ReactContextBaseJavaModule {
  private final Object lock = new Object();
  private ServerSocket server;
  private String callbackPath;
  private String returnUrl;
  private String pending;
  private Promise waiter;

  OAuthLoopbackModule(ReactApplicationContext context) {
    super(context);
  }

  @Override public String getName() {
    return "AOOAuthLoopback";
  }

  @ReactMethod
  public void start(double port, String path, String returnTo, Promise promise) {
    stopInternal("A new sign-in started.");
    final ServerSocket socket;
    try {
      socket = new ServerSocket();
      socket.setReuseAddress(true);
      socket.bind(new InetSocketAddress(InetAddress.getByName("127.0.0.1"), (int) port), 8);
    } catch (BindException error) {
      promise.reject("in_use", "The sign-in port on this phone is busy.", error);
      return;
    } catch (Exception error) {
      promise.reject("socket", "Couldn't listen for the sign-in.", error);
      return;
    }
    synchronized (lock) {
      server = socket;
      callbackPath = path;
      returnUrl = returnTo;
      pending = null;
    }
    Thread thread = new Thread(() -> acceptLoop(socket), "ao-oauth-loopback");
    thread.setDaemon(true);
    thread.start();
    promise.resolve(socket.getLocalPort());
  }

  @ReactMethod
  public void waitForCallback(Promise promise) {
    synchronized (lock) {
      if (pending != null) {
        promise.resolve(pending);
        pending = null;
        return;
      }
      if (server == null) {
        promise.reject("not_started", "The sign-in listener isn't running.");
        return;
      }
      if (waiter != null) waiter.reject("cancelled", "Superseded by a newer wait.");
      waiter = promise;
    }
  }

  @ReactMethod
  public void stop() {
    stopInternal("Sign-in cancelled.");
  }

  @Override public void invalidate() {
    stopInternal("The app is closing.");
  }

  private void stopInternal(String reason) {
    synchronized (lock) {
      if (server != null) {
        try { server.close(); } catch (Exception ignored) { }
        server = null;
      }
      if (waiter != null) waiter.reject("cancelled", reason);
      waiter = null;
      pending = null;
    }
  }

  private static String escapeHtml(String text) {
    return text.replace("&", "&amp;").replace("\"", "&quot;").replace("<", "&lt;");
  }

  private static final int REQUEST_LINE_LIMIT = 8192;
  private static final long REQUEST_LINE_DEADLINE_NANOS = 3_000_000_000L;

  /**
   * The request line ("GET /callback?code=... HTTP/1.1"), read through its line
   * end: one read may return only part of it. Null when the client closes, or
   * the line isn't complete within 8 KiB and 3 seconds in all, so a client that
   * trickles bytes can't hold the listener's one thread.
   */
  private static String readRequestLine(Socket client) throws IOException {
    InputStream in = client.getInputStream();
    byte[] buffer = new byte[REQUEST_LINE_LIMIT];
    int length = 0;
    long start = System.nanoTime();
    while (length < buffer.length) {
      long left = REQUEST_LINE_DEADLINE_NANOS - (System.nanoTime() - start);
      if (left <= 0) return null;
      // Each read waits only as long as the deadline has left; a zero timeout would wait forever.
      client.setSoTimeout((int) Math.max(1, left / 1_000_000));
      int count;
      try {
        count = in.read(buffer, length, buffer.length - length);
      } catch (SocketTimeoutException timedOut) {
        return null;
      }
      if (count < 0) return null;
      for (int i = length; i < length + count; i++) {
        if (buffer[i] != '\n') continue;
        int end = i > 0 && buffer[i - 1] == '\r' ? i - 1 : i;
        return new String(buffer, 0, end, StandardCharsets.UTF_8);
      }
      length += count;
    }
    return null;
  }

  private void acceptLoop(ServerSocket socket) {
    while (!socket.isClosed()) {
      try (Socket client = socket.accept()) {
        String line = readRequestLine(client);
        if (line == null) continue;
        String[] parts = line.split(" ");
        String target = parts.length >= 2 ? parts[1] : "";
        int query = target.indexOf('?');
        String path = query >= 0 ? target.substring(0, query) : target;
        String expected;
        String back;
        synchronized (lock) {
          // Stopped, or replaced by a newer sign-in, since this request was
          // accepted: it belongs to no sign-in now.
          if (server != socket) return;
          expected = callbackPath;
          back = returnUrl == null ? "" : returnUrl;
        }
        OutputStream out = client.getOutputStream();
        if (parts.length < 2 || !"GET".equals(parts[0]) || !path.equals(expected)) {
          out.write("HTTP/1.1 404 Not Found\r\nContent-Length: 0\r\nConnection: close\r\n\r\n".getBytes(StandardCharsets.UTF_8));
          out.flush();
          continue;
        }
        String body = "<!doctype html><meta charset=\"utf-8\"><meta name=\"viewport\" content=\"width=device-width\"><title>Signed in</title>"
          + "<body style=\"font:16px system-ui,sans-serif;padding:15vh 1.5rem;text-align:center\"><h1 style=\"font-size:1.25rem\">Signed in</h1>"
          + "<p><a href=\"" + escapeHtml(back) + "\">Return to Orchestrator</a></p></body>";
        byte[] bodyBytes = body.getBytes(StandardCharsets.UTF_8);
        String head = back.isEmpty()
          ? "HTTP/1.1 200 OK\r\n"
          : "HTTP/1.1 302 Found\r\nLocation: " + back + "\r\n";
        head += "Content-Type: text/html; charset=utf-8\r\nContent-Length: " + bodyBytes.length + "\r\nCache-Control: no-store\r\nConnection: close\r\n\r\n";
        out.write(head.getBytes(StandardCharsets.UTF_8));
        out.write(bodyBytes);
        out.flush();
        synchronized (lock) {
          if (server != socket) return;
          if (waiter != null) {
            waiter.resolve(target);
            waiter = null;
          } else {
            pending = target;
          }
        }
      } catch (Exception ignored) {
        // A closed listener ends the loop; a bad request is skipped.
      }
    }
  }
}
