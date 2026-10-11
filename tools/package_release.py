#!/usr/bin/env python3
"""Package release-side source bundles and checksum assets.

Usage:
    python tools/package_release.py --sources source-export --output release-dir

The command keeps generated assets below GitHub's per-asset size limit and writes
SHA256SUMS for every emitted file.
"""

import argparse
from pathlib import Path
import subprocess
import tempfile

from notices import digest


# Leave room below GitHub's per-asset limit, including a multipart source bundle.
PART_SIZE = 1800 * 1024 * 1024


def write_source_archive(sources, output, name):
    """Create the third-party source tarball, splitting it when it grows too large."""
    with tempfile.TemporaryDirectory(dir=output) as scratch:
        archive = Path(scratch) / name
        subprocess.run(['tar', '-czf', str(archive), '-C', str(sources), 'third-party-sources'], check=True)
        if archive.stat().st_size <= PART_SIZE:
            archive.replace(output / name)
        else:
            split_archive(archive, output, name)


def split_archive(archive, output, name):
    """Split an oversized source tarball into numbered parts."""
    with archive.open('rb') as source:
        index = 1
        while write_part(source, output / (name + f'.part{index:03}')):
            index += 1


def write_part(source, target):
    """Write up to PART_SIZE bytes; return False when the source was already exhausted."""
    block = source.read(min(1024 * 1024, PART_SIZE))
    if not block:
        return False
    with target.open('wb') as output:
        count = 0
        while block:
            output.write(block)
            count += len(block)
            if count == PART_SIZE:
                break
            block = source.read(min(1024 * 1024, PART_SIZE - count))
    return True


def main():
    """Generate source bundle artifacts and their checksum manifest."""
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--sources', type=Path, required=True)
    parser.add_argument('--output', type=Path, required=True)
    args = parser.parse_args()

    args.output.mkdir(parents=True, exist_ok=True)
    write_source_archive(args.sources, args.output, 'shelfmark-third-party-sources.tar.gz')

    files = sorted(path for path in args.output.iterdir() if path.is_file() and path.name != 'SHA256SUMS')
    for path in files:
        if path.stat().st_size >= 2 * 1024 ** 3:
            raise RuntimeError('Release asset exceeds GitHub limit: ' + path.name)
    (args.output / 'SHA256SUMS').write_text(''.join(digest(path) + '  ' + path.name + '\n' for path in files))


if __name__ == '__main__':
    main()
