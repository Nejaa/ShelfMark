#!/usr/bin/env python3
"""Collect release notices and optional corresponding source bundles.

Usage:
    python tools/notices.py --output notices.tar.gz --ocr prebuilt-ocr.tar.gz \
        [--desktop desktop-runtime.tar.gz] [--sources sources-dir]

The notice bundle records the exact third-party texts shipped with a build. When
--sources is provided, the command also copies the matching source inputs needed
for redistribution.
"""

import argparse
import hashlib
import io
import json
from pathlib import Path
import shutil
import subprocess
import tarfile


ROOT = Path(__file__).resolve().parent.parent


def digest(path):
    """Return the SHA-256 digest of *path*."""
    result = hashlib.sha256()
    with path.open('rb') as stream:
        for block in iter(lambda: stream.read(1024 * 1024), b''):
            result.update(block)
    return result.hexdigest()


def objects(data):
    """Yield concatenated JSON objects from go mod download -json output."""
    decoder = json.JSONDecoder()
    while data.strip():
        data = data.lstrip()
        value, end = decoder.raw_decode(data)
        yield value
        data = data[end:]


def is_notice(path):
    """Return True when a file name looks like a license or attribution notice."""
    name = path.name.lower()
    return name.startswith(('license', 'licence', 'copying', 'copyright', 'notice', 'unlicense', 'authors', 'patents', 'credits', 'ofl', 'fontlog'))


def verify_terms():
    """Verify the committed WebView2 terms file before it is redistributed."""
    terms = json.loads((ROOT / 'licenses/webview2-terms.json').read_text())
    if digest(ROOT / 'licenses' / terms['file']) != terms['sha256']:
        raise RuntimeError('WebView2 terms do not match their recorded checksum')


def add_project_notices(files):
    """Add first-party license files and committed notice assets."""
    for name in ('LICENSE', 'THIRD_PARTY.md'):
        files[name] = (ROOT / name).read_bytes()
    for path in (ROOT / 'licenses').rglob('*'):
        if path.is_file():
            files[str(path.relative_to(ROOT))] = path.read_bytes()


def check_fonts(module, fonts, seen_fonts):
    """Verify that embedded font hashes still match the reviewed inventory."""
    directory = Path(module['Dir'])
    for font in fonts:
        if font['module'] == module['Path']:
            if digest(directory / font['file']) != font['sha256']:
                raise RuntimeError('Embedded font changed; review its license: ' + font['file'])
            seen_fonts.add((font['module'], font['file']))


def copy_module_sources(module, destination):
    """Copy the Go module zip and go.mod records used for redistribution."""
    destination.mkdir(parents=True, exist_ok=True)
    for key in ('Zip', 'GoMod'):
        path = Path(module[key])
        shutil.copyfile(path, destination / path.name)


def add_go_modules(files, sources):
    """Collect notice files and optional source zips for every resolved Go module."""
    # Download first: a module graph can contain modules absent from the cache.
    downloaded = subprocess.check_output(['go', 'mod', 'download', '-json', 'all'], cwd=ROOT, text=True)
    modules = []
    fonts = json.loads((ROOT / 'licenses/fonts.json').read_text())
    seen_fonts = set()
    for module in objects(downloaded):
        if 'Error' in module:
            raise RuntimeError(module['Error'])
        directory = Path(module['Dir'])
        check_fonts(module, fonts, seen_fonts)
        notices = [p for p in directory.rglob('*') if p.is_file() and is_notice(p)]
        if not notices:
            raise RuntimeError('No license found for ' + module['Path'])
        prefix = 'go-modules/' + module['Path'] + '@' + module['Version']
        for path in notices:
            files[prefix + '/' + str(path.relative_to(directory))] = path.read_bytes()
        modules.append({key: module[key] for key in ('Path', 'Version', 'Sum', 'GoModSum') if key in module})
        if sources:
            copy_module_sources(module, sources / prefix)
    if len(seen_fonts) != len(fonts):
        raise RuntimeError('Font notice inventory does not match the module graph')
    files['go-modules.json'] = (json.dumps(modules, indent=2) + '\n').encode()


def add_go_toolchain(files, sources):
    """Collect Go toolchain notices and optional source code for redistribution."""
    goroot = Path(subprocess.check_output(['go', 'env', 'GOROOT'], text=True).strip())
    for path in goroot.rglob('*'):
        if path.is_file() and is_notice(path):
            files['go/' + str(path.relative_to(goroot))] = path.read_bytes()
    files['go/version.txt'] = subprocess.check_output(['go', 'version'])
    if sources:
        shutil.copytree(goroot / 'src', sources / 'go/src', dirs_exist_ok=True)
        shutil.copyfile(goroot / 'LICENSE', sources / 'go/LICENSE')


