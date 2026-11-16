package ao.mobile.selection;

import android.view.ActionMode;
import android.view.Menu;
import android.view.MenuItem;
import android.view.View;
import android.widget.TextView;
import com.facebook.react.bridge.Arguments;
import com.facebook.react.bridge.ReactApplicationContext;
import com.facebook.react.bridge.ReactContextBaseJavaModule;
import com.facebook.react.bridge.ReactMethod;
import com.facebook.react.bridge.UIManager;
import com.facebook.react.bridge.UiThreadUtil;
import com.facebook.react.bridge.WritableMap;
import com.facebook.react.modules.core.DeviceEventManagerModule;
import com.facebook.react.uimanager.UIManagerHelper;
import java.lang.ref.WeakReference;
import java.util.HashMap;
import java.util.Map;

/** Adds one action to the existing RN TextView; standard selection remains native. */
public final class ResponseSelectionModule extends ReactContextBaseJavaModule {
  private static final int ADD_TO_CHAT = 0x0a0c0001;
  private final Map<Integer, Binding> bindings = new HashMap<>();

  private static final class Binding {
    final WeakReference<TextView> view;
    final ActionMode.Callback previous;
    final ActionMode.Callback callback;
    Binding(TextView view, ActionMode.Callback previous, ActionMode.Callback callback) {
      this.view = new WeakReference<>(view);
      this.previous = previous;
      this.callback = callback;
    }
    void restore() {
      TextView textView = view.get();
      if (textView != null && textView.getCustomSelectionActionModeCallback() == callback) {
        textView.setCustomSelectionActionModeCallback(previous);
      }
    }
  }

  ResponseSelectionModule(ReactApplicationContext context) { super(context); }
  @Override public String getName() { return "AOResponseSelection"; }

  @ReactMethod public void attach(int tag) {
    UiThreadUtil.runOnUiThread(() -> {
      UIManager manager = UIManagerHelper.getUIManagerForReactTag(getReactApplicationContext(), tag);
      if (manager == null) return;
      final View resolved;
      try { resolved = manager.resolveView(tag); }
      catch (RuntimeException unmounted) { return; }
      if (!(resolved instanceof TextView)) return;
      TextView textView = (TextView) resolved;
      Binding old = bindings.remove(tag);
      if (old != null) old.restore();
      ActionMode.Callback previous = textView.getCustomSelectionActionModeCallback();
      // Do not capture textView strongly: RN may recycle a timeline row.
      WeakReference<TextView> weakView = new WeakReference<>(textView);
      ActionMode.Callback callback = new ActionMode.Callback() {
        @Override public boolean onCreateActionMode(ActionMode mode, Menu menu) {
          if (previous != null && !previous.onCreateActionMode(mode, menu)) return false;
          addAction(menu);
          return true;
        }
        @Override public boolean onPrepareActionMode(ActionMode mode, Menu menu) {
          boolean changed = previous != null && previous.onPrepareActionMode(mode, menu);
          return addAction(menu) || changed;
        }
        private boolean addAction(Menu menu) {
          if (menu.findItem(ADD_TO_CHAT) != null) return false;
          menu.add(Menu.NONE, ADD_TO_CHAT, 0, "Add to chat").setShowAsAction(MenuItem.SHOW_AS_ACTION_IF_ROOM);
          return true;
        }
        @Override public boolean onActionItemClicked(ActionMode mode, MenuItem item) {
          if (item.getItemId() != ADD_TO_CHAT) return previous != null && previous.onActionItemClicked(mode, item);
          TextView view = weakView.get();
          Binding current = bindings.get(tag);
          if (view == null || current == null || current.callback != this) return true;
          int start = Math.min(view.getSelectionStart(), view.getSelectionEnd());
          int end = Math.max(view.getSelectionStart(), view.getSelectionEnd());
          CharSequence text = view.getText();
          if (start >= 0 && end > start && end <= text.length() && getReactApplicationContext().hasActiveReactInstance()) {
            WritableMap event = Arguments.createMap();
            event.putInt("tag", tag);
            event.putString("text", text.subSequence(start, end).toString());
            getReactApplicationContext().getJSModule(DeviceEventManagerModule.RCTDeviceEventEmitter.class)
                .emit("AOAddSelectedTextToChat", event);
          }
          mode.finish();
          return true;
        }
        @Override public void onDestroyActionMode(ActionMode mode) {
          if (previous != null) previous.onDestroyActionMode(mode);
        }
      };
      bindings.put(tag, new Binding(textView, previous, callback));
      textView.setCustomSelectionActionModeCallback(callback);
    });
  }

  @ReactMethod public void detach(int tag) {
    UiThreadUtil.runOnUiThread(() -> {
      Binding binding = bindings.remove(tag);
      if (binding != null) binding.restore();
    });
  }

  @Override public void invalidate() {
    super.invalidate();
    UiThreadUtil.runOnUiThread(() -> {
      for (Binding binding : bindings.values()) binding.restore();
      bindings.clear();
    });
  }
}
