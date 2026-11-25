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
    "loading": "2-11. Seq",                     # looped, no gaps, while the loading squares show
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

LOADING_STEP = 0.5333 / 8  # one step of the yellow loading squares (ds.go loadingTurn / 8)

def loading_loop(pcm):
    """The DSi blip is two clicks ~71 ms apart. Make a gapless loop of them with each click
    starting one loading-square step after the last (click, pause, click, pause), so looped it
    ticks evenly, once per step of the squares."""
    a = pcm.astype(np.float64)
    loud = np.nonzero(np.abs(a) > 300)[0]
    starts = [loud[0]] + [i for p, i in zip(loud, loud[1:]) if i - p > int(0.01 * RATE)]
    assert len(starts) >= 2, "expected two clicks"
    total = int(round(2 * LOADING_STEP * RATE))
    first = total // 2
    loop = np.concatenate([a[starts[0]:starts[0] + first], a[starts[1]:starts[1] + total - first]])
    loop = np.pad(loop, (0, total - len(loop)))
    return np.clip(np.round(loop), -32768, 32767).astype("<i2")

for ev, name in EVENTS.items():
    pcm = load(os.path.join(src, name + ".flac"))
    if ev == "loading":
        pcm = loading_loop(pcm)
    open(os.path.join(out, ev + ".pcm"), "wb").write(pcm.tobytes())
    print(f"{ev:16s} <- {name:30s} {len(pcm)/RATE:5.2f}s")
