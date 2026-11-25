"""Convert the Nintendo DS system sounds for the launcher.

usage: py tools/mksounds.py "<folder with the album's FLAC files>" [ffmpeg]
Source: "Nintendo DS System Sounds (DS) (gamerip) (2004)" on downloads.khinsider.com (WAVs from
The Sounds Resource): disc 1 = the DS menu (BIOS) sounds, disc 2 = DSi Internet Settings.
Files keep the album's names, e.g. "1-25. Select.flac". Needs ffmpeg to decode.
Writes assets/sounds/<event>.pcm: 16-bit little-endian mono at 32 kHz (the launcher's pacat
stream format), leading and trailing silence trimmed, levels brought into a common range."""
import os, subprocess, sys
import numpy as np

src = sys.argv[1]
ffmpeg = sys.argv[2] if len(sys.argv) > 2 else "ffmpeg"
here = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
out = os.path.join(here, "assets", "sounds")
os.makedirs(out, exist_ok=True)
RATE = 32000

# launcher event -> album file (by what the DS plays them for)
EVENTS = {
    "move": "1-09. Hover",                      # cursor moves over an icon
    "tick": "1-12. Keyboard Down",              # typing
    "bump": "1-11. Invalid",                    # end of a list / nothing there
    "select": "1-25. Select",                   # choose
    "back": "1-26. Settings Closecategory",     # leave a page
    "open": "1-30. Settings Opencategory",      # open a page
    "modal": "1-19. Pictochat Select",          # details / question pops up
    "toggle-on": "1-28. Settings Increase",
    "toggle-off": "1-27. Settings Decrease",
    "increase": "1-28. Settings Increase",      # sliders and choices
    "decrease": "1-27. Settings Decrease",
    "launch": "1-03. Confirm",                  # start an app
    "notice": "1-05. Downloadplay Found",       # message toast
    "error": "1-08. Failure",
    "lock": "1-17. Pictochat Leaveroom",
    "unlock": "1-15. Pictochat Enterroom",
    "alarm": "1-02. Alarm Ring",                # the DS alarm, looped while it rings
    "alarm-exit": "1-01. Alarm Exit",
    "startup": "1-33. Startup",
    "startup-birthday": "1-34. Startup Birthday",
    "shutdown": "1-32. Shutdown",
    "loading": "2-11. Seq",                     # ticking in two sine swells that circle you + a pause, looped
    # PictoChat
    "pc-send": "1-20. Pictochat Send",
    "pc-receive": "1-21. Pictochat Something0", # a guess: the album doesn't name it
    "pc-enter": "1-15. Pictochat Enterroom",
    "pc-leave": "1-17. Pictochat Leaveroom",
    "pc-clear": "1-14. Pictochat Clear",
}

def load(path):
    raw = subprocess.run([ffmpeg, "-v", "error", "-i", path, "-ac", "1", "-ar", str(RATE), "-f", "s16le", "-"],
                         capture_output=True, check=True).stdout
    a = np.frombuffer(raw, dtype="<i2").astype(np.float64)
    loud = np.nonzero(np.abs(a) > 60)[0]
    if len(loud):  # trim silence, keep 2 ms before and 10 ms after
        a = a[max(0, loud[0] - int(0.002 * RATE)): min(len(a), loud[-1] + int(0.01 * RATE))]
    peak = np.abs(a).max() / 32768
    if peak > 0:  # quiet clicks up to 0.25 peak, loud ones down to 0.6
        a *= min(max(peak, 0.25), 0.6) / peak
    return np.clip(np.round(a), -32768, 32767).astype("<i2")

LOADING_TURN = 0.5333          # one turn of the yellow loading squares (ds.go loadingTurn)
LOADING_STEP = LOADING_TURN / 8  # one step of them
LOADING_SWELL = 4               # ticks (steps) per swell: half a turn
LOADING_PAUSE = 0.25            # silence after each pair of swells
LOADING_DOPPLER = 0.05          # orbit: pitch +-5% as the ticks come round and go away
LOADING_BACK_HZ = 2000          # orbit: behind the head the ticks are muffled to this
LOADING_FRONT_HZ = 14000        # orbit: in front they are clear

def orbit(x):
    """Make one swell sound like it goes once round the listener, on a single speaker: it starts
    behind the head (far, muffled), swings round to the front (near, clear) and goes away behind
    again. Read through the travel delay of a source whose distance shrinks in front and grows
    behind, the pitch bends up as it comes round and down as it leaves (Doppler), and a low-pass
    that opens in front and closes behind stands in for the head's shadow."""
    n = len(x)
    i = np.arange(n, dtype=np.float64)
    phi = 2 * np.pi * i / n                         # 0 = behind, pi = in front
    k = LOADING_DOPPLER * n / (2 * np.pi)           # delay swing in samples for that bend
    y = np.interp(i - k * (1 + np.cos(phi)), i, x, left=0.0)
    fc = LOADING_BACK_HZ * (LOADING_FRONT_HZ / LOADING_BACK_HZ) ** ((1 - np.cos(phi)) / 2)
    a = 1 - np.exp(-2 * np.pi * fc / RATE)
    out, s1, s2 = np.zeros(n), 0.0, 0.0
    for j in range(n):                              # two one-pole stages
        s1 += a[j] * (y[j] - s1)
        s2 += a[j] * (s1 - s2)
        out[j] = s2
    return out

def loading_loop(pcm):
    """The DSi blip is two clicks ~71 ms apart. The loop ticks once per loading-square step
    (the two clicks taking turns), its volume swelling from nothing to full and back like a
    sine, the way the DS does: two swells of four ticks (half a turn) each, then a quarter
    second of silence, and round again. Each swell also circles the listener (see orbit). The ticks sit half a step into each slot, so the
    loudest are in the middle and none falls on the silent ends of a swell."""
    a = pcm.astype(np.float64)
    loud = np.nonzero(np.abs(a) > 300)[0]
    starts = [loud[0]] + [i for p, i in zip(loud, loud[1:]) if i - p > int(0.01 * RATE)]
    assert len(starts) >= 2, "expected two clicks"
    clicks = [a[s:s + int(LOADING_STEP * RATE)] for s in starts[:2]]
    n = int(round(LOADING_SWELL * LOADING_STEP * RATE))
    swell = np.zeros(n)
    for k in range(LOADING_SWELL):  # one tick per step, half a step in
        at = int(round((k + 0.5) * LOADING_STEP * RATE))
        c = clicks[k % 2][: n - at]
        swell[at:at + len(c)] += c
    swell *= np.sin(np.pi * np.arange(n) / n)  # 0 -> full -> 0
    swell = orbit(swell)                       # once round the listener per swell
    loop = np.concatenate([swell, swell, np.zeros(int(round(LOADING_PAUSE * RATE)))])
    return np.clip(np.round(loop), -32768, 32767).astype("<i2")

for ev, name in EVENTS.items():
    pcm = load(os.path.join(src, name + ".flac"))
    if ev == "loading":
        pcm = loading_loop(pcm)
    open(os.path.join(out, ev + ".pcm"), "wb").write(pcm.tobytes())
    print(f"{ev:16s} <- {name:30s} {len(pcm)/RATE:5.2f}s")
