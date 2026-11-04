package ao.mobile.imagepaste;

import android.content.ContentResolver;
import android.content.Context;
import android.content.pm.PackageInfo;
import android.content.pm.PackageManager;
import android.content.pm.ProviderInfo;
import android.graphics.Bitmap;
import android.graphics.BitmapFactory;
import android.net.Uri;
import android.os.Build;
import android.os.Bundle;
import android.util.Base64;
import android.util.Pair;
import android.view.ActionMode;
import android.view.Menu;
import android.view.MenuItem;
import android.view.View;
import android.view.inputmethod.InputContentInfo;
import android.view.inputmethod.InputMethodManager;
import android.widget.EditText;
import androidx.core.view.ContentInfoCompat;
import androidx.core.view.ViewCompat;
import com.facebook.react.bridge.Arguments;
import com.facebook.react.bridge.ReactApplicationContext;
import com.facebook.react.bridge.ReactContextBaseJavaModule;
import com.facebook.react.bridge.ReactMethod;
import com.facebook.react.bridge.UIManager;
import com.facebook.react.bridge.UiThreadUtil;
import com.facebook.react.bridge.WritableArray;
import com.facebook.react.bridge.WritableMap;
import com.facebook.react.modules.core.DeviceEventManagerModule;
import com.facebook.react.uimanager.UIManagerHelper;
import java.io.ByteArrayOutputStream;
import java.io.InputStream;
import java.lang.ref.WeakReference;
import java.util.ArrayList;
import java.util.Arrays;
import java.util.HashMap;
import java.util.HashSet;
import java.util.List;
import java.util.Map;
import java.util.Set;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;

/**
 * Lets the RN composer EditText receive images: the long-press Paste item and a
 * keyboard's image insertion (Gboard's clipboard strip, stickers) both arrive
 * through OnReceiveContentListener. Text in the same payload is left to the field.
 */
public final class ImagePasteModule extends ReactContextBaseJavaModule {
  private static final String[] MIME_TYPES = {"image/*"};
  // Matches the composer's 10 MB per-image limit; anything larger is reported by size, unread.
  private static final int MAX_IMAGE_BYTES = 10 * 1024 * 1024;
  // The composer's batch limits (8 attachments, 25 MB of images). Applied here too, so
  // a large multi-image paste isn't read and encoded only to be discarded in JS.
  private static final int MAX_IMAGES = 8;
  private static final long MAX_BATCH_BYTES = 25L * 1024 * 1024;
  private static final Set<String> PASSTHROUGH_TYPES = new HashSet<>(Arrays.asList(
      "image/png", "image/jpeg", "image/jpg", "image/gif", "image/webp", "image/bmp"));
  private final Map<Integer, Binding> bindings = new HashMap<>();
  private final ExecutorService reader = Executors.newSingleThreadExecutor();

  /** Wraps RN's toolbar callback only to learn which ActionMode is showing. */
  private static final class TrackedCallback implements ActionMode.Callback {
    final ActionMode.Callback previous;
    ActionMode current;
    TrackedCallback(ActionMode.Callback previous) { this.previous = previous; }
    @Override public boolean onCreateActionMode(ActionMode mode, Menu menu) {
      boolean show = previous == null || previous.onCreateActionMode(mode, menu);
      current = show ? mode : null;
      return show;
    }
    @Override public boolean onPrepareActionMode(ActionMode mode, Menu menu) {
      return previous != null && previous.onPrepareActionMode(mode, menu);
    }
    @Override public boolean onActionItemClicked(ActionMode mode, MenuItem item) {
      return previous != null && previous.onActionItemClicked(mode, item);
    }
    @Override public void onDestroyActionMode(ActionMode mode) {
      if (current == mode) current = null;
      if (previous != null) previous.onDestroyActionMode(mode);
    }
    void finish() { if (current != null) current.finish(); }
  }

