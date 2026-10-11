#!/usr/bin/env python3
"""Build and package ShelfMark's pinned native OCR runtime.

Usage:
    python -m runtime.ocr.package --target-os {linux,windows,darwin} \
        --target-arch {amd64,386,arm64} --cache PATH --output PATH [--jobs N]

The command downloads the exact committed OCR sources, builds a minimized Tesseract
bundle, stages license notices, and writes a reproducible runtime archive.
"""

import argparse
import hashlib
import inspect
import json
import os
from pathlib import Path
import platform
import shutil
import subprocess
import sys
import tarfile
import tempfile
import urllib.request


HERE = Path(__file__).resolve().parent


def download(entry, cache):
    """Return a verified cached download for one manifest entry."""
    file = cache / entry["filename"]
    if file.exists() and hashlib.sha256(file.read_bytes()).hexdigest() == entry["sha256"]:
        return file

    print(f'Downloading {entry["filename"]}', file=sys.stderr, flush=True)
    cache.mkdir(parents=True, exist_ok=True)
    temporary = file.with_suffix(file.suffix + ".partial")
    try:
        request = urllib.request.Request(entry["url"], headers={"User-Agent": "Shelfmark-build"})
        with urllib.request.urlopen(request, timeout=120) as response, temporary.open("wb") as output:
            shutil.copyfileobj(response, output)
        if hashlib.sha256(temporary.read_bytes()).hexdigest() != entry["sha256"]:
            raise RuntimeError(f'Checksum mismatch: {entry["filename"]}')
        temporary.replace(file)
    finally:
        temporary.unlink(missing_ok=True)
    return file


def extract_source(archive, destination):
    """Extract a tar archive into *destination* while rejecting unsafe members."""
    destination.mkdir(parents=True, exist_ok=True)
    with tarfile.open(archive) as source:
        for member in source:
            path = Path(member.name)
            if path.is_absolute() or ".." in path.parts:
                raise RuntimeError(f"Invalid source path: {member.name}")
            if member.isdir():
                (destination / path).mkdir(parents=True, exist_ok=True)
            elif member.isfile():
                target = destination / path
                target.parent.mkdir(parents=True, exist_ok=True)
                with source.extractfile(member) as reader, target.open("wb") as output:
                    shutil.copyfileobj(reader, output)
            else:
                raise RuntimeError(f"Unsupported source member: {member.name}")
    roots = list(destination.iterdir())
    if len(roots) != 1 or not roots[0].is_dir():
        raise RuntimeError("Expected a single source root")
    return roots[0]


def command(args, log):
    """Run a build command, echoing it into the shared build log first."""
    log.write("\n" + " ".join(map(str, args)) + "\n")
    log.flush()
    subprocess.run(list(map(str, args)), stdout=log, stderr=subprocess.STDOUT, check=True)


ARCH_TRIPLET = {"amd64": "x86_64", "386": "i686", "arm64": "aarch64"}
CMAKE_SYSTEM = {"linux": "Linux", "windows": "Windows", "darwin": "Darwin"}
LEPTONICA_OPTIONS = ["-DBUILD_PROG=OFF"] + [
    f"-DENABLE_{name}=OFF" for name in ("ZLIB", "PNG", "GIF", "JPEG", "TIFF", "WEBP", "OPENJPEG")
]


def select_compilers(args):
    """Choose the C and C++ compilers used to build the OCR bundle."""
    cc = os.environ.get("OCR_CC", os.environ.get("CC", "gcc"))
    cxx = os.environ.get("OCR_CXX", os.environ.get("CXX", "g++"))
    if args.target_os == "windows" and platform.system() != "Windows":
        triplet = ARCH_TRIPLET[args.target_arch] + "-w64-mingw32"
        cc = os.environ.get("OCR_CC", os.environ.get("CC", triplet + "-gcc"))
        cxx = os.environ.get("OCR_CXX", os.environ.get("CXX", triplet + "-g++"))
        # POSIX MinGW supports the C++ threading primitives used by Tesseract.
        if "OCR_CXX" not in os.environ and shutil.which(cxx + "-posix"):
            cxx += "-posix"
            if shutil.which(cc + "-posix"):
                cc += "-posix"
    for compiler in (cc, cxx):
        if not shutil.which(compiler):
            raise RuntimeError(f"Missing OCR compiler: {compiler}; set OCR_CC/OCR_CXX")
    return cc, cxx


