package com.duolauncher.link;

import android.app.Activity;
import android.app.AlertDialog;
import android.content.Context;
import android.content.SharedPreferences;
import android.os.Build;
import android.os.Bundle;
import android.os.Handler;
import android.os.Looper;
import android.text.InputType;
import android.view.KeyEvent;
import android.view.View;
import android.view.WindowManager;
import android.view.inputmethod.InputMethodManager;
import android.widget.EditText;

import java.util.List;

/**
 * Duo Link for Android car stereos: finds a Cardputer running Duo Launcher over Wi-Fi (its own
 * Wi-Fi, "DuoLink-...", or a shared one), pairs with the code it shows, then shows its screen and
 * sends keys back. Wi-Fi only: a USB cable just charges the Cardputer.
 */
public final class MainActivity extends Activity implements ScreenView.Host, LinkClient.Listener, Discovery.Listener {
    /** The Cardputer on its own Wi-Fi (Duo's hotspot). */
    static final String HOTSPOT_HOST = "10.42.0.1";
    static final int PORT = 47140;

    private final Handler ui = new Handler(Looper.getMainLooper());
    private ScreenView view;
    private Discovery discovery;
    private volatile LinkClient client;
    private SharedPreferences prefs;
    private boolean keyboardShown;
    private boolean userLeft; // the user chose Leave: don't reconnect on our own
    private boolean welcomed; // the current connection got as far as WELCOME
    private boolean quiet; // the current connection is a background try: no "connecting" text
    private boolean altTurn; // background tries alternate the last address and the hotspot's
    private long lastTry;
    private AlertDialog dialog;

    @Override
    protected void onCreate(Bundle b) {
        super.onCreate(b);
        getWindow().addFlags(WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON);
        prefs = getSharedPreferences("duolink", Context.MODE_PRIVATE);
        view = new ScreenView(this, this);
        setContentView(view);
        immersive();
        discovery = new Discovery(this, this);
        showSearching();
    }

    @Override
    protected void onResume() {
        super.onResume();
        immersive();
        discovery.start();
        ui.post(retry);
    }

    @Override
    protected void onPause() {
        super.onPause();
        ui.removeCallbacks(retry);
        discovery.stop();
        // the connection stays up while the stereo shows something else (its radio, say)
        LinkClient c = client;
        if (c != null && isFinishing()) c.close();
    }

    @Override
    protected void onDestroy() {
        super.onDestroy();
        LinkClient c = client;
        if (c != null) c.close();
    }

    @Override
    public void onWindowFocusChanged(boolean hasFocus) {
        super.onWindowFocusChanged(hasFocus);
        if (hasFocus) immersive();
    }

    private void immersive() {
        if (Build.VERSION.SDK_INT >= 19) {
            getWindow().getDecorView().setSystemUiVisibility(View.SYSTEM_UI_FLAG_IMMERSIVE_STICKY | View.SYSTEM_UI_FLAG_FULLSCREEN
                    | View.SYSTEM_UI_FLAG_HIDE_NAVIGATION | View.SYSTEM_UI_FLAG_LAYOUT_STABLE
                    | View.SYSTEM_UI_FLAG_LAYOUT_HIDE_NAVIGATION | View.SYSTEM_UI_FLAG_LAYOUT_FULLSCREEN);
        }
    }

    private void showSearching() {
        view.title = "Duo Link";
        view.status = "Looking for your Cardputer over Wi-Fi...\n"
                + "On the Cardputer: Settings > Duo Link > Set up a stereo, then join\n"
                + "the Wi-Fi it shows (DuoLink-...) in this stereo's Wi-Fi settings.\n"
                + "Duo Link uses Wi-Fi only; a USB cable just charges the Cardputer.";
        String last = prefs.getString("last_name", "");
        if (!last.isEmpty() && !userLeft) view.status += "\nWill reconnect to " + last + " when it shows up.";
        view.devices = discovery == null ? view.devices : discovery.devices();
        view.dropPicture();
    }

    // tries the Cardputer every few seconds while nothing is connected: the last one if it
    // announces itself (or the only one heard, the first time), else its last address and the
    // Cardputer's own Wi-Fi address in turn (in case broadcasts don't get through)
    private final Runnable retry = this::retryTick;

