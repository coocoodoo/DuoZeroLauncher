package com.duolauncher.link;

import android.content.Context;
import android.hardware.usb.UsbConstants;
import android.hardware.usb.UsbDevice;
import android.hardware.usb.UsbDeviceConnection;
import android.hardware.usb.UsbEndpoint;
import android.hardware.usb.UsbInterface;
import android.hardware.usb.UsbManager;
import android.util.Base64;

import java.io.File;
import java.io.FileInputStream;
import java.io.FileOutputStream;
import java.io.IOException;
import java.io.InputStream;
import java.io.OutputStream;
import java.math.BigInteger;
import java.nio.ByteBuffer;
import java.nio.ByteOrder;
import java.nio.charset.StandardCharsets;
import java.security.KeyFactory;
import java.security.KeyPair;
import java.security.KeyPairGenerator;
import java.security.PrivateKey;
import java.security.interfaces.RSAPublicKey;
import java.security.spec.PKCS8EncodedKeySpec;
import java.security.spec.X509EncodedKeySpec;
import java.util.concurrent.LinkedBlockingQueue;
import java.util.concurrent.Semaphore;
import java.util.concurrent.TimeUnit;

import javax.crypto.Cipher;

/**
 * Duo Link over USB: the Cardputer (USB switch to the LEFT) is an ADB device, so this speaks
 * the ADB protocol over the stereo's USB host port and opens "tcp:47140" on the Cardputer,
 * which is Duo Link's port. The Cardputer needs no changes; the cable also charges it.
 * If the Cardputer's adbd asks for a key, this app's own RSA key is offered.
 */
final class AdbTransport {
    static final int A_CNXN = 0x4e584e43, A_OPEN = 0x4e45504f, A_OKAY = 0x59414b4f, A_CLSE = 0x45534c43,
            A_WRTE = 0x45545257, A_AUTH = 0x48545541;
    static final int VERSION = 0x01000001, MAXDATA = 256 * 1024;

    /** An ADB interface: vendor class, subclass 0x42, protocol 1. */
    static UsbInterface adbInterface(UsbDevice d) {
        for (int i = 0; i < d.getInterfaceCount(); i++) {
            UsbInterface f = d.getInterface(i);
            if (f.getInterfaceClass() == 0xff && f.getInterfaceSubclass() == 0x42 && f.getInterfaceProtocol() == 1) return f;
        }
        return null;
    }

    static UsbDevice findCardputer(UsbManager m) {
        if (m == null) return null;
        for (UsbDevice d : m.getDeviceList().values()) {
            if (adbInterface(d) != null) return d;
        }
        return null;
    }

    private final UsbDeviceConnection conn;
    private final UsbInterface intf;
    private UsbEndpoint in, out;
    private int maxData = 4096;
    private int remoteId;
    private final int localId = 1;
    private volatile boolean closed;
    private final Semaphore canWrite = new Semaphore(0);
    private final LinkedBlockingQueue<byte[]> incoming = new LinkedBlockingQueue<>();
    final InputStream input = new QueueInput(this);
    final OutputStream output = new AdbOutput(this);

    private AdbTransport(UsbDeviceConnection conn, UsbInterface intf) {
        this.conn = conn;
        this.intf = intf;
        for (int i = 0; i < intf.getEndpointCount(); i++) {
            UsbEndpoint e = intf.getEndpoint(i);
            if (e.getType() != UsbConstants.USB_ENDPOINT_XFER_BULK) continue;
            if (e.getDirection() == UsbConstants.USB_DIR_IN) in = e;
            else out = e;
        }
    }

    /** Connects to the Cardputer's adbd and opens service (like "tcp:47140"). */
    static AdbTransport open(Context ctx, UsbManager m, UsbDevice d, String service) throws IOException {
        UsbInterface f = adbInterface(d);
        if (f == null) throw new IOException("that USB device isn't a Cardputer");
        UsbDeviceConnection c = m.openDevice(d);
        if (c == null) throw new IOException("no permission for the USB device");
        if (!c.claimInterface(f, true)) {
            c.close();
            throw new IOException("the USB connection is busy");
        }
        AdbTransport t = new AdbTransport(c, f);
        try {
            t.handshake(ctx);
            t.openService(service);
        } catch (IOException e) {
            t.close();
            throw e;
        }
        Thread r = new Thread(t::readLoop, "duolink-adb");
        r.setDaemon(true);
        r.start();
        return t;
    }

    void close() {
        closed = true;
        incoming.offer(new byte[0]);
        canWrite.release(100);
        try {
            conn.releaseInterface(intf);
        } catch (RuntimeException ignored) {
        }
        conn.close();
    }

    // ---- messages ----

    private void send(int cmd, int arg0, int arg1, byte[] data) throws IOException {
        int len = data == null ? 0 : data.length;
        long sum = 0;
        for (int i = 0; i < len; i++) sum += data[i] & 0xff;
        ByteBuffer h = ByteBuffer.allocate(24).order(ByteOrder.LITTLE_ENDIAN);
        h.putInt(cmd).putInt(arg0).putInt(arg1).putInt(len).putInt((int) sum).putInt(cmd ^ 0xffffffff);
        bulkOut(h.array(), 24);
        if (len > 0) bulkOut(data, len);
    }

