#!/usr/bin/env python3
"""Sanity-check a dc template: JS syntax of the Component script + tag balance.

  python3 tools/dccheck.py path/to/gift.template.html
"""
import re
import subprocess
import sys
import tempfile

SCRIPT_RE = re.compile(r'<script type="text/x-dc"[^>]*>(.*?)</script>', re.S)
VOID = {'area', 'base', 'br', 'col', 'embed', 'hr', 'img', 'input', 'link',
        'meta', 'param', 'source', 'track', 'wbr'}


def check_js(src):
    m = SCRIPT_RE.search(src)
    if not m:
        return []  # static page, no Component logic
    with tempfile.NamedTemporaryFile('w', suffix='.js', delete=False) as f:
        # `class Component extends DCLogic` needs DCLogic bound to parse-check.
        f.write('const DCLogic = class {}; const React = {};\n' + m.group(1))
        path = f.name
    r = subprocess.run(['node', '--check', path], capture_output=True, text=True)
    return [] if r.returncode == 0 else ['JS syntax: ' + r.stderr.strip()]


def check_tags(src):
    """Catch the '</div' and unclosed-<blockquote> class of typo in the markup."""
    body = src[src.index('<x-dc>') + len('<x-dc>'):src.index('</x-dc>')]
    errs = []
    for i, line in enumerate(body.split('\n'), 1):
        # An opening '<' or '</' that never reaches its '>' on the same line.
        for m in re.finditer(r'<(/?)([a-zA-Z][\w-]*)', line):
            rest = line[m.end():]
            if '>' not in rest.split('<')[0]:
                errs.append(f'line {i}: unterminated <{m.group(1)}{m.group(2)} tag')
    stack, pos = [], []
    for m in re.finditer(r'<(/?)([a-zA-Z][\w-]*)([^>]*)>', body):
        closing, tag, attrs = m.group(1), m.group(2).lower(), m.group(3)
        if tag in VOID or attrs.rstrip().endswith('/'):
            continue
        if not closing:
            stack.append(tag)
            pos.append(body[:m.start()].count('\n') + 1)
        elif stack and stack[-1] == tag:
            stack.pop()
            pos.pop()
        else:
            errs.append(f'line {body[:m.start()].count(chr(10)) + 1}: </{tag}> '
                        f'closes but <{stack[-1] if stack else "nothing"}> is open')
    for tag, ln in zip(stack, pos):
        errs.append(f'line {ln}: <{tag}> never closed')
    return errs


if __name__ == '__main__':
    if len(sys.argv) != 2:
        sys.exit(__doc__)
    src = open(sys.argv[1], encoding='utf8').read()
    errs = check_js(src) + check_tags(src)
    for e in errs:
        print('FAIL ' + e)
    print('OK' if not errs else f'{len(errs)} problem(s)')
    sys.exit(1 if errs else 0)