    private void retryTick() {
        ui.postDelayed(retry, 3000);
        if (client != null || userLeft || dialog != null) return;
        String id = prefs.getString("last_id", "");
        List<Discovery.Device> heard = discovery.devices();
        for (Discovery.Device d : heard) {
            if (d.id.equals(id)) {
                connect(d, true);
                return;
            }
        }
        if (id.isEmpty() && heard.size() == 1) {
            connect(heard.get(0), true);
            return;
        }
        if (System.currentTimeMillis() - lastTry < 9000) return;
        String host = prefs.getString("last_host", "");
        altTurn = !altTurn;
        Discovery.Device d = new Discovery.Device();
        if (host.isEmpty() || host.equals(HOTSPOT_HOST) || altTurn) {
            d.host = HOTSPOT_HOST;
            d.port = PORT;
            d.id = host.equals(HOTSPOT_HOST) && !id.isEmpty() ? id : HOTSPOT_HOST + ":" + PORT;
            d.name = host.equals(HOTSPOT_HOST) ? prefs.getString("last_name", "Cardputer") : "Cardputer";
        } else {
            d.id = id.isEmpty() ? host : id;
            d.host = host;
            d.port = prefs.getInt("last_port", PORT);
            d.name = prefs.getString("last_name", host);
        }
        connect(d, true);
    }

    // ---- ScreenView.Host ----

    @Override
    public LinkClient client() {
        return client;
    }

    @Override
    public void connectTo(Discovery.Device d) {
        connect(d, false);
    }

    private void connect(Discovery.Device d, boolean background) {
        if (client != null) return;
        userLeft = false;
        welcomed = false;
        quiet = background;
        lastTry = System.currentTimeMillis();
        String token = prefs.getString("token_" + d.id, "");
        if (token.isEmpty()) token = prefs.getString("token_host_" + d.host + ":" + d.port, "");
        if (!background) {
            view.status = "Connecting to " + d.name + " (" + d.host + ")...";
            view.invalidate();
        }
        String me = Build.MODEL == null || Build.MODEL.isEmpty() ? "Car stereo" : Build.MODEL;
        LinkClient c = new LinkClient(getApplicationContext(), d.host, d.port, d.id, token, me, this);
        client = c;
        c.start();
    }

    @Override
    public void askAddress() {
        final EditText e = new EditText(this);
        e.setInputType(InputType.TYPE_CLASS_TEXT | InputType.TYPE_TEXT_VARIATION_URI);
        e.setHint(HOTSPOT_HOST + ":" + PORT);
        e.setText(prefs.getString("last_host", ""));
        dialog = new AlertDialog.Builder(this).setTitle("Cardputer's address")
                .setMessage("Shown on the Cardputer in Settings > Duo Link. On the Cardputer's own Wi-Fi it is "
                        + HOTSPOT_HOST + ".")
                .setView(e)
                .setPositiveButton("Connect", (di, w) -> {
                    dialog = null;
                    String s = e.getText().toString().trim();
                    if (s.startsWith("http://")) s = s.substring(7);
                    if (s.endsWith("/")) s = s.substring(0, s.length() - 1);
                    if (s.isEmpty()) return;
                    Discovery.Device d = new Discovery.Device();
                    int colon = s.lastIndexOf(':');
                    d.host = colon > 0 ? s.substring(0, colon) : s;
                    d.port = PORT;
                    try {
                        if (colon > 0) d.port = Integer.parseInt(s.substring(colon + 1));
                    } catch (NumberFormatException ignored) {
                    }
                    d.id = d.host + ":" + d.port;
                    d.name = d.host;
                    connectTo(d);
                })
                .setNegativeButton("Cancel", (di, w) -> dialog = null)
                .setOnCancelListener(di -> dialog = null)
                .show();
    }

    @Override
    public void toggleKeyboard() {
        InputMethodManager imm = (InputMethodManager) getSystemService(Context.INPUT_METHOD_SERVICE);
        if (imm == null) return;
        view.requestFocus();
        if (keyboardShown) {
            imm.hideSoftInputFromWindow(view.getWindowToken(), 0);
        } else {
            imm.restartInput(view);
            imm.showSoftInput(view, InputMethodManager.SHOW_FORCED);
        }
        keyboardShown = !keyboardShown;
    }

    @Override
    public void disconnect() {
        userLeft = true;
        LinkClient c = client;
        if (c != null) c.close();
    }

    // ---- LinkClient.Listener (called on the connection's thread) ----

