package com.duolauncher.link;

import android.content.Context;
import android.graphics.Bitmap;
import android.graphics.Canvas;
import android.graphics.Paint;
import android.graphics.Rect;
import android.graphics.RectF;
import android.text.InputType;
import android.util.SparseArray;
import android.view.KeyEvent;
import android.view.MotionEvent;
import android.view.View;
import android.view.inputmethod.BaseInputConnection;
import android.view.inputmethod.EditorInfo;
import android.view.inputmethod.InputConnection;

import java.nio.ShortBuffer;
import java.util.ArrayList;
import java.util.List;

/**
 * Draws the Cardputer's screen (or the "looking for a Cardputer" page) and the buttons beside it,
 * and turns touches into key presses: the buttons press and release Linux key codes, a tap on
 * Duo's key bar presses keys 4-8, and a swipe on the picture is an arrow key.
 */
final class ScreenView extends View {
    // Linux key codes, as the Cardputer's keyboard sends them
    static final int K_ESC = 1, K_TAB = 15, K_ENTER = 28, K_BACKSPACE = 14, K_UP = 103, K_DOWN = 108, K_LEFT = 105, K_RIGHT = 106;
    static final int KEYBOARD = -1, DISCONNECT = -2; // panel buttons that aren't keys

    static final class Button {
        final RectF r = new RectF();
        final String label;
        final int code;
        boolean down;

        Button(String label, int code) {
            this.label = label;
            this.code = code;
        }
    }

    interface Host {
        LinkClient client();

        void toggleKeyboard();

        void disconnect();

        void connectTo(Discovery.Device d);

        void askAddress();
    }

    private final Host host;
    private final Paint paint = new Paint();
    private final Paint text = new Paint(Paint.ANTI_ALIAS_FLAG);
    private final float dp;
    private Bitmap bmp;
    private final Rect src = new Rect();
    private final RectF dst = new RectF();
    private final List<Button> buttons = new ArrayList<>();
    private final SparseArray<Object> pointers = new SparseArray<>(); // pointer id -> Button, or float[] swipe start
    private int barKey = 0; // the key-bar key held down by a touch, 0 if none

    // the page shown while not connected
    String title = "Duo Link";
    String status = "";
    List<Discovery.Device> devices = new ArrayList<>();
    private final List<RectF> deviceRects = new ArrayList<>();
    private final RectF addressRect = new RectF();

    ScreenView(Context ctx, Host host) {
        super(ctx);
        this.host = host;
        dp = ctx.getResources().getDisplayMetrics().density;
        setFocusable(true);
        setFocusableInTouchMode(true);
        paint.setFilterBitmap(true);
        buttons.add(new Button("▲", K_UP));
        buttons.add(new Button("◀", K_LEFT));
        buttons.add(new Button("OK", K_ENTER));
        buttons.add(new Button("▶", K_RIGHT));
        buttons.add(new Button("▼", K_DOWN));
        buttons.add(new Button("Back", K_ESC));
        buttons.add(new Button("Tab", K_TAB));
        buttons.add(new Button("Keys", KEYBOARD));
        buttons.add(new Button("Leave", DISCONNECT));
    }

    private boolean connected() {
        LinkClient c = host.client();
        return c != null && !c.isClosed() && bmp != null;
    }

    /** Called when a frame arrived: copies the pixels into the bitmap. */
    void frame() {
        LinkClient c = host.client();
        if (c == null) return;
        synchronized (c.pxLock) {
            if (bmp == null || bmp.getWidth() != c.w || bmp.getHeight() != c.h) {
                bmp = Bitmap.createBitmap(c.w, c.h, Bitmap.Config.RGB_565);
            }
            bmp.copyPixelsFromBuffer(ShortBuffer.wrap(c.px));
        }
        postInvalidate();
    }

    void dropPicture() {
        bmp = null;
        postInvalidate();
    }

