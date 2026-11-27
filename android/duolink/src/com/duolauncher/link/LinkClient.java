package com.duolauncher.link;

import android.content.Context;

import java.io.BufferedInputStream;
import java.io.DataInputStream;
import java.io.DataOutputStream;
import java.io.IOException;
import java.net.InetSocketAddress;
import java.net.Socket;
import java.nio.charset.StandardCharsets;
import java.util.concurrent.LinkedBlockingQueue;
import java.util.zip.DataFormatException;
import java.util.zip.Inflater;

import org.json.JSONException;
import org.json.JSONObject;

/**
 * The Duo Link connection to one Cardputer over Wi-Fi (the protocol is described in link.go in
 * Duo's source). One thread reads, one thread writes; the screen is kept in {@link #px}.
 */
final class LinkClient {
    // client -> Cardputer
    static final int HELLO = 1, PAIR = 2, KEY = 3, TEXT = 4, PING = 5, ACK = 6;
    // Cardputer -> client
    static final int WELCOME = 0x81, NEEDPAIR = 0x82, DENIED = 0x83, FRAME = 0x84, PONG = 0x85, WRONGCODE = 0x86, AUDIO = 0x87;

    interface Listener {
        void onNeedPair(String device, int triesLeft, boolean wrong);
        void onWelcome(String device, String token, String deviceId);
        void onFrame();
        void onClosed(String why, boolean denied);
    }

    final String host;
    final int port;
    final String id; // the Cardputer's id from its announcement (or host:port)
    volatile int w = 320, h = 170;
    volatile short[] px = new short[320 * 170];
    final Object pxLock = new Object();

    private final Listener listener;
    private final String token, myName;
    private final LinkedBlockingQueue<byte[]> out = new LinkedBlockingQueue<>();
    private volatile Socket sock;
    private final AudioPlayer audio = new AudioPlayer();
    private final Context ctx; // finds the Wi-Fi network for the socket
    private volatile boolean closed;
    private volatile boolean denied;

    LinkClient(Context ctx, String host, int port, String id, String token, String myName, Listener l) {
        this.ctx = ctx;
        this.host = host;
        this.port = port;
        this.id = id;
        this.token = token;
        this.myName = myName;
        this.listener = l;
    }

    void start() {
        Thread r = new Thread(this::readLoop, "duolink-read");
        r.setDaemon(true);
        r.start();
    }

    void close() {
        closed = true;
        audio.stop();
        Socket s = sock;
        if (s != null) {
            try {
                s.close();
            } catch (IOException ignored) {
            }
        }
        out.offer(new byte[0]); // wakes the writer
    }

    boolean isClosed() {
        return closed;
    }

    void send(int type, byte[] payload) {
        if (closed) return;
        byte[] m = new byte[5 + payload.length];
        m[0] = (byte) type;
        int n = payload.length;
        m[1] = (byte) (n >>> 24);
        m[2] = (byte) (n >>> 16);
        m[3] = (byte) (n >>> 8);
        m[4] = (byte) n;
        System.arraycopy(payload, 0, m, 5, n);
        out.offer(m);
    }

    void sendKey(int code, boolean down) {
        send(KEY, new byte[] {(byte) (code >>> 8), (byte) code, (byte) (down ? 1 : 0)});
    }

    void sendText(String s) {
        if (!s.isEmpty()) send(TEXT, s.getBytes(StandardCharsets.UTF_8));
    }

    void sendPair(String code) {
        JSONObject j = new JSONObject();
        try {
            j.put("code", code);
        } catch (JSONException ignored) {
        }
        send(PAIR, j.toString().getBytes(StandardCharsets.UTF_8));
    }

    private void writeLoop(DataOutputStream o) {
        try {
            while (!closed) {
                byte[] m = out.take();
                if (m.length == 0) break;
                o.write(m);
                if (out.isEmpty()) o.flush();
            }
        } catch (IOException | InterruptedException e) {
            close();
        }
    }

