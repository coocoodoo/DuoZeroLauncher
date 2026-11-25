"""Convert the DSi UI sound pack for the launcher.

usage: py tools/mksounds.py "<folder with the DSi *.wav files>"
Writes assets/sounds/<event>.pcm: 16-bit little-endian mono at 32 kHz (the launcher's pacat
stream format), trailing silence trimmed."""
import os, sys, wave
import numpy as np

src = sys.argv[1]
here = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
out = os.path.join(here, "assets", "sounds")
os.makedirs(out, exist_ok=True)
RATE = 32000

# launcher event -> pack file (following the pack's own Steam Deck mapping)
EVENTS = {
    "move": "DSi Guide Move.wav",        # navigation
    "tick": "DSi Type.wav",              # typing
    "bump": "DSi Navigation 1.wav",      # end of a list (bumper end)
    "select": "DSi Select.wav",          # default activation / into detail
    "back": "DSi Back.wav",              # out of detail / hide modal
    "open": "DSi Settings.wav",          # side menu / tab transition
    "modal": "DSi Guide Select.wav",     # show modal
    "toggle-on": "DSi Guide Select.wav",
    "toggle-off": "DSi Guide Select.wav",
    "launch": "DSi Confirm.wav",         # launch game
    "notice": "DSi Online.wav",          # message toast
    "error": "DSi Navigation 3.wav",
    "lock": "DSi Settings.wav",
    "unlock": "DSi Confirm.wav",
    "alarm": "DSi Online.wav",
    "startup": "DSi Confirm.wav",
}

def load(path):
    w = wave.open(path)
    n, ch, sw, rate = w.getnframes(), w.getnchannels(), w.getsampwidth(), w.getframerate()
    assert sw == 2, "16-bit only"
    a = np.frombuffer(w.readframes(n), dtype="<i2").astype(np.float64).reshape(-1, ch).mean(axis=1)
    # resample by linear interpolation
    t = np.arange(0, len(a) * RATE / rate) * rate / RATE
    a = np.interp(t, np.arange(len(a)), a)
    # trim trailing near-silence, keep a short fade
    loud = np.nonzero(np.abs(a) > 60)[0]
    if len(loud):
        a = a[: min(len(a), loud[-1] + int(0.01 * RATE))]
    return np.clip(np.round(a), -32768, 32767).astype("<i2")

for ev, name in EVENTS.items():
    pcm = load(os.path.join(src, name))
    open(os.path.join(out, ev + ".pcm"), "wb").write(pcm.tobytes())
    print(f"{ev:11s} <- {name:22s} {len(pcm)/RATE:.2f}s")