  private static final class Binding {
    final WeakReference<EditText> field;
    final TrackedCallback insertion;
    final TrackedCallback selection;
    Binding(EditText field) {
      this.field = new WeakReference<>(field);
      insertion = new TrackedCallback(field.getCustomInsertionActionModeCallback());
      selection = new TrackedCallback(field.getCustomSelectionActionModeCallback());
      field.setCustomInsertionActionModeCallback(insertion);
      field.setCustomSelectionActionModeCallback(selection);
    }
    // A text paste changes the text, which closes the Paste toolbar; an image doesn't.
    void finishToolbars() { insertion.finish(); selection.finish(); }
    void restore(EditText view) {
      if (view.getCustomInsertionActionModeCallback() == insertion) view.setCustomInsertionActionModeCallback(insertion.previous);
      if (view.getCustomSelectionActionModeCallback() == selection) view.setCustomSelectionActionModeCallback(selection.previous);
    }
  }

  ImagePasteModule(ReactApplicationContext context) { super(context); }
  @Override public String getName() { return "AOImagePaste"; }

  @ReactMethod public void attach(int tag) {
    UiThreadUtil.runOnUiThread(() -> {
      UIManager manager = UIManagerHelper.getUIManagerForReactTag(getReactApplicationContext(), tag);
      if (manager == null) return;
      final View resolved;
      try { resolved = manager.resolveView(tag); }
      catch (RuntimeException unmounted) { return; }
      if (!(resolved instanceof EditText)) return;
      EditText field = (EditText) resolved;
      detachNow(tag);
      Binding binding = new Binding(field);
      ViewCompat.setOnReceiveContentListener(field, MIME_TYPES, (view, payload) -> {
        ContentResolver resolver = view.getContext().getContentResolver();
        Pair<ContentInfoCompat, ContentInfoCompat> split = payload.partition(
            item -> item.getUri() != null && isImage(resolver, item.getUri(), payload));
        if (split.first != null) {
          List<Uri> uris = new ArrayList<>();
          for (int i = 0; i < split.first.getClip().getItemCount(); i++) uris.add(split.first.getClip().getItemAt(i).getUri());
          // The job holds the payload itself, not just its URIs: for a keyboard
          // insertion it owns the IME's temporary read grant, which ends when the
          // grant's InputContentInfo is collected. Released once the reads are done.
          reader.execute(() -> {
            try { emit(tag, resolver, uris); }
            finally { releaseImeGrant(payload); }
          });
          view.post(binding::finishToolbars);
        }
        return split.second;
      });
      bindings.put(tag, binding);
      restartInput(field);
    });
  }

  // NativeEventEmitter's contract; iOS counts listeners, Android always delivers.
  @ReactMethod public void addListener(String eventName) {}
  @ReactMethod public void removeListeners(double count) {}

  @ReactMethod public void detach(int tag) {
    UiThreadUtil.runOnUiThread(() -> detachNow(tag));
  }

  private void detachNow(int tag) {
    Binding old = bindings.remove(tag);
    EditText field = old == null ? null : old.field.get();
    if (field == null) return;
    old.restore(field);
    ViewCompat.setOnReceiveContentListener(field, null, null);
    restartInput(field);
  }

  // Where AndroidX's InputConnectionCompat keeps the IME's InputContentInfo
  // after requesting its read permission.
  private static final String IME_CONTENT_INFO = "androidx.core.view.extra.INPUT_CONTENT_INFO";

  @SuppressWarnings("deprecation")
  private static void releaseImeGrant(ContentInfoCompat payload) {
    Bundle extras = payload.getExtras();
    if (extras == null || Build.VERSION.SDK_INT < Build.VERSION_CODES.N_MR1) return;
    Object info = extras.get(IME_CONTENT_INFO);
    if (info instanceof InputContentInfo) ((InputContentInfo) info).releasePermission();
  }

  // The keyboard reads the accepted MIME types when its connection opens, so an
  // already-focused field needs a fresh connection to offer image insertion.
  private static void restartInput(EditText field) {
    if (!field.hasFocus()) return;
    InputMethodManager imm = (InputMethodManager) field.getContext().getSystemService(Context.INPUT_METHOD_SERVICE);
    if (imm != null) imm.restartInput(field);
  }

  private static boolean isImage(ContentResolver resolver, Uri uri, ContentInfoCompat payload) {
    String type = null;
    try { type = resolver.getType(uri); } catch (RuntimeException ignored) {}
    if (type != null) return type.startsWith("image/");
    return payload.getClip().getDescription().hasMimeType("image/*");
  }

