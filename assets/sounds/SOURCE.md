# Sounds

From "Nintendo DS System Sounds (DS) (gamerip) (2004)" on downloads.khinsider.com
(`game-soundtracks/album/nintendo-ds-system-music-ds-gamerip-2004`; WAVs from The Sounds Resource):
disc 1 is the DS menu (BIOS), disc 2 the DSi Internet Settings. The sounds are Nintendo's.

`tools/mksounds.py <folder of the album's FLAC files> [ffmpeg]` writes each `<event>.pcm` (16-bit
little-endian mono, 32 kHz), trimmed and levelled. Which DS sound plays for what:

| Event | DS sound | Used for |
| --- | --- | --- |
| move | 1-09 Hover | moving between apps and rows |
| tick | 1-12 Keyboard Down | typing, volume keys |
| bump | 1-11 Invalid | end of a list |
| select | 1-25 Select | choosing |
| back | 1-26 Settings Closecategory | leaving a page |
| open | 1-30 Settings Opencategory | opening a page |
| modal | 1-19 Pictochat Select | app details, questions |
| toggle-on / increase | 1-28 Settings Increase | switches on, sliders and choices up |
| toggle-off / decrease | 1-27 Settings Decrease | switches off, sliders and choices down |
| launch | 1-03 Confirm | starting an app |
| notice | 1-05 Downloadplay Found | messages |
| error | 1-08 Failure | errors |
| lock / unlock | 1-17 / 1-15 Pictochat Leaveroom / Enterroom | lock screen |
| alarm / alarm-exit | 1-02 Alarm Ring / 1-01 Alarm Exit | the alarm (looped), stopping it |
| startup / startup-birthday | 1-33 Startup / 1-34 Startup Birthday | start-up chime (on your birthday) |
| shutdown | 1-32 Shutdown | restart and turn off |
| loading | 2-11 Seq | one blip per turn of the yellow loading squares |