    private void bulkOut(byte[] b, int len) throws IOException {
        int done = 0;
        while (done < len) {
            int n = conn.bulkTransfer(out, b, done, Math.min(len - done, 16384), 5000);
            if (n < 0) throw new IOException("USB write failed");
            done += n;
        }
    }

    private byte[] bulkIn(int len, int timeout) throws IOException {
        byte[] b = new byte[len];
        int done = 0;
        while (done < len) {
            int n = conn.bulkTransfer(in, b, done, Math.min(len - done, 16384), timeout);
            if (n < 0) {
                if (closed) throw new IOException("closed");
                if (timeout > 0) throw new IOException("the Cardputer didn't answer over USB");
                continue;
            }
            done += n;
        }
        return b;
    }

    /** One message: [cmd, arg0, arg1, data]. */
    private Object[] read(int timeout) throws IOException {
        ByteBuffer h = ByteBuffer.wrap(bulkIn(24, timeout)).order(ByteOrder.LITTLE_ENDIAN);
        int cmd = h.getInt(), a0 = h.getInt(), a1 = h.getInt(), len = h.getInt();
        if (len < 0 || len > 1 << 20) throw new IOException("bad ADB message");
        byte[] data = len > 0 ? bulkIn(len, timeout) : new byte[0];
        return new Object[] {cmd, a0, a1, data};
    }

    private void handshake(Context ctx) throws IOException {
        send(A_CNXN, VERSION, MAXDATA, "host::duolink\0".getBytes(StandardCharsets.US_ASCII));
        boolean signed = false, offered = false;
        long until = System.currentTimeMillis() + 60000;
        while (System.currentTimeMillis() < until) {
            Object[] m = read(offered ? 30000 : 5000);
            int cmd = (Integer) m[0];
            if (cmd == A_CNXN) {
                maxData = Math.max(1024, Math.min((Integer) m[2], MAXDATA));
                return;
            }
            if (cmd == A_AUTH && (Integer) m[1] == 1) { // a token to sign
                KeyPair kp = AdbKey.get(ctx);
                if (!signed) {
                    send(A_AUTH, 2, 0, AdbKey.sign(kp.getPrivate(), (byte[]) m[3]));
                    signed = true;
                } else if (!offered) {
                    send(A_AUTH, 3, 0, AdbKey.publicKeyBlob((RSAPublicKey) kp.getPublic()));
                    offered = true; // the Cardputer has to accept it (once)
                } else {
                    throw new IOException("the Cardputer didn't accept this stereo's USB key");
                }
            }
        }
        throw new IOException("the Cardputer didn't answer over USB");
    }

    private void openService(String service) throws IOException {
        send(A_OPEN, localId, 0, (service + "\0").getBytes(StandardCharsets.US_ASCII));
        long until = System.currentTimeMillis() + 10000;
        while (System.currentTimeMillis() < until) {
            Object[] m = read(5000);
            int cmd = (Integer) m[0];
            if (cmd == A_OKAY && (Integer) m[2] == localId) {
                remoteId = (Integer) m[1];
                canWrite.release();
                return;
            }
            if (cmd == A_CLSE) throw new IOException("Duo Link isn't on: Settings > Duo Link on the Cardputer");
        }
        throw new IOException("the Cardputer didn't open Duo Link");
    }

    private void readLoop() {
        try {
            while (!closed) {
                Object[] m = read(0);
                int cmd = (Integer) m[0];
                if (cmd == A_WRTE) {
                    byte[] d = (byte[]) m[3];
                    if (d.length > 0) incoming.offer(d);
                    send(A_OKAY, localId, remoteId, null);
                } else if (cmd == A_OKAY) {
                    canWrite.release();
                } else if (cmd == A_CLSE) {
                    break;
                }
            }
        } catch (IOException ignored) {
        }
        closed = true;
        incoming.offer(new byte[0]);
        canWrite.release(100);
    }

    void write(byte[] b, int off, int len) throws IOException {
        while (len > 0) {
            if (closed) throw new IOException("the USB link closed");
            try {
                if (!canWrite.tryAcquire(10, TimeUnit.SECONDS)) throw new IOException("the USB link stalled");
            } catch (InterruptedException e) {
                throw new IOException("interrupted");
            }
            if (closed) throw new IOException("the USB link closed");
            int n = Math.min(len, maxData);
            byte[] chunk = new byte[n];
            System.arraycopy(b, off, chunk, 0, n);
            send(A_WRTE, localId, remoteId, chunk);
            off += n;
            len -= n;
        }
    }

    byte[] take(int timeoutMs) throws IOException {
        try {
            byte[] b = incoming.poll(timeoutMs, TimeUnit.MILLISECONDS);
            if (b == null) throw new IOException("the Cardputer went quiet");
            if (b.length == 0) {
                incoming.offer(b);
                return null; // closed
            }
            return b;
        } catch (InterruptedException e) {
            throw new IOException("interrupted");
        }
    }