    private void layoutPanel(int vw, int vh) {
        float pw = Math.max(150 * dp, vw * 0.17f);
        float px0 = vw - pw;
        float pad = 6 * dp;
        float cell = Math.min((pw - 2 * pad) / 3, (vh - 2 * pad) / 6.6f);
        float x0 = px0 + (pw - 3 * cell) / 2, y0 = pad;
        // d-pad
        set(0, x0 + cell, y0, cell);
        set(1, x0, y0 + cell, cell);
        set(2, x0 + cell, y0 + cell, cell);
        set(3, x0 + 2 * cell, y0 + cell, cell);
        set(4, x0 + cell, y0 + 2 * cell, cell);
        // wide buttons
        float y = y0 + 3 * cell + pad * 2;
        for (int i = 5; i < 9; i++) {
            float bh = i == 8 ? cell * 0.7f : cell;
            buttons.get(i).r.set(x0 + 2, y + 2, x0 + 3 * cell - 2, y + bh - 2);
            y += bh;
        }
        // the picture: as big as fits beside the panel
        float aw = px0 - pad * 2, ah = vh - pad * 2;
        int w = bmp != null ? bmp.getWidth() : 320, h = bmp != null ? bmp.getHeight() : 170;
        float s = Math.min(aw / w, ah / h);
        if (s >= 2) s = (float) Math.floor(s * 2) / 2; // keep whole or half steps when there's room: crisper
        float dw = w * s, dh = h * s;
        dst.set(pad + (aw - dw) / 2, (vh - dh) / 2, pad + (aw - dw) / 2 + dw, (vh - dh) / 2 + dh);
        src.set(0, 0, w, h);
        paint.setFilterBitmap(s != Math.floor(s)); // whole steps: sharp pixels
    }

    private void set(int i, float x, float y, float cell) {
        buttons.get(i).r.set(x + 2, y + 2, x + cell - 2, y + cell - 2);
    }

    @Override
    protected void onDraw(Canvas c) {
        int vw = getWidth(), vh = getHeight();
        c.drawColor(0xff101418);
        if (!connected()) {
            drawSearch(c, vw, vh);
            return;
        }
        layoutPanel(vw, vh);
        c.drawBitmap(bmp, src, dst, paint);
        text.setTextAlign(Paint.Align.CENTER);
        for (Button b : buttons) {
            Paint p = new Paint(Paint.ANTI_ALIAS_FLAG);
            p.setColor(b.down ? 0xfffbd300 : (b.code == DISCONNECT ? 0xff2a3038 : 0xff3a4450));
            c.drawRoundRect(b.r, 8 * dp, 8 * dp, p);
            text.setColor(b.down ? 0xff101418 : 0xffffffff);
            text.setTextSize(Math.min(b.r.height() * 0.38f, 22 * dp));
            c.drawText(b.label, b.r.centerX(), b.r.centerY() + text.getTextSize() * 0.36f, text);
        }
    }

    private void drawSearch(Canvas c, int vw, int vh) {
        float x = 28 * dp, y = 48 * dp;
        text.setTextAlign(Paint.Align.LEFT);
        text.setColor(0xfffbd300);
        text.setTextSize(30 * dp);
        c.drawText(title, x, y, text);
        text.setColor(0xffffffff);
        text.setTextSize(17 * dp);
        y += 34 * dp;
        for (String line : status.split("\n")) {
            c.drawText(line, x, y, text);
            y += 24 * dp;
        }
        y += 10 * dp;
        deviceRects.clear();
        Paint p = new Paint(Paint.ANTI_ALIAS_FLAG);
        for (Discovery.Device d : devices) {
            RectF r = new RectF(x, y, Math.min(vw - x, x + 520 * dp), y + 52 * dp);
            p.setColor(0xff3a4450);
            c.drawRoundRect(r, 8 * dp, 8 * dp, p);
            text.setTextSize(20 * dp);
            c.drawText(d.name + "   (" + d.host + ")", r.left + 16 * dp, r.centerY() + 7 * dp, text);
            deviceRects.add(r);
            y += 62 * dp;
        }
        addressRect.set(x, vh - 70 * dp, x + 260 * dp, vh - 22 * dp);
        p.setColor(0xff2a3038);
        c.drawRoundRect(addressRect, 8 * dp, 8 * dp, p);
        text.setTextSize(17 * dp);
        c.drawText("Type an address", addressRect.left + 16 * dp, addressRect.centerY() + 6 * dp, text);
    }

