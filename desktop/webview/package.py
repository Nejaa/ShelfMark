#!/usr/bin/env python3
"""Build the desktop helper runtime bundle used by ShelfMark.

Usage:
    python -m desktop.webview.package --helper PATH --output PATH \
        --target-os {linux,windows,darwin} --target-arch {amd64,arm64,386}

The archive contains the helper executable plus only the runtime pieces that
must ship with it; core system libraries remain supplied by the host OS.
"""

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
SYSTEM_LIB_NAMES = frozenset(("c", "m", "dl", "pthread", "rt", "resolv", "util", "anl"))
SWRAST_DRIVER = "dri/swrast_dri.so"
WEBKIT_RELOCATED_ROOT = "${ROOT}/w"
PIXBUF_QUERY = "gdk-pixbuf-2.0/gdk-pixbuf-query-loaders"


def is_system_library(name):
    """Return True for libraries that must remain provided by the host system."""
    if name.startswith("ld-linux"):
        return True
    if not name.startswith("lib"):
        return False

    library = name[3:]
    stem, separator, version = library.partition(".so")
    if not separator:
        return False
    if stem.startswith("nss_"):
        stem, _, version = library.rpartition(".so")
        return stem.startswith("nss_") and (not version or version.startswith("."))
    return stem in SYSTEM_LIB_NAMES and (not version or version.startswith("."))


def run(args, env=None):
    """Run a command and return its combined text output."""
    return subprocess.check_output(args, env=env, text=True, stderr=subprocess.STDOUT)


def first_existing(candidates):
    """Return the first existing path from an iterator of candidates."""
    return next((path for path in candidates if path.exists()), None)


class RuntimeBundle:
    """Copy binaries and their non-system shared-library closure into a stage."""

    def __init__(self, root, stage):
        self.root = root
        self.stage = stage
        self.arch = run(["gcc", "-dumpmachine"]).strip()
        self.libdirs = [
            root / "usr/lib" / self.arch,
            root / "lib" / self.arch,
            root / "usr/lib64",
            root / "usr/lib",
            root / "lib",
        ]
        self.env = os.environ.copy()
        # Resolve dependencies against the same library search path we bundle from.
        self.env["LD_LIBRARY_PATH"] = ":".join(str(path) for path in self.libdirs if path.is_dir())
        self.copied, self.inventory, self.originals = {}, [], {}

    def copy(self, source, dest):
        """Copy a file into the stage once and record where it came from."""
        source = source.resolve()
        target = self.stage / dest
        if target.exists():
            return target

        target.parent.mkdir(parents=True, exist_ok=True)
        shutil.copy2(source, target)
        self.originals[dest] = source
        self.inventory.append({
            "file": dest,
            "source": str(source),
            "sha256": hashlib.sha256(source.read_bytes()).hexdigest(),
        })
        return target

    def dependencies(self, binary):
        """Copy the non-system shared libraries reported by ldd for *binary*."""
        output = run(["ldd", str(binary)], self.env)
        if "not found" in output:
            raise RuntimeError(f"Unresolved dependency of {binary}:\n{output}")

        for line in output.splitlines():
            match = re.match(r"\s*(\S+) => (/\S+) \(", line)
            if not match:
                continue
            name, source = match.groups()
            if is_system_library(name) or name in self.copied:
                continue
            self.copied[name] = source
            self.copy(Path(source), "lib/" + name)
            self.dependencies(Path(source))

    def add(self, source, dest):
        """Copy a binary or library, then copy its dependency closure."""
        self.copy(source, dest)
        self.dependencies(source)

    def find_in_libdirs(self, relative):
        """Look up a runtime file by path relative to the library directories."""
        return first_existing(path / relative for path in self.libdirs)