    @Override
    public void onNeedPair(String device, int left, boolean wrong) {
        ui.post(() -> {
            LinkClient c = client;
            if (c == null) return;
            quiet = false;
            final EditText e = new EditText(this);
            e.setInputType(InputType.TYPE_CLASS_NUMBER);
            e.setHint("1234");
            String msg = (wrong ? "That wasn't the code. " + left + " tries left.\n\n" : "")
                    + "Type the 4-digit code shown on " + device + ".";
            if (dialog != null) dialog.dismiss();
            dialog = new AlertDialog.Builder(this).setTitle("Pair with " + device)
                    .setMessage(msg).setView(e).setCancelable(false)
                    .setPositiveButton("Pair", (di, w) -> {
                        dialog = null;
                        c.sendPair(e.getText().toString().trim());
                    })
                    .setNegativeButton("Cancel", (di, w) -> {
                        dialog = null;
                        userLeft = true;
                        c.close();
                    })
                    .show();
        });
    }

    @Override
    public void onWelcome(String device, String token, String deviceId) {
        LinkClient c = client;
        if (c == null) return;
        String id = deviceId.isEmpty() ? c.id : deviceId;
        SharedPreferences.Editor ed = prefs.edit().putString("last_name", device).putString("last_id", id)
                .putString("last_host", c.host).putInt("last_port", c.port);
        if (!token.isEmpty()) {
            ed.putString("token_" + id, token).putString("token_" + c.id, token)
                    .putString("token_host_" + c.host + ":" + c.port, token);
        }
        ed.apply();
        ui.post(() -> {
            welcomed = true;
            quiet = false;
            if (dialog != null) {
                dialog.dismiss();
                dialog = null;
            }
        });
    }

    @Override
    public void onFrame() {
        view.frame();
    }

    @Override
    public void onClosed(String why, boolean denied) {
        ui.post(() -> {
            LinkClient c = client;
            client = null;
            if (dialog != null) {
                dialog.dismiss();
                dialog = null;
            }
            if (keyboardShown) toggleKeyboard();
            showSearching();
            if (denied) {
                userLeft = true;
                if (c != null) {
                    prefs.edit().remove("token_" + c.id).remove("token_host_" + c.host + ":" + c.port).apply();
                }
                view.status = "Not connected: " + why + "\nTap your Cardputer below to try again.";
            } else if (c != null && !userLeft && welcomed) {
                view.status = "Lost the connection (" + why + ").\nReconnecting when the Cardputer shows up...";
            } else if (c != null && !quiet && !userLeft) {
                view.status = "Couldn't connect to " + c.host + " (" + why + ").\nStill looking...";
            }
            welcomed = false;
            quiet = false;
            view.invalidate();
        });
    }

    // ---- Discovery.Listener ----

    @Override
    public void onDevices(List<Discovery.Device> devices) {
        ui.post(() -> {
            view.devices = devices;
            if (client == null) view.invalidate();
        });
    }

    // ---- hardware keys (a Bluetooth keyboard, steering-wheel arrows) ----

    @Override
    public boolean dispatchKeyEvent(KeyEvent e) {
        LinkClient c = client;
        if (c == null || dialog != null) return super.dispatchKeyEvent(e);
        int code = linuxCode(e.getKeyCode());
        boolean down = e.getAction() == KeyEvent.ACTION_DOWN;
        if (code != 0) {
            if (e.getAction() == KeyEvent.ACTION_MULTIPLE) return true;
            if (down && e.getRepeatCount() > 0) return true; // the Cardputer repeats held keys itself
            c.sendKey(code, down);
            return true;
        }
        int ch = e.getUnicodeChar();
        if (ch > 0 && !Character.isISOControl(ch)) {
            if (down) c.sendText(new String(Character.toChars(ch)));
            return true;
        }
        return super.dispatchKeyEvent(e); // volume and the rest: the stereo's own
    }

    static int linuxCode(int android) {
        switch (android) {
            case KeyEvent.KEYCODE_DPAD_UP:
                return ScreenView.K_UP;
            case KeyEvent.KEYCODE_DPAD_DOWN:
                return ScreenView.K_DOWN;
            case KeyEvent.KEYCODE_DPAD_LEFT:
                return ScreenView.K_LEFT;
            case KeyEvent.KEYCODE_DPAD_RIGHT:
                return ScreenView.K_RIGHT;
            case KeyEvent.KEYCODE_DPAD_CENTER:
            case KeyEvent.KEYCODE_ENTER:
            case KeyEvent.KEYCODE_NUMPAD_ENTER:
                return ScreenView.K_ENTER;
            case KeyEvent.KEYCODE_BACK:
            case KeyEvent.KEYCODE_ESCAPE:
                return ScreenView.K_ESC;
            case KeyEvent.KEYCODE_TAB:
                return ScreenView.K_TAB;
            case KeyEvent.KEYCODE_DEL:
                return ScreenView.K_BACKSPACE;
        }
        return 0;
    }
}