    /** The stream from the Cardputer. */
    static final class QueueInput extends InputStream {
        private final AdbTransport t;
        private byte[] cur;
        private int pos;

        QueueInput(AdbTransport t) {
            this.t = t;
        }

        @Override
        public int read() throws IOException {
            byte[] one = new byte[1];
            return read(one, 0, 1) < 0 ? -1 : one[0] & 0xff;
        }

        @Override
        public int read(byte[] b, int off, int len) throws IOException {
            if (cur == null || pos >= cur.length) {
                cur = t.take(15000);
                pos = 0;
                if (cur == null) return -1;
            }
            int n = Math.min(len, cur.length - pos);
            System.arraycopy(cur, pos, b, off, n);
            pos += n;
            return n;
        }
    }

    /** The stream to the Cardputer. */
    static final class AdbOutput extends OutputStream {
        private final AdbTransport t;

        AdbOutput(AdbTransport t) {
            this.t = t;
        }

        @Override
        public void write(int b) throws IOException {
            write(new byte[] {(byte) b}, 0, 1);
        }

        @Override
        public synchronized void write(byte[] b, int off, int len) throws IOException {
            t.write(b, off, len);
        }
    }

    /** This app's ADB key (made once), and ADB's ways of using it. */
    static final class AdbKey {
        private static KeyPair pair;
        private static final byte[] SHA1_PREFIX = {0x30, 0x21, 0x30, 0x09, 0x06, 0x05, 0x2b, 0x0e, 0x03, 0x02, 0x1a, 0x05, 0x00, 0x04, 0x14};

        static synchronized KeyPair get(Context ctx) throws IOException {
            if (pair != null) return pair;
            File priv = new File(ctx.getFilesDir(), "adbkey"), pub = new File(ctx.getFilesDir(), "adbkey.pub");
            try {
                KeyFactory kf = KeyFactory.getInstance("RSA");
                if (priv.exists() && pub.exists()) {
                    pair = new KeyPair(kf.generatePublic(new X509EncodedKeySpec(readAll(pub))),
                            kf.generatePrivate(new PKCS8EncodedKeySpec(readAll(priv))));
                    return pair;
                }
                KeyPairGenerator g = KeyPairGenerator.getInstance("RSA");
                g.initialize(2048);
                pair = g.generateKeyPair();
                writeAll(priv, pair.getPrivate().getEncoded());
                writeAll(pub, pair.getPublic().getEncoded());
                return pair;
            } catch (java.security.GeneralSecurityException e) {
                throw new IOException("can't make a USB key: " + e.getMessage());
            }
        }

        /** ADB signs the token as if it were a SHA-1 hash: PKCS#1 v1.5 with SHA-1's DigestInfo. */
        static byte[] sign(PrivateKey k, byte[] token) throws IOException {
            try {
                Cipher c = Cipher.getInstance("RSA/ECB/PKCS1Padding");
                c.init(Cipher.ENCRYPT_MODE, k);
                c.update(SHA1_PREFIX);
                return c.doFinal(token);
            } catch (java.security.GeneralSecurityException e) {
                throw new IOException("can't sign: " + e.getMessage());
            }
        }

        /** The public key the way adbd wants it: Android's RSAPublicKey struct, base64, a name. */
        static byte[] publicKeyBlob(RSAPublicKey pub) {
            BigInteger n = pub.getModulus();
            int words = 2048 / 32;
            BigInteger r32 = BigInteger.ONE.shiftLeft(32);
            BigInteger n0inv = n.mod(r32).modInverse(r32).negate().mod(r32);
            BigInteger rr = BigInteger.ONE.shiftLeft(2048 * 2).mod(n);
            ByteBuffer b = ByteBuffer.allocate(4 + 4 + words * 4 * 2 + 4).order(ByteOrder.LITTLE_ENDIAN);
            b.putInt(words).putInt(n0inv.intValue());
            for (int i = 0; i < words; i++) b.putInt(n.shiftRight(32 * i).mod(r32).intValue());
            for (int i = 0; i < words; i++) b.putInt(rr.shiftRight(32 * i).mod(r32).intValue());
            b.putInt(pub.getPublicExponent().intValue());
            String s = Base64.encodeToString(b.array(), Base64.NO_WRAP) + " duolink@stereo\0";
            return s.getBytes(StandardCharsets.US_ASCII);
        }

        private static byte[] readAll(File f) throws IOException {
            byte[] b = new byte[(int) f.length()];
            try (FileInputStream in = new FileInputStream(f)) {
                int n = 0;
                while (n < b.length) {
                    int k = in.read(b, n, b.length - n);
                    if (k < 0) break;
                    n += k;
                }
            }
            return b;
        }

        private static void writeAll(File f, byte[] b) throws IOException {
            try (FileOutputStream out = new FileOutputStream(f)) {
                out.write(b);
            }
        }
    }
}