  private void emit(int tag, ContentResolver resolver, List<Uri> uris) {
    WritableArray images = Arguments.createArray();
    Set<String> own = ownAuthorities();
    String overflow = null;
    int count = 0;
    long sent = 0;
    for (Uri uri : uris) {
      if (count >= MAX_IMAGES) { overflow = "count"; break; }
      WritableMap image = isForeignContent(uri, own) ? read(resolver, uri) : unreadable(null);
      long bytes = image.hasKey("data") ? (long) image.getDouble("bytes") : 0;
      if (count > 0 && sent + bytes > MAX_BATCH_BYTES) { overflow = "size"; break; }
      sent += bytes;
      images.pushMap(image);
      count++;
    }
    ReactApplicationContext context = getReactApplicationContext();
    if (!context.hasActiveReactInstance()) return;
    WritableMap event = Arguments.createMap();
    event.putInt("tag", tag);
    event.putArray("images", images);
    if (overflow != null) event.putString("overflow", overflow);
    context.getJSModule(DeviceEventManagerModule.RCTDeviceEventEmitter.class).emit("AOImagePaste", event);
  }

  /**
   * Reads run with this app's own permissions, so a clip may only point at another
   * app's content provider: never a file path, and never one of our own providers,
   * which could otherwise hand a crafted clip our private files.
   */
  private static boolean isForeignContent(Uri uri, Set<String> own) {
    return ContentResolver.SCHEME_CONTENT.equals(uri.getScheme()) && uri.getAuthority() != null && !own.contains(uri.getAuthority());
  }

  private Set<String> ownAuthorities() {
    Set<String> authorities = new HashSet<>();
    Context context = getReactApplicationContext();
    try {
      PackageInfo info = context.getPackageManager().getPackageInfo(context.getPackageName(), PackageManager.GET_PROVIDERS);
      if (info.providers != null) {
        for (ProviderInfo provider : info.providers) {
          if (provider.authority != null) authorities.addAll(Arrays.asList(provider.authority.split(";")));
        }
      }
    } catch (PackageManager.NameNotFoundException ignored) {}
    return authorities;
  }

  private static WritableMap unreadable(String type) {
    WritableMap image = Arguments.createMap();
    image.putString("mimeType", type == null ? "image/png" : type);
    image.putDouble("bytes", 0);
    return image;
  }

  /** `{ mimeType, bytes, data? }`; no data means the image was too large or unreadable. */
  private static WritableMap read(ContentResolver resolver, Uri uri) {
    WritableMap image = Arguments.createMap();
    String type = null;
    try { type = resolver.getType(uri); } catch (RuntimeException ignored) {}
    byte[] bytes;
    try (InputStream input = resolver.openInputStream(uri)) {
      if (input == null) throw new IllegalStateException("No stream");
      ByteArrayOutputStream output = new ByteArrayOutputStream();
      byte[] buffer = new byte[64 * 1024];
      int count;
      while ((count = input.read(buffer)) != -1) {
        output.write(buffer, 0, count);
        if (output.size() > MAX_IMAGE_BYTES) {
          image.putString("mimeType", type == null ? "image/png" : type);
          image.putDouble("bytes", output.size());
          return image;
        }
      }
      bytes = output.toByteArray();
    } catch (Exception failed) {
      return unreadable(type);
    }
    // HEIC and other formats the agents can't take are re-encoded as JPEG.
    if (type == null || !PASSTHROUGH_TYPES.contains(type)) {
      Bitmap bitmap = BitmapFactory.decodeByteArray(bytes, 0, bytes.length);
      if (bitmap == null) return unreadable(type);
      ByteArrayOutputStream jpeg = new ByteArrayOutputStream();
      bitmap.compress(Bitmap.CompressFormat.JPEG, 90, jpeg);
      bitmap.recycle();
      bytes = jpeg.toByteArray();
      type = "image/jpeg";
    }
    image.putString("mimeType", type);
    image.putDouble("bytes", bytes.length);
    if (bytes.length <= MAX_IMAGE_BYTES) image.putString("data", Base64.encodeToString(bytes, Base64.NO_WRAP));
    return image;
  }

  @Override public void invalidate() {
    super.invalidate();
    reader.shutdown();
    UiThreadUtil.runOnUiThread(() -> {
      for (Integer tag : new ArrayList<>(bindings.keySet())) detachNow(tag);
    });
  }
}
