# Third-party software

Shelfmark's own code is licensed under MIT. Dependencies, fonts, models, native
runtimes, and container operating-system files retain their upstream licenses.
MIT does not replace their attribution, source-distribution, or other conditions.

## Release contents

Every scripted executable embeds a notices archive, downloadable through
`/api/licenses`. Companion notices archives are also published with the binaries.
They contain exact Go module versions and upstream license/NOTICE files, Go's
license, OCR notices and pinned manifests, and desktop runtime notices where used.
The container includes its notices under `/usr/share/shelfmark/licenses`; Debian's
package copyrights and common license texts remain in `/usr/share/doc` and
`/usr/share/common-licenses`.

Releases also include Shelfmark source and a third-party source bundle. The latter
contains exact Debian source packages (including original archives, Debian patches
and `.dsc` files), Go module archives, Go standard-library source, and OCR source
archives and models. Build/package scripts in Shelfmark source describe native
compiler flags and alterations, including the Windows Tesseract CMake adjustment
and Linux library path relocation. SHA-256 inventories identify supplied files.
Source collection fails the release if a required exact package version cannot be
obtained. Some build-only components are included to keep source coverage complete.

Download source bundles from the same release as the executable or image. Preserve
notices and provide the matching sources when redistributing applicable components.
Large source bundles may be split into numbered parts; concatenate them in filename
order before extracting. Sources are accompanying downloads, not embedded inside
the executable. Keep them available for as long as the corresponding binaries are
available.

## Component families

| Component | Licensing details |
| --- | --- |
| Go modules and standard library | Original MIT, BSD, Apache and other upstream texts are collected from the resolved module graph; nested notices are retained. See the generated inventory rather than treating all modules as one license. |
| Roboto font embedded by pdfcpu | Apache-2.0; original Google copyright, trademark and license metadata are recorded in `licenses/Roboto-NOTICE.txt`. Its file hash is verified against `licenses/fonts.json`. |
| Tesseract and English/French `tessdata_best` models | Apache-2.0; original texts and model versions are retained. |
| Leptonica | Its upstream BSD-style license is retained. |
| GCC/MinGW compiler runtimes | GCC license texts and runtime exception, MinGW notices and the build distribution's actual package notices are retained. |
| Linux GTK/WebKitGTK stack and supporting tools | Mixed licenses, including LGPL/GPL, BSD/MIT and font licenses. Exact distribution notices and source packages accompany the release. These components are separate native helpers, tools and dynamically loaded libraries. |
| Windows WebView2 loader and Fixed Version runtime | Microsoft software terms, plus the runtime's third-party notices. These components are not licensed under MIT. |
| Debian container base and CA certificates | Distribution package notices are retained; matching package sources accompany the image archive. |

## Rebuilding native components

The public source and build scripts permit replacing/rebuilding the helper and
its libraries and repackaging them into Shelfmark. Use `build-target.sh` or the
Docker build stages; `WEBVIEW_SYSROOT` can select a replacement Linux dependency
root. No Shelfmark term forbids modification or reverse engineering of LGPL
components to debug modifications. Microsoft components remain subject to their
separate terms, including applicable third-party licensing exceptions.

## Windows runtime terms

The unmodified Fixed Version terms are in
[licenses/WebView2-Fixed-Version.html](licenses/WebView2-Fixed-Version.html).
[licenses/webview2-terms.json](licenses/webview2-terms.json) records the official
source, retrieval date and content hash. The loader's separate terms come from its
pinned Microsoft NuGet archive. Native runtime files and their notices are kept
intact when packaging. WebView2 also contains embedded third-party credits; its
original `show_third_party_software_licenses.bat` opens those credits from the
extracted runtime while Shelfmark is running. The companion archive includes
the original license files and that script, not a replacement for embedded credits.

Windows desktop use requires agreement to the Fixed Version runtime terms. The
first desktop launch presents a native confirmation with a link to the terms
served by Shelfmark at `/api/webview2-license`. Acceptance is stored beside the
executable; declining keeps the HTTP server available. This agreement applies to
Microsoft's runtime, not to Shelfmark's MIT-licensed code. Microsoft's offer for applicable open-source components is retained in the terms
and points to <https://thirdpartysource.microsoft.com>. Those components are not
covered by the Debian/Go/OCR source bundle. Redistributors must
preserve the Microsoft terms and the required end-user notices and conditions.

## License references

- [MIT](https://opensource.org/license/mit)
- [GNU LGPL 2.1](https://www.gnu.org/licenses/old-licenses/lgpl-2.1.html)
- [GNU GPL 2](https://www.gnu.org/licenses/old-licenses/gpl-2.0.html)
- [GCC runtime exception](https://www.gnu.org/licenses/gcc-exception-3.1.html)
- [Microsoft WebView2 distribution](https://learn.microsoft.com/en-us/microsoft-edge/webview2/concepts/distribution)
