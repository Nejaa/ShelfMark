#!/usr/bin/env python3
"""Build and package a private native OCR engine and pinned language models."""
import argparse
import hashlib
import json
import inspect
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
    """Verify both new downloads and cached artifacts against committed hashes."""
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
    """Upstream source archives contain one root; reject links and traversal."""
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
    log.write("\n" + " ".join(map(str, args)) + "\n")
    log.flush()
    subprocess.run(list(map(str, args)), stdout=log, stderr=subprocess.STDOUT, check=True)


def build_engine(args, artifacts, work, prefix):
    """Limit native dependencies to Leptonica and the language recognition core.

    Images arrive as PNM decoded by Go, so no native PNG/JPEG/TIFF/etc libraries
    are needed. Disable training, graphics, network, archive, and OpenMP features.
    Compiler runtimes are static; OS system libraries remain supplied by the OS.
    """
    cc = os.environ.get("OCR_CC", os.environ.get("CC", "gcc"))
    cxx = os.environ.get("OCR_CXX", os.environ.get("CXX", "g++"))
    if args.target_os == "windows" and platform.system() != "Windows":
        triplet = {"amd64": "x86_64", "386": "i686", "arm64": "aarch64"}[args.target_arch] + "-w64-mingw32"
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
    system = {"linux": "Linux", "windows": "Windows", "darwin": "Darwin"}[args.target_os]
    processor = {"amd64": "x86_64", "386": "i686", "arm64": "aarch64"}[args.target_arch]
    common = [f"-DCMAKE_SYSTEM_NAME={system}", f"-DCMAKE_SYSTEM_PROCESSOR={processor}",
              f"-DCMAKE_C_COMPILER={cc}", f"-DCMAKE_CXX_COMPILER={cxx}",
              "-DCMAKE_BUILD_TYPE=Release", "-DBUILD_SHARED_LIBS=OFF", "-DSW_BUILD=OFF",
              f"-DCMAKE_INSTALL_PREFIX={prefix}", "-DCMAKE_INSTALL_LIBDIR=lib"]
    if args.target_os == "windows":
        common += ["-DCMAKE_EXE_LINKER_FLAGS=-static -static-libgcc -static-libstdc++"]
    elif args.target_os == "linux":
        common += ["-DCMAKE_EXE_LINKER_FLAGS=-static-libgcc -static-libstdc++"]
    logpath = work / "build.log"
    print(f"Building bundled OCR for {args.target_os}/{args.target_arch}; log: {logpath}", file=sys.stderr, flush=True)
    with logpath.open("w") as log:
        lept = extract_source(artifacts["leptonica"], work / "leptonica-src")
        leptbuild = work / "leptonica-build"
        options = ["-DBUILD_PROG=OFF"] + [f"-DENABLE_{name}=OFF" for name in
                   ("ZLIB", "PNG", "GIF", "JPEG", "TIFF", "WEBP", "OPENJPEG")]
        command(["cmake", "-S", lept, "-B", leptbuild, *common, *options], log)
        command(["cmake", "--build", leptbuild, "--parallel", args.jobs], log)
        command(["cmake", "--install", leptbuild], log)
        tess = extract_source(artifacts["tesseract"], work / "tesseract-src")
        # Upstream's Windows SDK spelling fails on case-sensitive MinGW hosts.
        if args.target_os == "windows":
            cmake = tess / "CMakeLists.txt"
            cmake.write_text(cmake.read_text().replace("set(LIB_Ws2_32 Ws2_32)", "set(LIB_Ws2_32 ws2_32)"))
        tessbuild = work / "tesseract-build"
        options = [f"-DCMAKE_PREFIX_PATH={prefix}", "-DBUILD_TRAINING_TOOLS=OFF", "-DBUILD_TESTS=OFF",
                   "-DGRAPHICS_DISABLED=ON", "-DDISABLED_LEGACY_ENGINE=ON", "-DOPENMP_BUILD=OFF",
                   "-DENABLE_NATIVE=OFF", "-DENABLE_LTO=OFF", "-DDISABLE_TIFF=ON",
                   "-DDISABLE_ARCHIVE=ON", "-DDISABLE_CURL=ON", "-DINSTALL_CONFIGS=OFF",
                   "-DLEPT_TIFF_RESULT=1", "-DLEPT_TIFF_COMPILE_SUCCESS=TRUE"]
        command(["cmake", "-S", tess, "-B", tessbuild, *common, *options], log)
        command(["cmake", "--build", tessbuild, "--target", "tesseract", "--parallel", args.jobs], log)
        binary = tessbuild / "bin" / ("tesseract.exe" if args.target_os == "windows" else "tesseract")
        prefix.joinpath("bin").mkdir(exist_ok=True)
        shutil.copy2(binary, prefix / "bin" / binary.name)
    return lept, tess