    private void readLoop() {
        String why = "Connection closed";
        try {
            Socket s = WifiNet.socket(ctx);
            sock = s;
            s.setTcpNoDelay(true);
            s.connect(new InetSocketAddress(host, port), 5000);
            s.setSoTimeout(15000); // the Cardputer sends at least a PONG every few seconds
            java.io.InputStream rawIn = s.getInputStream();
            java.io.OutputStream rawOut = s.getOutputStream();
            DataOutputStream o = new DataOutputStream(new java.io.BufferedOutputStream(rawOut, 65536));
            o.write("DLK1".getBytes(StandardCharsets.US_ASCII));
            o.flush();
            Thread wt = new Thread(() -> writeLoop(o), "duolink-write");
            wt.setDaemon(true);
            wt.start();
            JSONObject hello = new JSONObject();
            hello.put("name", myName);
            hello.put("token", token == null ? "" : token);
            hello.put("v", 2);
            hello.put("audio", true);
            send(HELLO, hello.toString().getBytes(StandardCharsets.UTF_8));

            DataInputStream in = new DataInputStream(new BufferedInputStream(rawIn, 65536));
            Inflater inf = new Inflater();
            byte[] tiles = new byte[0];
            long lastPing = System.currentTimeMillis();
            while (!closed) {
                int type = in.readUnsignedByte();
                int n = in.readInt();
                if (n < 0 || n > 8 << 20) throw new IOException("bad message");
                byte[] p = new byte[n];
                in.readFully(p);
                switch (type) {
                    case WELCOME: {
                        JSONObject j = new JSONObject(new String(p, StandardCharsets.UTF_8));
                        int nw = j.optInt("w", 320), nh = j.optInt("h", 170);
                        if (nw > 0 && nh > 0 && nw * nh <= 4 << 20) {
                            synchronized (pxLock) {
                                w = nw;
                                h = nh;
                                px = new short[nw * nh];
                            }
                        }
                        listener.onWelcome(j.optString("name", host), j.optString("token", ""), j.optString("id", ""));
                        break;
                    }
                    case NEEDPAIR:
                    case WRONGCODE: {
                        JSONObject j = new JSONObject(new String(p, StandardCharsets.UTF_8));
                        listener.onNeedPair(j.optString("name", host), j.optInt("left", 5), type == WRONGCODE);
                        break;
                    }
                    case DENIED: {
                        JSONObject j = new JSONObject(new String(p, StandardCharsets.UTF_8));
                        why = j.optString("reason", "The Cardputer said no");
                        denied = true;
                        closed = true;
                        break;
                    }
                    case FRAME: {
                        if (n < 4) break;
                        int seq = ((p[0] & 0xff) << 24) | ((p[1] & 0xff) << 16) | ((p[2] & 0xff) << 8) | (p[3] & 0xff);
                        inf.reset();
                        inf.setInput(p, 4, n - 4);
                        int got = 0;
                        if (tiles.length < 2 * w * h + 4096) tiles = new byte[2 * w * h + 4096];
                        while (!inf.finished()) {
                            if (got == tiles.length) {
                                byte[] t = new byte[tiles.length * 2];
                                System.arraycopy(tiles, 0, t, 0, got);
                                tiles = t;
                            }
                            int k = inf.inflate(tiles, got, tiles.length - got);
                            if (k == 0 && (inf.needsInput() || inf.needsDictionary())) break;
                            got += k;
                        }
                        applyTiles(tiles, got);
                        send(ACK, new byte[] {(byte) (seq >>> 24), (byte) (seq >>> 16), (byte) (seq >>> 8), (byte) seq});
                        listener.onFrame();
                        break;
                    }
                    case AUDIO:
                        audio.feed(p);
                        break;
                    default: // PONG and anything newer
                        break;
                }
                long now = System.currentTimeMillis();
                if (now - lastPing > 4000) {
                    send(PING, new byte[0]);
                    lastPing = now;
                }
            }
        } catch (IOException e) {
            if (!closed) why = e.getMessage() == null ? "Connection lost" : e.getMessage();
        } catch (JSONException | DataFormatException e) {
            why = "The Cardputer sent something this app doesn't understand";
        }
        boolean wasDenied = denied;
        close();
        listener.onClosed(why, wasDenied);
    }

    /** tiles: repeated x, y, w, h (16-bit, big-endian) then w*h RGB565 pixels (little-endian). */
    private void applyTiles(byte[] b, int n) {
        synchronized (pxLock) {
            short[] dst = px;
            int i = 0;
            while (i + 8 <= n) {
                int x = ((b[i] & 0xff) << 8) | (b[i + 1] & 0xff);
                int y = ((b[i + 2] & 0xff) << 8) | (b[i + 3] & 0xff);
                int tw = ((b[i + 4] & 0xff) << 8) | (b[i + 5] & 0xff);
                int th = ((b[i + 6] & 0xff) << 8) | (b[i + 7] & 0xff);
                i += 8;
                if (x + tw > w || y + th > h || i + 2 * tw * th > n) return;
                for (int r = 0; r < th; r++) {
                    int d = (y + r) * w + x;
                    for (int c = 0; c < tw; c++, i += 2) {
                        dst[d + c] = (short) ((b[i] & 0xff) | ((b[i + 1] & 0xff) << 8));
                    }
                }
            }
        }
    }
}