def collect_recipe_sources(sources):
    """Collect the exact source inputs referenced by the OCR and desktop recipes."""
    # The same verified inputs as the OCR packager, including model weights.
    import sys

    sys.path.insert(0, str(ROOT))
    from runtime.ocr.package import download

    manifest = json.loads((ROOT / 'runtime/ocr/sources.json').read_text())
    destination = sources / 'ocr'
    destination.mkdir(parents=True, exist_ok=True)
    for entry in manifest.values():
        path = download(entry, ROOT / 'target/cache/ocr/downloads')
        shutil.copyfile(path, destination / path.name)
    shutil.copytree(ROOT / 'runtime/ocr', destination / 'recipe', ignore=shutil.ignore_patterns('__pycache__'), dirs_exist_ok=True)
    shutil.copytree(ROOT / 'desktop/webview', sources / 'desktop-recipe', ignore=shutil.ignore_patterns('__pycache__'), dirs_exist_ok=True)
    shutil.copyfile(ROOT / 'build-target.sh', sources / 'build-target.sh')


def is_runtime_notice(name):
    """Return True for files kept from packaged runtime archives for notice bundles."""
    return 'licenses' in name.parts or is_notice(name) or name.name in ('manifest.json', 'show_third_party_software_licenses.bat')


def add_runtime_archive(files, family, archive):
    """Copy notice files out of a packaged runtime archive into the notice bundle."""
    with tarfile.open(archive) as source:
        for member in source:
            name = Path(member.name)
            if name.is_absolute() or '..' in name.parts:
                raise RuntimeError('Unsafe runtime archive path: ' + member.name)
            if member.isfile() and is_runtime_notice(name):
                with source.extractfile(member) as stream:
                    files[family + '/' + member.name] = stream.read()


def add_distribution_notices(files):
    """Collect Debian notice files that accompany bundled native dependencies."""
    # Distribution notices supplement upstream compiler/runtime texts. Preserve
    # all of them: transitive native dependencies differ between distributions.
    for root in (Path('/usr/share/doc'), Path('/usr/share/common-licenses')):
        paths = root.glob('*/copyright') if root.name == 'doc' else root.glob('*')
        for path in paths:
            if path.is_file():
                files['distribution/' + str(path.relative_to('/usr/share'))] = path.read_bytes()
    inventory = Path('/build-packages.tsv')
    if inventory.exists():
        files['distribution/packages.tsv'] = inventory.read_bytes()


def write_bundle(files, output):
    """Write a deterministic tarball containing every collected notice file."""
    files['SHA256SUMS'] = ''.join(
        hashlib.sha256(value).hexdigest() + '  ' + name + '\n' for name, value in sorted(files.items())
    ).encode()
    output.parent.mkdir(parents=True, exist_ok=True)
    with tarfile.open(output, 'w:gz') as archive:
        for name, content in sorted(files.items()):
            entry = tarfile.TarInfo(name)
            entry.size, entry.mode = len(content), 0o644
            archive.addfile(entry, io.BytesIO(content))


def write_source_checksums(sources):
    """Write SHA256SUMS for every optional copied source artifact."""
    inventory = {str(path.relative_to(sources)): digest(path) for path in sources.rglob('*') if path.is_file()}
    (sources / 'SHA256SUMS').write_text(''.join(value + '  ' + name + '\n' for name, value in sorted(inventory.items())))


def main():
    """Assemble notice files and optional corresponding source artifacts."""
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--output', type=Path, required=True)
    parser.add_argument('--ocr', type=Path, required=True)
    parser.add_argument('--desktop', type=Path)
    parser.add_argument('--sources', type=Path)
    args = parser.parse_args()

    files = {}
    verify_terms()
    add_project_notices(files)
    add_go_modules(files, args.sources)
    add_go_toolchain(files, args.sources)
    if args.sources:
        collect_recipe_sources(args.sources)
    for family, archive in (('ocr', args.ocr), ('desktop', args.desktop)):
        if archive is not None:
            add_runtime_archive(files, family, archive)
    add_distribution_notices(files)
    write_bundle(files, args.output)
    if args.sources:
        write_source_checksums(args.sources)


if __name__ == '__main__':
    main()