def common_cmake_options(args, cc, cxx, prefix):
    """Return the baseline CMake flags shared by Leptonica and Tesseract."""
    options = [
        f"-DCMAKE_SYSTEM_NAME={CMAKE_SYSTEM[args.target_os]}",
        f"-DCMAKE_SYSTEM_PROCESSOR={ARCH_TRIPLET[args.target_arch]}",
        f"-DCMAKE_C_COMPILER={cc}",
        f"-DCMAKE_CXX_COMPILER={cxx}",
        "-DCMAKE_BUILD_TYPE=Release",
        "-DBUILD_SHARED_LIBS=OFF",
        "-DSW_BUILD=OFF",
        f"-DCMAKE_INSTALL_PREFIX={prefix}",
        "-DCMAKE_INSTALL_LIBDIR=lib",
    ]
    if args.target_os == "windows":
        options.append("-DCMAKE_EXE_LINKER_FLAGS=-static -static-libgcc -static-libstdc++")
    elif args.target_os == "linux":
        options.append("-DCMAKE_EXE_LINKER_FLAGS=-static-libgcc -static-libstdc++")
    return options


def build_leptonica(args, artifacts, work, common, log):
    """Build the pared-down Leptonica dependency into the install prefix."""
    source = extract_source(artifacts["leptonica"], work / "leptonica-src")
    build = work / "leptonica-build"
    command(["cmake", "-S", source, "-B", build, *common, *LEPTONICA_OPTIONS], log)
    command(["cmake", "--build", build, "--parallel", args.jobs], log)
    command(["cmake", "--install", build], log)
    return source


def build_tesseract(args, artifacts, work, prefix, common, log):
    """Build the runtime OCR executable against the staged Leptonica prefix."""
    source = extract_source(artifacts["tesseract"], work / "tesseract-src")
    # Upstream's Windows SDK spelling fails on case-sensitive MinGW hosts.
    if args.target_os == "windows":
        cmake = source / "CMakeLists.txt"
        cmake.write_text(cmake.read_text().replace("set(LIB_Ws2_32 Ws2_32)", "set(LIB_Ws2_32 ws2_32)"))
    build = work / "tesseract-build"
    options = [
        f"-DCMAKE_PREFIX_PATH={prefix}",
        "-DBUILD_TRAINING_TOOLS=OFF",
        "-DBUILD_TESTS=OFF",
        "-DGRAPHICS_DISABLED=ON",
        "-DDISABLED_LEGACY_ENGINE=ON",
        "-DOPENMP_BUILD=OFF",
        "-DENABLE_NATIVE=OFF",
        "-DENABLE_LTO=OFF",
        "-DDISABLE_TIFF=ON",
        "-DDISABLE_ARCHIVE=ON",
        "-DDISABLE_CURL=ON",
        "-DINSTALL_CONFIGS=OFF",
        "-DLEPT_TIFF_RESULT=1",
        "-DLEPT_TIFF_COMPILE_SUCCESS=TRUE",
    ]
    command(["cmake", "-S", source, "-B", build, *common, *options], log)
    command(["cmake", "--build", build, "--target", "tesseract", "--parallel", args.jobs], log)
    binary = build / "bin" / engine_binary_name(args.target_os)
    prefix.joinpath("bin").mkdir(exist_ok=True)
    shutil.copy2(binary, prefix / "bin" / binary.name)
    return source


def build_engine(args, artifacts, work, prefix):
    """Limit native dependencies to Leptonica and the language recognition core.

    Images arrive as PNM decoded by Go, so no native PNG/JPEG/TIFF/etc libraries
    are needed. Disable training, graphics, network, archive, and OpenMP features.
    Compiler runtimes are static; OS system libraries remain supplied by the OS.
    """
    cc, cxx = select_compilers(args)
    common = common_cmake_options(args, cc, cxx, prefix)
    logpath = work / "build.log"
    print(f"Building bundled OCR for {args.target_os}/{args.target_arch}; log: {logpath}", file=sys.stderr, flush=True)
    with logpath.open("w") as log:
        lept = build_leptonica(args, artifacts, work, common, log)
        tess = build_tesseract(args, artifacts, work, prefix, common, log)
    return lept, tess


def engine_binary_name(target_os):
    """Return the platform-specific executable name for the packaged OCR engine."""
    return "tesseract.exe" if target_os == "windows" else "tesseract"


