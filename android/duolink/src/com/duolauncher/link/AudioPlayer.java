package com.duolauncher.link;

import android.media.AudioFormat;
import android.media.AudioManager;
import android.media.AudioTrack;

import java.util.concurrent.LinkedBlockingQueue;
import java.util.concurrent.TimeUnit;

/**
 * Plays the Cardputer's sound (s16le stereo 44.1 kHz pieces from the link) on the stereo. A
 * backlog of more than about 0.3 s is dropped, so the sound stays close to the picture.
 */
final class AudioPlayer {
    private final LinkedBlockingQueue<byte[]> queue = new LinkedBlockingQueue<>();
    private volatile boolean running;
    private AudioTrack track;
    private Thread thread;

    void start() {
        if (running) return;
        int min = AudioTrack.getMinBufferSize(44100, AudioFormat.CHANNEL_OUT_STEREO, AudioFormat.ENCODING_PCM_16BIT);
        try {
            track = new AudioTrack(AudioManager.STREAM_MUSIC, 44100, AudioFormat.CHANNEL_OUT_STEREO,
                    AudioFormat.ENCODING_PCM_16BIT, Math.max(min, 44100 * 4 / 5), AudioTrack.MODE_STREAM);
            track.play();
        } catch (RuntimeException e) {
            track = null;
            return;
        }
        running = true;
        thread = new Thread(this::loop, "duolink-audio");
        thread.setDaemon(true);
        thread.start();
    }

    void feed(byte[] pcm) {
        if (!running) start();
        if (queue.size() > 15) queue.clear(); // behind: skip ahead
        queue.offer(pcm);
    }

    void stop() {
        running = false;
        queue.clear();
        AudioTrack t = track;
        track = null;
        if (t != null) {
            try {
                t.pause();
                t.flush();
                t.release();
            } catch (RuntimeException ignored) {
            }
        }
    }

    private void loop() {
        while (running) {
            try {
                byte[] b = queue.poll(200, TimeUnit.MILLISECONDS);
                AudioTrack t = track;
                if (b != null && t != null) t.write(b, 0, b.length);
            } catch (InterruptedException e) {
                return;
            } catch (RuntimeException e) {
                return;
            }
        }
    }
}
