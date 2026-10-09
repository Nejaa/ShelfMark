#!/usr/bin/env python3
"""Package a native helper and its private runtime for embedding by the Go server."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import struct
import subprocess
import tarfile
import tempfile

# glibc and the ELF loader stay supplied by the OS: mixing their versions is unsafe.
SYSTEM_LIBS = re.compile(r"^(?:ld-linux.*|lib(?:c|m|dl|pthread|rt|resolv|util|anl|nss_.*)\.so(?:\..*)?)$")


def run(args, env=None):
    return subprocess.check_output(args, env=env, text=True, stderr=subprocess.STDOUT)


def package_linux(helper, root, stage):
    arch = run(["gcc", "-dumpmachine"]).strip()
    libdirs = [root / "usr/lib" / arch, root / "lib" / arch, root / "usr/lib64", root / "usr/lib", root / "lib"]
    env = os.environ.copy()
    env["LD_LIBRARY_PATH"] = ":".join(str(path) for path in libdirs if path.is_dir())
    copied, inventory, originals = {}, [], {}

    def copy(source, dest):
        source = source.resolve()
        target = stage / dest
        if target.exists():
            return target
        target.parent.mkdir(parents=True, exist_ok=True)
        shutil.copy2(source, target)
        originals[dest] = source
        inventory.append({"file": dest, "source": str(source), "sha256": hashlib.sha256(source.read_bytes()).hexdigest()})
        return target

    def dependencies(binary):
        output = run(["ldd", str(binary)], env)
        if "not found" in output:
            raise RuntimeError(f"Unresolved dependency of {binary}:\n{output}")
        for line in output.splitlines():
            match = re.match(r"\s*(\S+) => (/\S+) \(", line)
            if not match:
                continue
            name, source = match.groups()
            if SYSTEM_LIBS.match(name) or name in copied:
                continue
            copied[name] = source
            copy(Path(source), "lib/" + name)
            dependencies(Path(source))

    copy(helper, "shelfmark-window")
    dependencies(helper)
    webkit = next((path / "webkit2gtk-4.0" for path in libdirs if (path / "webkit2gtk-4.0/WebKitWebProcess").exists()), None)
    if not webkit:
        raise RuntimeError("WebKitWebProcess was not found in the runtime root")
    for name in ("WebKitWebProcess", "WebKitNetworkProcess", "WebKitGPUProcess"):
        binary = webkit / name
        if binary.exists():
            copy(binary, "w/" + name)
            dependencies(binary)
    for binary in (webkit / "injected-bundle").glob("*.so"):
        copy(binary, "w/injected-bundle/" + binary.name)
        dependencies(binary)
    for name in ("bwrap", "xdg-dbus-proxy"):
        binary = root / "usr/bin" / name
        if not binary.exists(): binary = Path("/usr/bin") / name
        if not binary.exists():
            raise RuntimeError(f"Missing WebKit sandbox executable: {binary}")
        copy(binary, "bin/" + name)
        dependencies(binary)

    # GLVND loads its vendor with dlopen; WebKit initializes EGL even when
    # accelerated page compositing is disabled. Ship Mesa's software renderer.
    for name in ("libEGL_mesa.so.0", "libGLX_mesa.so.0"):
        binary = next((path / name for path in libdirs if (path / name).exists()), None)
        if binary is None:
            raise RuntimeError(f"Install Mesa runtime libraries to bundle {name}")
        copy(binary, "lib/" + name)
        dependencies(binary)
    software = next((path / "dri/swrast_dri.so" for path in libdirs if (path / "dri/swrast_dri.so").exists()), None)
    if software is None:
        raise RuntimeError("Install libgl1-mesa-dri to bundle software rendering")
    copy(software, "dri/swrast_dri.so")
    dependencies(software)
    (stage / "egl-mesa.json").write_text(json.dumps({"file_format_version":"1.0.0", "ICD":{"library_path":"libEGL_mesa.so.0"}}))

    # Modules loaded with dlopen are not visible in the main executable's ldd output.
    for directory, dest in (("gio/modules", "gio"), ("gdk-pixbuf-2.0/2.10.0/loaders", "pixbuf")):
        for libdir in libdirs:
            for binary in (libdir / directory).glob("*.so"):
                copy(binary, dest + "/" + binary.name)
                dependencies(binary)
    query = next((path / "gdk-pixbuf-2.0/gdk-pixbuf-query-loaders" for path in libdirs if (path / "gdk-pixbuf-2.0/gdk-pixbuf-query-loaders").exists()), None)
    if query and (stage / "pixbuf").exists():
        cache = run([str(query), *map(str, sorted((stage / "pixbuf").glob("*.so")))], env)
        (stage / "pixbuf/loaders.cache").write_text(cache.replace(str(stage / "pixbuf"), "./pixbuf"))
    keyboard = root / "usr/share/X11/xkb"
    if not keyboard.exists():
        keyboard = Path("/usr/share/X11/xkb")
    if not keyboard.exists():
        raise RuntimeError("Install xkb-data to bundle keyboard layouts")
    shutil.copytree(keyboard, stage / "share/X11/xkb", symlinks=False)
    schemas = root / "usr/share/glib-2.0/schemas"
    if not schemas.exists():
        raise RuntimeError("GSettings schemas were not found")
    shutil.copytree(schemas, stage / "share/glib-2.0/schemas", symlinks=False)
    compiler = root / "usr/bin/glib-compile-schemas"
    if not compiler.exists():
        compiler = Path(shutil.which("glib-compile-schemas") or "")
    run([str(compiler), str(stage / "share/glib-2.0/schemas")], env)
    # Built-in GTK resources supply Adwaita; include icons and a basic font family.
    icons = root / "usr/share/icons/Adwaita"
    if icons.exists():
        shutil.copytree(icons, stage / "share/icons/Adwaita", symlinks=False)
    fonts = root / "usr/share/fonts/truetype/dejavu"
    if not fonts.exists():
        fonts = Path("/usr/share/fonts/truetype/dejavu")
    for name in ("DejaVuSans.ttf", "DejaVuSans-Bold.ttf", "DejaVuSansMono.ttf"):
        if (fonts / name).exists():
            copy(fonts / name, "fonts/" + name)
    (stage / "fonts.conf").write_text('<?xml version="1.0"?><!DOCTYPE fontconfig SYSTEM "fonts.dtd"><fontconfig><dir prefix="relative">fonts</dir><dir>/usr/share/fonts</dir><dir>/usr/local/share/fonts</dir><cachedir prefix="xdg">fontconfig</cachedir></fontconfig>')

    # Release WebKit builds ignore WEBKIT_EXEC_PATH. Record complete ELF strings
    # for relocation at extraction, leaving offsets and string table sizes intact.
    prefixes = {
        "/usr/lib/" + arch + "/webkit2gtk-4.0": "${ROOT}/w",
        "/usr/lib/webkit2gtk-4.0": "${ROOT}/w",
        "/usr/libexec/webkit2gtk-4.0": "${ROOT}/w",
        "/usr/bin/bwrap": "./bin/bwrap",
        "/usr/bin/xdg-dbus-proxy": "./bin/xdg-dbus-proxy",
    }
    patches = []
    for dest, source in originals.items():
        content = source.read_bytes()
        if not content.startswith(b"\x7fELF"):
            continue
        for prefix, replacement in prefixes.items():
            for old in sorted(set(re.findall(re.escape(prefix.encode()) + rb"[^\x00]*\x00", content))):
                try:
                    text = old[:-1].decode("ascii")
                except UnicodeDecodeError:
                    continue
                patches.append({"File": dest, "Old": text, "New": replacement + text[len(prefix):]})
    if not any(patch["Old"] == str(Path("/usr/lib") / arch / "webkit2gtk-4.0") for patch in patches):
        raise RuntimeError("Unrecognized WebKit subprocess path; this runtime needs packaging support")
    (stage / "relocations.json").write_text(json.dumps(patches, indent=2))
    (stage / "runtime-manifest.json").write_text(json.dumps(inventory, indent=2))

    package_versions = {"host": run(["dpkg-query", "-W", "-f", "${binary:Package}\t${Version}\t${source:Package}\t${source:Version}\n"])}
    debs = root.parent / "debs"
    if debs.is_dir():
        package_versions["sysroot"] = [run(["dpkg-deb", "-f", str(deb), "Package", "Version", "Source"]).strip() for deb in sorted(debs.glob("*.deb"))]
    (stage / "runtime-packages.json").write_text(json.dumps(package_versions, indent=2))

    # Preserve distribution copyright notices and their referenced common licenses.
    for docroot in (root / "usr/share/doc", Path("/usr/share/doc")):
        for notice in docroot.glob("*/copyright"):
            target = stage / "licenses/packages" / notice.parent.name / "copyright"
            if not target.exists():
                target.parent.mkdir(parents=True, exist_ok=True)
                shutil.copyfile(notice, target)
    common = root / "usr/share/common-licenses"
    if not common.exists():
        common = Path("/usr/share/common-licenses")
    shutil.copytree(common, stage / "licenses/common", symlinks=False)


def validate_windows_binary(path, arch):
    with path.open("rb") as binary:
        header = binary.read(64)
        if len(header) < 64 or header[:2] != b"MZ":
            raise RuntimeError(f"Not a Windows executable: {path}")
        binary.seek(struct.unpack_from("<I", header, 60)[0])
        pe = binary.read(6)
    machines = {"amd64": 0x8664, "arm64": 0xaa64, "386": 0x14c}
    if len(pe) != 6 or pe[:4] != b"PE\0\0" or struct.unpack_from("<H", pe, 4)[0] != machines[arch]:
        raise RuntimeError(f"Windows executable does not match {arch}: {path}")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--helper", type=Path, required=True)
    parser.add_argument("--sysroot", type=Path, default=Path("/"))
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--webview2-runtime", type=Path)
    parser.add_argument("--webview2-loader", type=Path)
    parser.add_argument("--target-os", choices=("linux", "windows", "darwin"), required=True)
    parser.add_argument("--target-arch", choices=("amd64", "arm64", "386"), required=True)
    args = parser.parse_args()
    args.output.parent.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix="shelfmark-webview-package-") as tmp:
        stage = Path(tmp)
        if args.target_os == "linux":
            package_linux(args.helper.resolve(), args.sysroot.resolve(), stage)
        else:
            name = "shelfmark-window.exe" if args.target_os == "windows" else "shelfmark-window"
            if args.target_os == "windows":
                validate_windows_binary(args.helper, args.target_arch)
            shutil.copy2(args.helper, stage / name)
            if args.target_os == "windows" and args.webview2_runtime:
                validate_windows_binary(args.webview2_runtime / "msedgewebview2.exe", args.target_arch)
                shutil.copytree(args.webview2_runtime, stage / "webview2", symlinks=False)
                loader = args.webview2_loader or args.webview2_runtime / "WebView2Loader.dll"
                validate_windows_binary(loader, args.target_arch)
                shutil.copy2(loader, stage / "WebView2Loader.dll")
                shutil.copy2(loader.with_name("WebView2Loader.LICENSE.txt"), stage / "WebView2Loader.LICENSE.txt")
        module = run(["go", "list", "-m", "-f", "{{.Dir}}", "github.com/webview/webview_go"]).strip()
        (stage / "licenses").mkdir(exist_ok=True)
        for path in Path(module).rglob("*LICENSE*"):
            if path.is_file():
                target = stage / "licenses/webview" / path.relative_to(module)
                target.parent.mkdir(parents=True, exist_ok=True)
                shutil.copyfile(path, target)
        (stage / "licenses/README.txt").write_text("This runtime contains dynamically linked third-party components. See package copyright notices and common licenses. The Linux packager records original hashes and relocates compiled paths at extraction. For redistribution, retain notices and provide corresponding source as required by each component's license. Obtain exact distribution sources with apt-get source using the versions of the build packages; retain those sources alongside release artifacts.\n")
        with tarfile.open(args.output, "w:gz", compresslevel=6, dereference=True) as archive:
            for file in sorted(stage.rglob("*")):
                if file.is_file():
                    entry = archive.gettarinfo(str(file), arcname=str(file.relative_to(stage)))
                    entry.uid = entry.gid = entry.mtime = 0
                    entry.uname = entry.gname = ""
                    entry.mode = 0o755 if os.access(file, os.X_OK) else 0o644
                    with file.open("rb") as data:
                        archive.addfile(entry, data)
        print(f"Embedded runtime: {args.output.stat().st_size / (1 << 20):.1f} MiB")


if __name__ == "__main__":
    main()
