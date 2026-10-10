#!/usr/bin/env python3
"""Append direct download links to a GitHub release description."""
import argparse
from pathlib import Path
import re
from urllib.parse import quote


START = '<!-- shelfmark-downloads:start -->'
END = '<!-- shelfmark-downloads:end -->'
DOWNLOADS = (
    ('Linux x64', 'shelfmark-linux-amd64'),
    ('Windows x64', 'shelfmark-windows-amd64.exe'),
    ('Docker (Linux x64)', 'shelfmark-docker-linux-amd64.tar.gz'),
)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--repository', required=True, help='GitHub owner/repository')
    parser.add_argument('--tag', required=True)
    parser.add_argument('--input', type=Path, required=True)
    parser.add_argument('--output', type=Path, required=True)
    args = parser.parse_args()

    repository = quote(args.repository, safe='/')
    tag = quote(args.tag, safe='')
    base = f'https://github.com/{repository}/releases/download/{tag}'
    guide = f'https://github.com/{repository}/blob/{tag}/README.md'

    # Retries replace only our marked section, retaining generated notes and
    # any manually edited text. Put the updated section at the bottom again.
    existing = args.input.read_text(encoding='utf-8')
    body = re.sub(re.escape(START) + r'.*?' + re.escape(END), '', existing,
                  flags=re.DOTALL).rstrip()
    section = [START, '## Downloads', '', '| Platform | Download |', '| --- | --- |']
    for platform, filename in DOWNLOADS:
        section.append(f'| {platform} | [{filename}]({base}/{filename}) |')
    section.extend([
        '',
        f'[Checksums]({base}/SHA256SUMS) · [Usage and requirements]({guide})',
        '',
        'License notices and source archives are available under **Assets**.',
        END,
    ])
    prefix = body + '\n\n' if body else ''
    args.output.write_text(prefix + '\n'.join(section) + '\n', encoding='utf-8')


if __name__ == '__main__':
    main()
