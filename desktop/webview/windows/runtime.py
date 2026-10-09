#!/usr/bin/env python3
"""Fetch and cache the pinned Microsoft Fixed Version WebView2 runtime."""
import argparse
import errno
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import time
import zipfile
from urllib.parse import urlparse
from urllib.request import urlopen

from ..package import validate_windows_binary


def digest(path):
    result = hashlib.sha256()
    with path.open("rb") as source:
        while chunk := source.read(1024 * 1024):
            result.update(chunk)
    return result.hexdigest()


def download(artifact, cache):
    url = artifact["url"]
    parsed = urlparse(url)
    if parsed.scheme != "https" or not trusted_host(parsed.hostname):
        raise RuntimeError("Manifest downloads must use Microsoft or NuGet HTTPS URLs")
    cache.mkdir(parents=True, exist_ok=True)
    archive = cache / Path(parsed.path).name
    if archive.is_file() and archive.stat().st_size == artifact["size"] and digest(archive) == artifact["sha256"]:
        return archive
    print(f"Downloading WebView2: {archive.name}", file=sys.stderr)
    for attempt in range(3):
        temporary = None
        try:
            with tempfile.NamedTemporaryFile(dir=cache, suffix=".partial", delete=False) as output:
                temporary = Path(output.name)
                with urlopen(url, timeout=60) as response:
                    redirected = urlparse(response.geturl())
                    if redirected.scheme != "https" or not trusted_host(redirected.hostname):
                        raise RuntimeError("Unexpected runtime download redirect")
                    size = 0
                    while chunk := response.read(1024 * 1024):
                        size += len(chunk)
                        if size > artifact["size"]:
                            raise RuntimeError("Runtime download exceeds the pinned archive size")
                        output.write(chunk)
            if temporary.stat().st_size != artifact["size"] or digest(temporary) != artifact["sha256"]:
                raise RuntimeError("WebView2 archive checksum or size does not match runtime.json")
            os.replace(temporary, archive)
            return archive
        except OSError:
            if attempt == 2:
                raise
            time.sleep(attempt + 1)
        finally:
            if temporary is not None:
                temporary.unlink(missing_ok=True)


def trusted_host(host):
    return (host or "").endswith(".microsoft.com") or host == "api.nuget.org"


def ensure_loader(runtime, manifest, arch, cache):
    loader = manifest["loader"]
    artifact = loader["builds"][arch]
    target = runtime / "WebView2Loader.dll"
    license_file = runtime / "WebView2Loader.LICENSE.txt"
    if target.is_file() and license_file.is_file() and digest(target) == artifact["sha256"]:
        return
    archive = download(loader, cache / "downloads")
    with zipfile.ZipFile(archive) as sdk:
        content = sdk.read(artifact["path"])
        if hashlib.sha256(content).hexdigest() != artifact["sha256"]:
            raise RuntimeError("WebView2 loader checksum does not match runtime.json")
        with tempfile.NamedTemporaryFile(dir=runtime, delete=False) as output:
            temporary = Path(output.name)
            try:
                output.write(sdk.read("LICENSE.txt"))
                output.close()
                os.replace(temporary, license_file)
            finally:
                temporary.unlink(missing_ok=True)
        with tempfile.NamedTemporaryFile(dir=runtime, delete=False) as output:
            temporary = Path(output.name)
            try:
                output.write(content)
                output.close()
                validate_windows_binary(temporary, arch)
                os.replace(temporary, target)
            finally:
                temporary.unlink(missing_ok=True)


def extractor():
    if command := shutil.which("cabextract"):
        return lambda archive, dest: [command, "-q", "-d", str(dest), str(archive)]
    if command := shutil.which("7z") or shutil.which("7za"):
        return lambda archive, dest: [command, "x", "-y", "-o" + str(dest), str(archive)]
    if os.name == "nt" and (command := shutil.which("expand.exe")):
        return lambda archive, dest: [command, str(archive), "-F:*", str(dest)]
    raise RuntimeError("Extracting WebView2 requires cabextract or 7-Zip (on Debian: sudo apt-get install cabextract); Windows can use expand.exe")


def runtime_path(cache, manifest, arch):
    artifact = manifest["builds"][arch]
    expected = {"version": manifest["version"], "arch": arch, **artifact}
    target = cache / manifest["version"] / arch
    marker = target / "shelfmark-runtime.json"
    if marker.is_file() and json.loads(marker.read_text()) == expected:
        validate_windows_binary(target / "msedgewebview2.exe", arch)
        ensure_loader(target, manifest, arch, cache)
        print(f"Using cached WebView2 {manifest['version']} ({arch})", file=sys.stderr)
        return target
    if target.exists():
        raise RuntimeError(f"Incomplete runtime cache: remove {target} and rebuild")
    extract_command = extractor()
    archive = download(artifact, cache / "downloads")
    target.parent.mkdir(parents=True, exist_ok=True)
    print(f"Extracting WebView2 {manifest['version']} ({arch})", file=sys.stderr)
    with tempfile.TemporaryDirectory(prefix=".extract-", dir=target.parent) as temporary:
        dest = Path(temporary)
        subprocess.run(extract_command(archive, dest), check=True, capture_output=True, text=True)
        executables = list(dest.rglob("msedgewebview2.exe"))
        if len(executables) != 1:
            raise RuntimeError("WebView2 archive must contain exactly one runtime executable")
        runtime = executables[0].parent
        validate_windows_binary(executables[0], arch)
        (runtime / marker.name).write_text(json.dumps(expected, indent=2) + "\n")
        try:
            runtime.rename(target)
        except OSError as error:
            # Concurrent builds may publish the same complete cache entry first.
            if error.errno not in (errno.EEXIST, errno.ENOTEMPTY) or not marker.is_file() or json.loads(marker.read_text()) != expected:
                raise
    ensure_loader(target, manifest, arch, cache)
    return target


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--arch", choices=("amd64", "arm64", "386"), required=True)
    parser.add_argument("--cache", type=Path, required=True)
    parser.add_argument("--loader-only", action="store_true")
    args = parser.parse_args()
    try:
        manifest = json.loads(Path(__file__).with_name("runtime.json").read_text())
        cache = args.cache.resolve()
        if args.loader_only:
            loader_dir = cache / "loaders" / manifest["loader"]["version"] / args.arch
            loader_dir.mkdir(parents=True, exist_ok=True)
            ensure_loader(loader_dir, manifest, args.arch, cache)
            print(loader_dir / "WebView2Loader.dll")
        else:
            print(runtime_path(cache, manifest, args.arch))
    except subprocess.CalledProcessError as error:
        parser.exit(1, f"WebView2 extraction failed: {error.stderr or error.stdout}\n")
    except (OSError, RuntimeError, ValueError) as error:
        parser.exit(1, f"WebView2 runtime: {error}\n")


if __name__ == "__main__":
    main()