def parse_args():
    """Parse and validate the packager CLI arguments."""
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--target-os", choices=("linux", "windows", "darwin"), required=True)
    parser.add_argument("--target-arch", choices=("amd64", "386", "arm64"), required=True)
    parser.add_argument("--cache", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--jobs", type=int, default=int(os.environ.get("OCR_BUILD_JOBS", "4")))
    args = parser.parse_args()
    if args.jobs < 1:
        parser.error("--jobs must be positive")
    for tool in ("cmake", "make"):
        if not shutil.which(tool):
            parser.error(f"Missing OCR build dependency: {tool}")
    return args


RECIPE_STEPS = (select_compilers, common_cmake_options, build_leptonica, build_tesseract, build_engine)


def recipe_key(args, manifest):
    """The build recipe is part of the key; changing flags invalidates old engines.

    Explicitly selecting the default cross-compiler is the same toolchain as
    automatic selection. The wrapper and direct packager share this cache.
    """
    compiler_prefix = ""
    if args.target_os == "windows" and platform.system() != "Windows":
        compiler_prefix = {"amd64": "x86_64", "386": "i686", "arm64": "aarch64"}[args.target_arch] + "-w64-mingw32-"
    default_compilers = {
        "CC": compiler_prefix + "gcc",
        "CXX": compiler_prefix + "g++",
        "OCR_CC": compiler_prefix + "gcc",
        "OCR_CXX": compiler_prefix + "g++",
    }
    compiler_options = [
        os.environ.get(key, "") if os.environ.get(key, "") != default_compilers[key] else ""
        for key in ("CC", "CXX", "OCR_CC", "OCR_CXX")
    ]
    engine_sources = {name: manifest[name] for name in ("leptonica", "tesseract")}
    return hashlib.sha256(
        json.dumps(engine_sources, sort_keys=True).encode()
        + "".join(inspect.getsource(step) for step in RECIPE_STEPS).encode()
        + repr(compiler_options).encode()
    ).hexdigest()[:16]


def ensure_engine(args, manifest, artifacts):
    """Return the cached engine prefix, building it when the cache is incomplete."""
    work = (args.cache / f"{args.target_os}-{args.target_arch}-{recipe_key(args, manifest)}").resolve()
    work.mkdir(parents=True, exist_ok=True)
    complete = work / "complete.json"
    prefix = work / "install"
    if complete.exists() and (prefix / "bin" / engine_binary_name(args.target_os)).exists():
        print(f"Using cached OCR engine: {work}", file=sys.stderr)
        return prefix
    # Incomplete builds are rerunnable and are never treated as cache hits.
    for name in ("leptonica-src", "tesseract-src"):
        shutil.rmtree(work / name, ignore_errors=True)
    try:
        lept, tess = build_engine(args, artifacts, work, prefix)
    except subprocess.CalledProcessError as error:
        raise RuntimeError(f"OCR compilation failed; see {work / 'build.log'}") from error
    licenses = prefix / "licenses"
    licenses.mkdir(exist_ok=True)
    shutil.copyfile(lept / "leptonica-license.txt", licenses / "leptonica.txt")
    shutil.copyfile(tess / "LICENSE", licenses / "tesseract.txt")
    complete.write_text(json.dumps(manifest, indent=2))
    return prefix


def stage_package(args, manifest, artifacts, prefix, stage):
    """Copy the built engine, models, and required notices into *stage*."""
    license_manifest = json.loads((HERE / "licenses.json").read_text())
    runtime_licenses = {name: download(entry, args.cache / "downloads") for name, entry in license_manifest.items()}

    binary = prefix / "bin" / engine_binary_name(args.target_os)
    shutil.copy2(binary, stage / binary.name)
    shutil.copytree(prefix / "licenses", stage / "licenses")
    shutil.copyfile(artifacts["models_license"], stage / "licenses/models.txt")

    # Ship only the runtime notices that apply to the selected target OS.
    if args.target_os != "darwin":
        for name, file in runtime_licenses.items():
            if name.startswith("mingw-") and args.target_os != "windows":
                continue
            shutil.copyfile(file, stage / "licenses" / name)

    (stage / "tessdata").mkdir()
    for language in ("eng", "fra"):
        shutil.copyfile(artifacts[language], stage / "tessdata" / f"{language}.traineddata")

    (stage / "manifest.json").write_text(
        json.dumps(
            {
                "sources": manifest,
                "target_os": args.target_os,
                "target_arch": args.target_arch,
                "runtime_licenses": license_manifest,
            },
            indent=2,
        )
    )
    return binary.name


def write_archive(stage, output, binary_name):
    """Write a normalized tarball for reproducible OCR bundle releases."""
    with tarfile.open(output, "w:gz", compresslevel=6) as archive:
        for file in sorted(stage.rglob("*")):
            if not file.is_file():
                continue
            entry = archive.gettarinfo(str(file), arcname=str(file.relative_to(stage)))
            entry.uid = entry.gid = entry.mtime = 0
            entry.uname = entry.gname = ""
            entry.mode = 0o700 if file.name == binary_name else 0o600
            with file.open("rb") as data:
                archive.addfile(entry, data)


def main():
    """Build or reuse the OCR cache entry, then write the packaged runtime archive."""
    args = parse_args()
    manifest = json.loads((HERE / "sources.json").read_text())
    artifacts = {name: download(entry, args.cache / "downloads") for name, entry in manifest.items()}
    prefix = ensure_engine(args, manifest, artifacts)
    args.output.parent.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix="shelfmark-ocr-package-") as temporary:
        stage = Path(temporary)
        binary_name = stage_package(args, manifest, artifacts, prefix, stage)
        write_archive(stage, args.output, binary_name)
    print(f"Bundled OCR: {args.output.stat().st_size / (1 << 20):.1f} MiB", file=sys.stderr)


if __name__ == "__main__":
    try:
        main()
    except (RuntimeError, OSError) as error:
        print(f"OCR packaging: {error}", file=sys.stderr)
        sys.exit(1)