def main():
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
    manifest = json.loads((HERE / "sources.json").read_text())
    artifacts = {name: download(entry, args.cache / "downloads") for name, entry in manifest.items()}
    # The build recipe is part of the key; changing flags invalidates old engines.
    # Explicitly selecting the default cross-compiler is the same toolchain as
    # automatic selection. The wrapper and direct packager share this cache.
    compiler_prefix = ""
    if args.target_os == "windows" and platform.system() != "Windows":
        compiler_prefix = {"amd64": "x86_64", "386": "i686", "arm64": "aarch64"}[args.target_arch] + "-w64-mingw32-"
    default_compilers = {"CC": compiler_prefix + "gcc", "CXX": compiler_prefix + "g++",
                         "OCR_CC": compiler_prefix + "gcc", "OCR_CXX": compiler_prefix + "g++"}
    compiler_options = [os.environ.get(key, "") if os.environ.get(key, "") != default_compilers[key] else ""
                        for key in ("CC", "CXX", "OCR_CC", "OCR_CXX")]
    engine_sources = {name: manifest[name] for name in ("leptonica", "tesseract")}
    recipe = hashlib.sha256(json.dumps(engine_sources, sort_keys=True).encode() +
                            inspect.getsource(build_engine).encode() +
                            repr(compiler_options).encode()).hexdigest()[:16]
    work = (args.cache / f"{args.target_os}-{args.target_arch}-{recipe}").resolve()
    work.mkdir(parents=True, exist_ok=True)
    complete = work / "complete.json"
    prefix = work / "install"
    if not complete.exists() or not (prefix / "bin" / ("tesseract.exe" if args.target_os == "windows" else "tesseract")).exists():
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
    else:
        print(f"Using cached OCR engine: {work}", file=sys.stderr)
    license_manifest = json.loads((HERE / "licenses.json").read_text())
    runtime_licenses = {name: download(entry, args.cache / "downloads") for name, entry in license_manifest.items()}
    args.output.parent.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix="shelfmark-ocr-package-") as temporary:
        stage = Path(temporary)
        binary = prefix / "bin" / ("tesseract.exe" if args.target_os == "windows" else "tesseract")
        shutil.copy2(binary, stage / binary.name)
        shutil.copytree(prefix / "licenses", stage / "licenses")
        shutil.copyfile(artifacts["models_license"], stage / "licenses/models.txt")
        if args.target_os != "darwin":
            for name, file in runtime_licenses.items():
                if name.startswith("mingw-") and args.target_os != "windows":
                    continue
                shutil.copyfile(file, stage / "licenses" / name)
        (stage / "tessdata").mkdir()
        for language in ("eng", "fra"):
            shutil.copyfile(artifacts[language], stage / "tessdata" / f"{language}.traineddata")
        (stage / "manifest.json").write_text(json.dumps({"sources": manifest, "target_os": args.target_os,
                                                       "target_arch": args.target_arch, "runtime_licenses": license_manifest}, indent=2))
        with tarfile.open(args.output, "w:gz", compresslevel=6) as archive:
            for file in sorted(stage.rglob("*")):
                if not file.is_file():
                    continue
                entry = archive.gettarinfo(str(file), arcname=str(file.relative_to(stage)))
                entry.uid = entry.gid = entry.mtime = 0
                entry.uname = entry.gname = ""
                entry.mode = 0o700 if file.name == binary.name else 0o600
                with file.open("rb") as data:
                    archive.addfile(entry, data)
    print(f"Bundled OCR: {args.output.stat().st_size / (1 << 20):.1f} MiB", file=sys.stderr)


if __name__ == "__main__":
    try:
        main()
    except (RuntimeError, OSError) as error:
        print(f"OCR packaging: {error}", file=sys.stderr)
        sys.exit(1)
