# Haven on Windows

1. Install ffmpeg (recommended: jellyfin-ffmpeg portable build from
   https://repo.jellyfin.org/?path=/ffmpeg/windows) and note the path to `ffmpeg.exe`/`ffprobe.exe`.
2. Copy `haven.exe` to `C:\Program Files\Haven\`.
3. From an elevated PowerShell:

   ```powershell
   & "C:\Program Files\Haven\haven.exe" install
   & "C:\Program Files\Haven\haven.exe" start
   ```

   This registers a Windows service named `haven` that starts automatically.
4. Edit `C:\ProgramData\Haven\haven.yaml` (created on first start) and set
   `transcode.ffmpeg_path` / `ffprobe_path` if ffmpeg is not on `PATH`, then
   `haven.exe restart`.
5. Allow TCP 32500 (and TCP/UDP 42069 if using built-in torrents) through Windows Firewall.

Hardware transcoding is auto-detected in this order: NVENC, Quick Sync, AMF.
Uninstall with `haven.exe stop` then `haven.exe uninstall`.
