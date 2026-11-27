#!/usr/bin/env bash
# Builds Duo Link (the Android car-stereo app) without Gradle: aapt2, javac, d8, zipalign,
# apksigner from the Android SDK. Writes ../../assets/duolink.apk, which Duo serves to the stereo.
#
#   ANDROID_HOME=~/Android/Sdk ./build.sh      (on Windows the SDK in %LOCALAPPDATA% is found)
#
# The APK is signed with ~/.android/duolink.keystore, made on the first build. Keep it: an update
# signed with another key won't install over the old app (uninstall it first).
set -euo pipefail
cd "$(dirname "$0")"

VERSION_CODE=3
VERSION_NAME=1.2
MIN_SDK=21
TARGET_SDK=34

SDK="${ANDROID_HOME:-${ANDROID_SDK_ROOT:-}}"
if [ -z "$SDK" ] && [ -n "${LOCALAPPDATA:-}" ]; then SDK="$(cygpath -u "$LOCALAPPDATA")/Android/Sdk"; fi
[ -d "$SDK" ] || { echo "Android SDK not found: set ANDROID_HOME" >&2; exit 1; }
BT="$SDK/build-tools/$(ls "$SDK/build-tools" | sort -V | tail -1)"
PLATFORM="$SDK/platforms/android-$TARGET_SDK/android.jar"
[ -f "$PLATFORM" ] || PLATFORM="$(ls -d "$SDK"/platforms/android-*/android.jar | sort -V | tail -1)"
exe() { if [ -f "$BT/$1.exe" ]; then echo "$BT/$1.exe"; else echo "$BT/$1"; fi; }

OUT=build
rm -rf "$OUT"
mkdir -p "$OUT/classes" "$OUT/dex"

"$(exe aapt2)" compile --dir res -o "$OUT/res.zip"
"$(exe aapt2)" link -o "$OUT/base.apk" -I "$PLATFORM" --manifest AndroidManifest.xml "$OUT/res.zip" \
	--min-sdk-version $MIN_SDK --target-sdk-version $TARGET_SDK \
	--version-code $VERSION_CODE --version-name $VERSION_NAME

javac -encoding UTF-8 --release 8 -nowarn -Xlint:-options -cp "$PLATFORM" -d "$OUT/classes" $(find src -name '*.java')
java -cp "$BT/lib/d8.jar" com.android.tools.r8.D8 --release --min-api $MIN_SDK --lib "$PLATFORM" \
	--output "$OUT/dex" $(find "$OUT/classes" -name '*.class')

python - "$OUT/base.apk" "$OUT/dex/classes.dex" "$OUT/unsigned.apk" <<'EOF'
import sys, zipfile, shutil
base, dex, out = sys.argv[1:]
shutil.copy(base, out)
with zipfile.ZipFile(out, 'a', zipfile.ZIP_DEFLATED) as z:
    z.write(dex, 'classes.dex')
EOF
"$(exe zipalign)" -p -f 4 "$OUT/unsigned.apk" "$OUT/aligned.apk"

KS="$HOME/.android/duolink.keystore"
if [ ! -f "$KS" ]; then
	mkdir -p "$(dirname "$KS")"
	keytool -genkeypair -keystore "$KS" -alias duolink -keyalg RSA -keysize 2048 -validity 10000 \
		-storepass duolink -keypass duolink -dname "CN=Duo Link" >/dev/null
fi
java -jar "$BT/lib/apksigner.jar" sign --ks "$KS" --ks-pass pass:duolink --key-pass pass:duolink \
	--out "$OUT/duolink.apk" "$OUT/aligned.apk"
java -jar "$BT/lib/apksigner.jar" verify "$OUT/duolink.apk"
cp "$OUT/duolink.apk" ../../assets/duolink.apk
ls -l ../../assets/duolink.apk
