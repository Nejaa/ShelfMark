#!/usr/bin/env python3
"""Package accompanying sources and checksum release assets (GitHub's 2 GiB cap)."""
import argparse
from pathlib import Path
import subprocess
import tempfile
from notices import digest

# Leave room below GitHub's per-asset limit, including a multipart source bundle.
PART_SIZE = 1800 * 1024 * 1024


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--sources', type=Path, required=True)
    parser.add_argument('--output', type=Path, required=True)
    args = parser.parse_args()
    args.output.mkdir(parents=True, exist_ok=True)
    name = 'shelfmark-third-party-sources.tar.gz'
    with tempfile.TemporaryDirectory(dir=args.output) as scratch:
        archive = Path(scratch) / name
        subprocess.run(['tar', '-czf', str(archive), '-C', str(args.sources), 'third-party-sources'], check=True)
        if archive.stat().st_size <= PART_SIZE:
            archive.replace(args.output / name)
        else:
            with archive.open('rb') as source:
                index = 1
                while True:
                    block = source.read(1024 * 1024)
                    if not block:
                        break
                    with (args.output / (name + f'.part{index:03}')).open('wb') as output:
                        count = 0
                        while block:
                            output.write(block)
                            count += len(block)
                            if count == PART_SIZE:
                                break
                            block = source.read(min(1024 * 1024, PART_SIZE - count))
                    index += 1
    files = sorted(path for path in args.output.iterdir() if path.is_file() and path.name != 'SHA256SUMS')
    for path in files:
        if path.stat().st_size >= 2 * 1024 ** 3:
            raise RuntimeError('Release asset exceeds GitHub limit: ' + path.name)
    (args.output / 'SHA256SUMS').write_text(''.join(digest(path) + '  ' + path.name + '\n' for path in files))


if __name__ == '__main__':
    main()
