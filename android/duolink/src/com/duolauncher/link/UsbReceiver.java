package com.duolauncher.link;

import android.content.BroadcastReceiver;
import android.content.Context;
import android.content.Intent;

/** USB permission answers and unplugging, passed on to the activity. */
public final class UsbReceiver extends BroadcastReceiver {
    private final MainActivity act;

    public UsbReceiver() {
        this.act = null;
    }

    UsbReceiver(MainActivity act) {
        this.act = act;
    }

    @Override
    public void onReceive(Context ctx, Intent intent) {
        if (act != null) act.onUsbIntent(intent);
    }
}