def bundle_webkit(bundle):
    """Bundle the WebKit subprocesses and sandbox helpers the helper spawns."""
    webkit = next(
        (path / "webkit2gtk-4.0" for path in bundle.libdirs if (path / "webkit2gtk-4.0/WebKitWebProcess").exists()),
        None,
    )
    if not webkit:
        raise RuntimeError("WebKitWebProcess was not found in the runtime root")

    # Ship only the subprocesses WebKit launches for rendering, networking, and GPU work.
    for name in ("WebKitWebProcess", "WebKitNetworkProcess", "WebKitGPUProcess"):
        binary = webkit / name
        if binary.exists():
            bundle.add(binary, "w/" + name)

    for binary in (webkit / "injected-bundle").glob("*.so"):
        bundle.add(binary, "w/injected-bundle/" + binary.name)

    for name in ("bwrap", "xdg-dbus-proxy"):
        binary = bundle.root / "usr/bin" / name
        if not binary.exists():
            binary = Path("/usr/bin") / name
        if not binary.exists():
            raise RuntimeError(f"Missing WebKit sandbox executable: {binary}")
        bundle.add(binary, "bin/" + name)


def bundle_mesa(bundle):
    """Bundle the software GL stack WebKit can dlopen at runtime."""
    # GLVND loads its vendor with dlopen; WebKit initializes EGL even when
    # accelerated page compositing is disabled. Ship Mesa's software renderer.
    for name in ("libEGL_mesa.so.0", "libGLX_mesa.so.0"):
        binary = bundle.find_in_libdirs(name)
        if binary is None:
            raise RuntimeError(f"Install Mesa runtime libraries to bundle {name}")
        bundle.add(binary, "lib/" + name)

    software = bundle.find_in_libdirs(SWRAST_DRIVER)
    if software is None:
        raise RuntimeError("Install libgl1-mesa-dri to bundle software rendering")
    bundle.add(software, SWRAST_DRIVER)
    (bundle.stage / "egl-mesa.json").write_text(
        json.dumps({"file_format_version": "1.0.0", "ICD": {"library_path": "libEGL_mesa.so.0"}})
    )


def bundle_dlopen_modules(bundle):
    """Bundle plugin directories that are discovered by dlopen rather than ldd."""
    # Keep these allow-lists narrow so the archive tracks only modules the helper can load.
    for directory, dest in (("gio/modules", "gio"), ("gdk-pixbuf-2.0/2.10.0/loaders", "pixbuf")):
        for libdir in bundle.libdirs:
            for binary in (libdir / directory).glob("*.so"):
                bundle.add(binary, dest + "/" + binary.name)

    query = bundle.find_in_libdirs(PIXBUF_QUERY)
    pixbuf = bundle.stage / "pixbuf"
    if query and pixbuf.exists():
        cache = run([str(query), *map(str, sorted(pixbuf.glob("*.so")))], bundle.env)
        (pixbuf / "loaders.cache").write_text(cache.replace(str(pixbuf), "./pixbuf"))


def root_or_system(root, relative):
    """Prefer files from the sysroot, falling back to the host filesystem."""
    path = root / relative
    return path if path.exists() else Path("/") / relative


def bundle_shared_data(bundle):
    """Stage shared GTK data such as schemas, fonts, icons, and keyboard maps."""
    root, stage = bundle.root, bundle.stage

    keyboard = root_or_system(root, "usr/share/X11/xkb")
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
    run([str(compiler), str(stage / "share/glib-2.0/schemas")], bundle.env)

    # Built-in GTK resources supply Adwaita; include icons and a basic font family.
    icons = root / "usr/share/icons/Adwaita"
    if icons.exists():
        shutil.copytree(icons, stage / "share/icons/Adwaita", symlinks=False)

    fonts = root_or_system(root, "usr/share/fonts/truetype/dejavu")
    for name in ("DejaVuSans.ttf", "DejaVuSans-Bold.ttf", "DejaVuSansMono.ttf"):
        if (fonts / name).exists():
            bundle.copy(fonts / name, "fonts/" + name)

    (stage / "fonts.conf").write_text(
        '<?xml version="1.0"?><!DOCTYPE fontconfig SYSTEM "fonts.dtd"><fontconfig><dir prefix="relative">fonts</dir><dir>/usr/share/fonts</dir><dir>/usr/local/share/fonts</dir><cachedir prefix="xdg">fontconfig</cachedir></fontconfig>'
    )