    @Override
    public boolean onTouchEvent(MotionEvent e) {
        int a = e.getActionMasked();
        int idx = e.getActionIndex();
        if (!connected()) {
            if (a == MotionEvent.ACTION_UP) {
                float x = e.getX(), y = e.getY();
                for (int i = 0; i < deviceRects.size() && i < devices.size(); i++) {
                    if (deviceRects.get(i).contains(x, y)) {
                        host.connectTo(devices.get(i));
                        return true;
                    }
                }
                if (addressRect.contains(x, y)) host.askAddress();
            }
            return true;
        }
        LinkClient cl = host.client();
        switch (a) {
            case MotionEvent.ACTION_DOWN:
            case MotionEvent.ACTION_POINTER_DOWN: {
                int id = e.getPointerId(idx);
                float x = e.getX(idx), y = e.getY(idx);
                for (Button b : buttons) {
                    if (b.r.contains(x, y)) {
                        pointers.put(id, b);
                        b.down = true;
                        if (b.code > 0) cl.sendKey(b.code, true);
                        performHapticFeedback(android.view.HapticFeedbackConstants.VIRTUAL_KEY);
                        invalidate();
                        return true;
                    }
                }
                if (dst.contains(x, y)) {
                    int w = bmp.getWidth(), h = bmp.getHeight();
                    float bx = (x - dst.left) / dst.width() * w, by = (y - dst.top) / dst.height() * h;
                    if (by >= h * 132f / 170f && barKey == 0) { // Duo's key bar: keys 4 5 6 7 8 (codes 5-9)
                        barKey = 5 + Math.min(4, (int) (bx * 5 / w));
                        cl.sendKey(barKey, true);
                        pointers.put(id, "bar");
                        performHapticFeedback(android.view.HapticFeedbackConstants.VIRTUAL_KEY);
                    } else {
                        pointers.put(id, new float[] {x, y});
                    }
                }
                return true;
            }
            case MotionEvent.ACTION_UP:
            case MotionEvent.ACTION_POINTER_UP:
            case MotionEvent.ACTION_CANCEL: {
                if (a == MotionEvent.ACTION_CANCEL) {
                    for (int i = 0; i < pointers.size(); i++) release(cl, pointers.valueAt(i), e, -1);
                    pointers.clear();
                    return true;
                }
                int id = e.getPointerId(idx);
                Object o = pointers.get(id);
                pointers.remove(id);
                release(cl, o, e, idx);
                return true;
            }
        }
        return true;
    }

    private void release(LinkClient cl, Object o, MotionEvent e, int idx) {
        if (o instanceof Button) {
            Button b = (Button) o;
            b.down = false;
            if (b.code > 0) cl.sendKey(b.code, false);
            else if (b.code == KEYBOARD && idx >= 0) host.toggleKeyboard();
            else if (b.code == DISCONNECT && idx >= 0) host.disconnect();
            invalidate();
        } else if ("bar".equals(o)) {
            if (barKey != 0) cl.sendKey(barKey, false);
            barKey = 0;
        } else if (o instanceof float[] && idx >= 0) { // a swipe is an arrow key
            float[] s = (float[]) o;
            float dx = e.getX(idx) - s[0], dy = e.getY(idx) - s[1];
            float min = 40 * dp;
            int code = 0;
            if (Math.abs(dx) > Math.abs(dy) && Math.abs(dx) > min) code = dx > 0 ? K_RIGHT : K_LEFT;
            else if (Math.abs(dy) > min) code = dy > 0 ? K_DOWN : K_UP;
            if (code != 0) {
                cl.sendKey(code, true);
                cl.sendKey(code, false);
            }
        }
    }

    // ---- the on-screen keyboard types into the Cardputer ----

    @Override
    public boolean onCheckIsTextEditor() {
        return true;
    }

    @Override
    public InputConnection onCreateInputConnection(EditorInfo out) {
        out.inputType = InputType.TYPE_CLASS_TEXT | InputType.TYPE_TEXT_VARIATION_VISIBLE_PASSWORD | InputType.TYPE_TEXT_FLAG_NO_SUGGESTIONS;
        out.imeOptions = EditorInfo.IME_FLAG_NO_EXTRACT_UI | EditorInfo.IME_FLAG_NO_FULLSCREEN | EditorInfo.IME_ACTION_NONE;
        return new Ime(this);
    }

    // static, not an inner or anonymous class: newer javac describes inner-class constructors in
    // a way this SDK's d8 crashes on
    private static final class Ime extends BaseInputConnection {
        private final ScreenView v;
        private final Host host;
        private String composing = "";

        Ime(ScreenView v) {
            super(v, false);
            this.v = v;
            this.host = v.host;
        }

            @Override
            public boolean commitText(CharSequence t, int pos) {
                LinkClient cl = host.client();
                if (cl != null) cl.sendText(t.toString());
                composing = "";
                return true;
            }

            @Override
            public boolean setComposingText(CharSequence t, int pos) {
                composing = t.toString();
                return true;
            }

            @Override
            public boolean finishComposingText() {
                LinkClient cl = host.client();
                if (cl != null && !composing.isEmpty()) cl.sendText(composing);
                composing = "";
                return true;
            }

            @Override
            public boolean deleteSurroundingText(int before, int after) {
                LinkClient cl = host.client();
                for (int i = 0; cl != null && i < Math.max(1, before); i++) {
                    cl.sendKey(K_BACKSPACE, true);
                    cl.sendKey(K_BACKSPACE, false);
                }
                return true;
            }

            @Override
            public boolean sendKeyEvent(KeyEvent ev) {
                return ((android.app.Activity) v.getContext()).dispatchKeyEvent(ev); // the IME's Enter and Delete
            }
    }
}
