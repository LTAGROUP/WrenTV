# Wren TV on Windows

1. Install ffmpeg (recommended: jellyfin-ffmpeg portable build from
   https://repo.jellyfin.org/?path=/ffmpeg/windows) and note the path to `ffmpeg.exe`/`ffprobe.exe`.
2. Copy `wrentv.exe` to `C:\Program Files\WrenTV\`.
3. From an elevated PowerShell:

   ```powershell
   & "C:\Program Files\WrenTV\wrentv.exe" install
   & "C:\Program Files\WrenTV\wrentv.exe" start
   ```

   This registers a Windows service named `wrentv` (and removes an old `haven` service) that starts automatically.
4. Edit `C:\ProgramData\WrenTV\wrentv.yaml` (created on first start) and set
   `transcode.ffmpeg_path` / `ffprobe_path` if ffmpeg is not on `PATH`, then
   `wrentv.exe restart`.
5. Allow TCP 32500 through Windows Firewall.

Hardware transcoding is auto-detected in this order: NVENC, Quick Sync, AMF.
Uninstall with `wrentv.exe stop` then `wrentv.exe uninstall`.
