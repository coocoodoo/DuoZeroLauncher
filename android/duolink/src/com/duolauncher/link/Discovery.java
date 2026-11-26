package com.duolauncher.link;

import android.content.Context;
import android.net.wifi.WifiManager;

import java.net.DatagramPacket;
import java.net.DatagramSocket;
import java.net.InetSocketAddress;
import java.nio.charset.StandardCharsets;
import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;

import org.json.JSONObject;

/** Listens for Cardputers announcing Duo Link on the network ("DUOLINK1 {json}" on UDP 47141). */
final class Discovery {
    static final int PORT = 47141;

    static final class Device {
        String id, name, host;
        int port;
        long seen;
    }

    interface Listener {
        void onDevices(List<Device> devices);
    }

    private final Context ctx;
    private final Listener listener;
    private final Map<String, Device> found = new LinkedHashMap<>();
    private volatile DatagramSocket sock;
    private volatile boolean running;
    private WifiManager.MulticastLock lock;

    Discovery(Context ctx, Listener l) {
        this.ctx = ctx.getApplicationContext();
        this.listener = l;
    }

    void start() {
        if (running) return;
        running = true;
        try {
            WifiManager wm = (WifiManager) ctx.getSystemService(Context.WIFI_SERVICE);
            if (wm != null) {
                lock = wm.createMulticastLock("duolink"); // some devices drop broadcasts without it
                lock.setReferenceCounted(false);
                lock.acquire();
            }
        } catch (RuntimeException ignored) {
        }
        Thread t = new Thread(this::loop, "duolink-discovery");
        t.setDaemon(true);
        t.start();
    }

    void stop() {
        running = false;
        DatagramSocket s = sock;
        if (s != null) s.close();
        if (lock != null && lock.isHeld()) lock.release();
    }

    List<Device> devices() {
        synchronized (found) {
            long now = System.currentTimeMillis();
            List<Device> out = new ArrayList<>();
            for (Device d : found.values()) {
                if (now - d.seen < 10000) out.add(d);
            }
            return out;
        }
    }

    private void loop() {
        byte[] buf = new byte[2048];
        while (running) {
            try {
                DatagramSocket s = new DatagramSocket(null);
                s.setReuseAddress(true);
                s.setBroadcast(true);
                s.bind(new InetSocketAddress(PORT));
                sock = s;
                while (running) {
                    DatagramPacket p = new DatagramPacket(buf, buf.length);
                    s.receive(p);
                    String msg = new String(p.getData(), 0, p.getLength(), StandardCharsets.UTF_8);
                    if (!msg.startsWith("DUOLINK1 ")) continue;
                    JSONObject j = new JSONObject(msg.substring(9));
                    Device d = new Device();
                    d.host = p.getAddress().getHostAddress();
                    d.port = j.optInt("port", 47140);
                    d.name = j.optString("name", d.host);
                    d.id = j.optString("id", d.host + ":" + d.port);
                    d.seen = System.currentTimeMillis();
                    synchronized (found) {
                        found.put(d.id, d);
                    }
                    listener.onDevices(devices());
                }
            } catch (Exception e) {
                if (!running) return;
                try {
                    Thread.sleep(2000);
                } catch (InterruptedException ie) {
                    return;
                }
            } finally {
                DatagramSocket s = sock;
                if (s != null) s.close();
            }
        }
    }
}
