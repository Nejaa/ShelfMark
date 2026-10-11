#!/usr/bin/env python3
"""Collect exact Debian source archives for a package inventory.

Usage:
    python tools/debian_sources.py --inventory build-packages.tsv --output sources-dir

The inventory is expected to come from dpkg-query with binary and source package versions.
Each collected source package is checksum-verified before it is copied into the output tree.
"""

import argparse
from email.parser import Parser
import hashlib
import json
from pathlib import Path
import shutil
import subprocess
import tempfile


def digest(path):
    """Return the SHA-256 digest of *path*."""
    result = hashlib.sha256()
    with path.open('rb') as stream:
        for block in iter(lambda: stream.read(1024 * 1024), b''):
            result.update(block)
    return result.hexdigest()


def descriptor(path):
    """Parse a Debian .dsc file, including clear-signed descriptors."""
    text = path.read_text()
    # A .dsc may be clear-signed. Strip the signature envelope before parsing
    # its Debian control fields; apt already verifies the repository hashes.
    if text.startswith('-----BEGIN PGP SIGNED MESSAGE-----'):
        text = text.split('\n\n', 1)[1].split('-----BEGIN PGP SIGNATURE-----', 1)[0]
    return Parser().parsestr(text)


def read_sources(inventory):
    """Return the set of source package/version pairs referenced by the inventory."""
    sources = set()
    for line in inventory.read_text().splitlines():
        package, version, source, source_version, _ = line.split('\t')
        sources.add((source or package.split(':')[0], source_version or version))
    return sources


def verified_files(dsc, fields, pool):
    """Yield the .dsc and all checksum-verified source files it references."""
    checksums = fields['Checksums-Sha256']
    if not checksums:
        raise RuntimeError('Missing source checksums: ' + dsc.name)
    files = [dsc]
    for line in checksums.splitlines():
        if not line.strip():
            continue
        checksum, size, name = line.split()
        if Path(name).name != name or name in ('.', '..'):
            raise RuntimeError('Unsafe source filename: ' + name)
        path = pool / name
        if path.stat().st_size != int(size) or digest(path) != checksum:
            raise RuntimeError('Source checksum mismatch: ' + name)
        files.append(path)
    return files


def collect_source(dsc, pool, output, sources, collected):
    """Copy one verified source package into the output tree and record its manifest."""
    fields = descriptor(dsc)
    package, version = fields['Source'], fields['Version']
    identity = (package, version)
    if identity not in sources or identity in collected:
        raise RuntimeError('Unexpected source descriptor: ' + str(identity))

    destination = output / (package + '-' + version.replace(':', '_'))
    destination.mkdir(exist_ok=True)
    inventory = []
    for path in verified_files(dsc, fields, pool):
        target = destination / path.name
        shutil.copyfile(path, target)
        inventory.append({'file': str(target.relative_to(output)), 'sha256': digest(target)})
    collected.add(identity)
    return {'package': package, 'version': version, 'files': inventory}


def main():
    """Download every referenced Debian source package and write an inventory."""
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--inventory', type=Path, required=True)
    parser.add_argument('--output', type=Path, required=True)
    args = parser.parse_args()

    sources = read_sources(args.inventory)
    args.output.mkdir(parents=True, exist_ok=True)
    manifest, collected = [], set()
    with tempfile.TemporaryDirectory(dir=args.output, prefix='.downloads-') as scratch:
        pool = Path(scratch)
        # --only-source avoids binary-name collisions (e.g. serf). One batch
        # reads apt's metadata once. Missing exact versions abort collection.
        subprocess.run(
            ['apt-get', 'source', '--only-source', '--download-only', *[name + '=' + version for name, version in sorted(sources)]],
            cwd=pool,
            check=True,
        )
        for dsc in sorted(pool.glob('*.dsc')):
            manifest.append(collect_source(dsc, pool, args.output, sources, collected))
    if collected != sources:
        raise RuntimeError('Missing sources: ' + str(sorted(sources - collected)))
    (args.output / 'sources.json').write_text(json.dumps(manifest, indent=2) + '\n')
    (args.output / 'packages.tsv').write_bytes(args.inventory.read_bytes())


if __name__ == '__main__':
    main()
