package com.duolauncher.link;

import android.content.Context;
import android.net.ConnectivityManager;
import android.net.Network;
import android.net.NetworkCapabilities;
import android.os.Build;

import java.io.IOException;
import java.net.Socket;

/**
 * Sockets over the stereo's Wi-Fi. The Cardputer's own Wi-Fi has no internet, so a stereo with
 * mobile data keeps that as its default network and a plain socket to 10.42.0.1 would go there;
 * a socket made by the Wi-Fi network goes over the Wi-Fi. With no Wi-Fi client network (the
 * stereo is a hotspot itself, say) a plain socket is right.
 */
final class WifiNet {
    private WifiNet() {
    }

    static Socket socket(Context ctx) throws IOException {
        Network n = wifi(ctx);
        if (n != null) {
            try {
                return n.getSocketFactory().createSocket();
            } catch (IOException | RuntimeException ignored) {
            }
        }
        return new Socket();
    }

    static Network wifi(Context ctx) {
        if (Build.VERSION.SDK_INT < 21 || ctx == null) return null;
        try {
            ConnectivityManager cm = (ConnectivityManager) ctx.getSystemService(Context.CONNECTIVITY_SERVICE);
            if (cm == null) return null;
            for (Network n : cm.getAllNetworks()) {
                NetworkCapabilities c = cm.getNetworkCapabilities(n);
                if (c != null && c.hasTransport(NetworkCapabilities.TRANSPORT_WIFI)) return n;
            }
        } catch (RuntimeException ignored) {
        }
        return null;
    }
}