def relocation_patches(bundle):
    """Describe in-place string substitutions applied when the bundle is extracted."""
    # Release WebKit builds ignore WEBKIT_EXEC_PATH. Record complete ELF strings
    # for relocation at extraction, leaving offsets and string table sizes intact.
    prefixes = {
        "/usr/lib/" + bundle.arch + "/webkit2gtk-4.0": WEBKIT_RELOCATED_ROOT,
        "/usr/lib/webkit2gtk-4.0": WEBKIT_RELOCATED_ROOT,
        "/usr/libexec/webkit2gtk-4.0": WEBKIT_RELOCATED_ROOT,
        "/usr/bin/bwrap": "./bin/bwrap",
        "/usr/bin/xdg-dbus-proxy": "./bin/xdg-dbus-proxy",
    }
    patches = []
    for dest, source in bundle.originals.items():
        content = source.read_bytes()
        if not content.startswith(b"\x7fELF"):
            continue
        for prefix, replacement in prefixes.items():
            pattern = re.escape(prefix.encode()) + rb"[^\x00]*\x00"
            for old in sorted(set(re.findall(pattern, content))):
                try:
                    text = old[:-1].decode("ascii")
                except UnicodeDecodeError:
                    continue
                patches.append({"File": dest, "Old": text, "New": replacement + text[len(prefix):]})
    expected = str(Path("/usr/lib") / bundle.arch / "webkit2gtk-4.0")
    if not any(patch["Old"] == expected for patch in patches):
        raise RuntimeError("Unrecognized WebKit subprocess path; this runtime needs packaging support")
    return patches


def write_manifests(bundle):
    """Record relocation metadata, copied files, and originating Debian packages."""
    stage, root = bundle.stage, bundle.root
    (stage / "relocations.json").write_text(json.dumps(relocation_patches(bundle), indent=2))
    (stage / "runtime-manifest.json").write_text(json.dumps(bundle.inventory, indent=2))

    package_versions = {
        "host": run(["dpkg-query", "-W", "-f", "${binary:Package}\t${Version}\t${source:Package}\t${source:Version}\n"])
    }
    debs = root.parent / "debs"
    if debs.is_dir():
        package_versions["sysroot"] = [
            run(["dpkg-deb", "-f", str(deb), "Package", "Version", "Source"]).strip()
            for deb in sorted(debs.glob("*.deb"))
        ]
    (stage / "runtime-packages.json").write_text(json.dumps(package_versions, indent=2))


def copy_distribution_notices(root, stage):
    """Copy Debian copyright notices and common licenses for bundled packages."""
    # Preserve distribution copyright notices and their referenced common licenses.
    for docroot in (root / "usr/share/doc", Path("/usr/share/doc")):
        for notice in docroot.glob("*/copyright"):
            target = stage / "licenses/packages" / notice.parent.name / "copyright"
            if not target.exists():
                target.parent.mkdir(parents=True, exist_ok=True)
                shutil.copyfile(notice, target)

    common = root_or_system(root, "usr/share/common-licenses")
    shutil.copytree(common, stage / "licenses/common", symlinks=False)


def package_linux(helper, root, stage):
    """Stage the Linux helper, private runtime, and accompanying notices."""
    bundle = RuntimeBundle(root, stage)
    bundle.add(helper, "shelfmark-window")
    bundle_webkit(bundle)
    bundle_mesa(bundle)
    bundle_dlopen_modules(bundle)
    bundle_shared_data(bundle)
    write_manifests(bundle)
    copy_distribution_notices(root, stage)


