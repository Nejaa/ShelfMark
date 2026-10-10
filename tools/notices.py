#!/usr/bin/env python3
"""Collect original notices and optional sources from the resolved build inputs."""
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
    result = hashlib.sha256()
    with path.open('rb') as stream:
        for block in iter(lambda: stream.read(1024 * 1024), b''):
            result.update(block)
    return result.hexdigest()


def objects(data):
    decoder = json.JSONDecoder()
    while data.strip():
        data = data.lstrip()
        value, end = decoder.raw_decode(data)
        yield value
        data = data[end:]


def is_notice(path):
    name = path.name.lower()
    return name.startswith(('license', 'licence', 'copying', 'copyright', 'notice', 'unlicense', 'authors', 'patents', 'credits', 'ofl', 'fontlog'))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--output', type=Path, required=True)
    parser.add_argument('--ocr', type=Path, required=True)
    parser.add_argument('--desktop', type=Path)
    parser.add_argument('--sources', type=Path)
    args = parser.parse_args()
    files = {}
    terms = json.loads((ROOT / 'licenses/webview2-terms.json').read_text())
    if digest(ROOT / 'licenses' / terms['file']) != terms['sha256']:
        raise RuntimeError('WebView2 terms do not match their recorded checksum')

    def add(name, content):
        files[name] = content

    for name in ('LICENSE', 'THIRD_PARTY.md'):
        add(name, (ROOT / name).read_bytes())
    for path in (ROOT / 'licenses').rglob('*'):
        if path.is_file():
            add(str(path.relative_to(ROOT)), path.read_bytes())

    # Download first: a module graph can contain modules absent from the cache.
    downloaded = subprocess.check_output(['go', 'mod', 'download', '-json', 'all'], cwd=ROOT, text=True)
    modules = []
    fonts = json.loads((ROOT / 'licenses/fonts.json').read_text())
    seen_fonts = set()
    for module in objects(downloaded):
        if 'Error' in module:
            raise RuntimeError(module['Error'])
        directory = Path(module['Dir'])
        for font in fonts:
            if font['module'] == module['Path']:
                if digest(directory / font['file']) != font['sha256']:
                    raise RuntimeError('Embedded font changed; review its license: ' + font['file'])
                seen_fonts.add((font['module'], font['file']))
        notices = [p for p in directory.rglob('*') if p.is_file() and is_notice(p)]
        if not notices:
            raise RuntimeError('No license found for ' + module['Path'])
        prefix = 'go-modules/' + module['Path'] + '@' + module['Version']
        for path in notices:
            add(prefix + '/' + str(path.relative_to(directory)), path.read_bytes())
        modules.append({key: module[key] for key in ('Path', 'Version', 'Sum', 'GoModSum') if key in module})
        if args.sources:
            destination = args.sources / prefix
            destination.mkdir(parents=True, exist_ok=True)
            for key in ('Zip', 'GoMod'):
                path = Path(module[key])
                shutil.copyfile(path, destination / path.name)
    if len(seen_fonts) != len(fonts):
        raise RuntimeError('Font notice inventory does not match the module graph')
    add('go-modules.json', (json.dumps(modules, indent=2) + '\n').encode())

    goroot = Path(subprocess.check_output(['go', 'env', 'GOROOT'], text=True).strip())
    for path in goroot.rglob('*'):
        if path.is_file() and is_notice(path):
            add('go/' + str(path.relative_to(goroot)), path.read_bytes())
    add('go/version.txt', subprocess.check_output(['go', 'version']))
    if args.sources:
        shutil.copytree(goroot / 'src', args.sources / 'go/src', dirs_exist_ok=True)
        shutil.copyfile(goroot / 'LICENSE', args.sources / 'go/LICENSE')
        # The same verified inputs as the OCR packager, including model weights.
        import sys
        sys.path.insert(0, str(ROOT))
        from runtime.ocr.package import download
        manifest = json.loads((ROOT / 'runtime/ocr/sources.json').read_text())
        destination = args.sources / 'ocr'
        destination.mkdir(parents=True, exist_ok=True)
        for entry in manifest.values():
            path = download(entry, ROOT / 'target/cache/ocr/downloads')
            shutil.copyfile(path, destination / path.name)
        shutil.copytree(ROOT / 'runtime/ocr', destination / 'recipe', ignore=shutil.ignore_patterns('__pycache__'), dirs_exist_ok=True)
        shutil.copytree(ROOT / 'desktop/webview', args.sources / 'desktop-recipe', ignore=shutil.ignore_patterns('__pycache__'), dirs_exist_ok=True)
        shutil.copyfile(ROOT / 'build-target.sh', args.sources / 'build-target.sh')

    for family, archive in (('ocr', args.ocr), ('desktop', args.desktop)):
        if archive is None:
            continue
        with tarfile.open(archive) as source:
            for member in source:
                name = Path(member.name)
                if name.is_absolute() or '..' in name.parts:
                    raise RuntimeError('Unsafe runtime archive path: ' + member.name)
                if member.isfile() and ('licenses' in name.parts or is_notice(name) or name.name in ('manifest.json', 'show_third_party_software_licenses.bat')):
                    with source.extractfile(member) as stream:
                        add(family + '/' + member.name, stream.read())

    # Distribution notices supplement upstream compiler/runtime texts. Preserve
    # all of them: transitive native dependencies differ between distributions.
    for root in (Path('/usr/share/doc'), Path('/usr/share/common-licenses')):
        paths = root.glob('*/copyright') if root.name == 'doc' else root.glob('*')
        for path in paths:
            if path.is_file():
                add('distribution/' + str(path.relative_to('/usr/share')), path.read_bytes())
    inventory = Path('/build-packages.tsv')
    if inventory.exists():
        add('distribution/packages.tsv', inventory.read_bytes())

    add('SHA256SUMS', ''.join(hashlib.sha256(value).hexdigest() + '  ' + name + '\n'
                             for name, value in sorted(files.items())).encode())
    args.output.parent.mkdir(parents=True, exist_ok=True)
    with tarfile.open(args.output, 'w:gz') as output:
        for name, content in sorted(files.items()):
            entry = tarfile.TarInfo(name)
            entry.size, entry.mode = len(content), 0o644
            output.addfile(entry, io.BytesIO(content))
    if args.sources:
        inventory = {str(path.relative_to(args.sources)): digest(path)
                     for path in args.sources.rglob('*') if path.is_file()}
        (args.sources / 'SHA256SUMS').write_text(''.join(value + '  ' + name + '\n' for name, value in sorted(inventory.items())))


if __name__ == '__main__':
    main()
