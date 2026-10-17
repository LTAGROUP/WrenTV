#!/usr/bin/env python3
"""Copies libmpv and everything it loads from Homebrew into the app's
Frameworks folder and points every load command there, so the app runs on a
Mac without Homebrew.

    bundle_dylibs.py "Wren TV.app"
"""
import os
import shutil
import subprocess
import sys

HOMEBREW = "/opt/homebrew"


def run(*args):
    return subprocess.run(args, check=True, capture_output=True, text=True).stdout


def deps(path):
    """Install names this file loads, skipping its own id line."""
    lines = run("otool", "-L", path).splitlines()[1:]
    names = [l.strip().split(" (")[0] for l in lines]
    own = run("otool", "-D", path).splitlines()[1:]
    return [n for n in names if n not in own]


def rpaths(path):
    out, lines = [], run("otool", "-l", path).splitlines()
    for i, l in enumerate(lines):
        if l.strip() == "cmd LC_RPATH":
            out.append(lines[i + 2].strip().split(" ")[1])
    return out


def resolve(name, loader):
    """Where a load command points on this Mac, or None for system libraries."""
    base = os.path.dirname(os.path.realpath(loader))
    if name.startswith("@loader_path/"):
        return os.path.realpath(os.path.join(base, name[len("@loader_path/"):]))
    if name.startswith("@rpath/"):
        rest = name[len("@rpath/"):]
        for r in rpaths(loader) + [HOMEBREW + "/lib"]:
            r = r.replace("@loader_path", base)
            if os.path.exists(os.path.join(r, rest)):
                return os.path.realpath(os.path.join(r, rest))
        sys.exit(f"can't find {name} for {loader}")
    if name.startswith(HOMEBREW):
        return os.path.realpath(name)
    return None


def main(app):
    exe_dir = os.path.join(app, "Contents", "MacOS")
    frameworks = os.path.join(app, "Contents", "Frameworks")
    os.makedirs(frameworks, exist_ok=True)
    exe = os.path.join(exe_dir, os.listdir(exe_dir)[0])

    copied = {}  # real source path -> bundled file name
    queue = [exe]
    while queue:
        f = queue.pop()
        for name in deps(f):
            src = resolve(name, f)
            if src is None:
                continue
            if src not in copied:
                dest_name = os.path.basename(name)
                if dest_name in copied.values():
                    sys.exit(f"two libraries named {dest_name}")
                dest = os.path.join(frameworks, dest_name)
                shutil.copy2(src, dest)
                os.chmod(dest, 0o755)
                copied[src] = dest_name
                run("install_name_tool", "-id", "@rpath/" + dest_name, dest)
                queue.append(dest)
            run("install_name_tool", "-change", name, "@rpath/" + copied[src], f)

    # Nothing may look in Homebrew any more.
    for f in [exe] + [os.path.join(frameworks, n) for n in copied.values()]:
        for r in rpaths(f):
            if r.startswith(HOMEBREW) or r.startswith("@loader_path"):
                run("install_name_tool", "-delete_rpath", r, f)
        if f != exe:
            run("install_name_tool", "-add_rpath", "@loader_path", f)
        left = [d for d in deps(f) if d.startswith(HOMEBREW)]
        if left:
            sys.exit(f"{f} still loads {left}")
    print(f"bundled {len(copied)} libraries")


if __name__ == "__main__":
    main(sys.argv[1])