def validate_windows_binary(path, arch):
    """Verify that *path* is a PE executable for the requested architecture."""
    with path.open("rb") as binary:
        header = binary.read(64)
        if len(header) < 64 or header[:2] != b"MZ":
            raise RuntimeError(f"Not a Windows executable: {path}")
        binary.seek(struct.unpack_from("<I", header, 60)[0])
        pe = binary.read(6)

    machines = {"amd64": 0x8664, "arm64": 0xAA64, "386": 0x14C}
    if len(pe) != 6 or pe[:4] != b"PE\0\0" or struct.unpack_from("<H", pe, 4)[0] != machines[arch]:
        raise RuntimeError(f"Windows executable does not match {arch}: {path}")


def stage_target_files(args, stage):
    """Copy the target-specific helper payload into the temporary staging area."""
    if args.target_os == "linux":
        package_linux(args.helper.resolve(), args.sysroot.resolve(), stage)
        return

    name = "shelfmark-window.exe" if args.target_os == "windows" else "shelfmark-window"
    if args.target_os == "windows":
        validate_windows_binary(args.helper, args.target_arch)
    shutil.copy2(args.helper, stage / name)

    if args.target_os == "windows" and args.webview2_runtime:
        stage_webview2(args, stage)


def stage_webview2(args, stage):
    """Stage the pinned Fixed Version WebView2 runtime and loader."""
    validate_windows_binary(args.webview2_runtime / "msedgewebview2.exe", args.target_arch)
    shutil.copytree(args.webview2_runtime, stage / "webview2", symlinks=False)

    # Prefer an explicitly supplied loader, otherwise reuse the one cached with the runtime.
    loader = args.webview2_loader or args.webview2_runtime / "WebView2Loader.dll"
    validate_windows_binary(loader, args.target_arch)
    shutil.copy2(loader, stage / "WebView2Loader.dll")
    shutil.copy2(loader.with_name("WebView2Loader.LICENSE.txt"), stage / "WebView2Loader.LICENSE.txt")


def stage_licenses(stage):
    """Copy upstream webview binding notices and redistribution guidance."""
    module = run(["go", "list", "-m", "-f", "{{.Dir}}", "github.com/webview/webview_go"]).strip()
    (stage / "licenses").mkdir(exist_ok=True)
    for path in Path(module).rglob("*LICENSE*"):
        if path.is_file():
            target = stage / "licenses/webview" / path.relative_to(module)
            target.parent.mkdir(parents=True, exist_ok=True)
            shutil.copyfile(path, target)

    (stage / "licenses/README.txt").write_text(
        "This runtime contains dynamically linked third-party components. See package copyright notices and common licenses. The Linux packager records original hashes and relocates compiled paths at extraction. For redistribution, retain notices and provide corresponding source as required by each component's license. Obtain exact distribution sources with apt-get source using the versions of the build packages; retain those sources alongside release artifacts.\n"
    )


def write_archive(stage, output):
    """Write a normalized tarball so release archives stay reproducible."""
    with tarfile.open(output, "w:gz", compresslevel=6, dereference=True) as archive:
        for file in sorted(stage.rglob("*")):
            if file.is_file():
                entry = archive.gettarinfo(str(file), arcname=str(file.relative_to(stage)))
                entry.uid = entry.gid = entry.mtime = 0
                entry.uname = entry.gname = ""
                entry.mode = 0o755 if os.access(file, os.X_OK) else 0o644
                with file.open("rb") as data:
                    archive.addfile(entry, data)


def main():
    """Parse CLI arguments, stage the runtime, and emit the final archive."""
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
        stage_target_files(args, stage)
        stage_licenses(stage)
        write_archive(stage, args.output)
        print(f"Embedded runtime: {args.output.stat().st_size / (1 << 20):.1f} MiB")


if __name__ == "__main__":
    main()
