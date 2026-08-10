#!/usr/bin/env python3
"""Extract / inject the <script type="__bundler/template"> payload of a bundled page.

The .html files at the repo root are self-contained bundler output: fonts and the
dc-runtime ship as base64 blobs in the manifest, and the actual page source (the
<x-dc> template plus its `class Component extends DCLogic` script) lives JSON-encoded
in the template island. Editing a page means editing that template, not the 560KB wrapper.

  python3 tools/dcbundle.py extract gift.html out/gift.template.html
  python3 tools/dcbundle.py inject  gift.html out/gift.template.html
"""
import json
import re
import sys

TEMPLATE_RE = re.compile(
    r'(<script type="__bundler/template">)(.*?)(</script>)', re.S)


def read(path):
    with open(path, encoding='utf8') as f:
        return f.read()


def extract(page, out):
    m = TEMPLATE_RE.search(read(page))
    if not m:
        sys.exit(f'{page}: no __bundler/template island')
    with open(out, 'w', encoding='utf8') as f:
        f.write(json.loads(m.group(2)))
    print(f'extracted {page} -> {out}')


def inject(page, tmpl):
    src = read(page)
    if not TEMPLATE_RE.search(src):
        sys.exit(f'{page}: no __bundler/template island')
    # Match the bundler's own encoding: literal non-ASCII, and every '/' after a
    # '<' escaped as \\u002F so a </script> in the payload can't close the island.
    payload = json.dumps(read(tmpl), ensure_ascii=False).replace('</', '<\\u002F')
    out = TEMPLATE_RE.sub(
        lambda m: m.group(1) + '\n' + payload + '\n  ' + m.group(3), src, count=1)
    with open(page, 'w', encoding='utf8') as f:
        f.write(out)
    print(f'injected {tmpl} -> {page}')


if __name__ == '__main__':
    if len(sys.argv) != 4 or sys.argv[1] not in ('extract', 'inject'):
        sys.exit(__doc__)
    (extract if sys.argv[1] == 'extract' else inject)(sys.argv[2], sys.argv[3])
