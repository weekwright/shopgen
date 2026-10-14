#!/usr/bin/env python3
"""Builds the docs site's home and commands pages from README.md.

The README is the source of truth. This script rewrites its paths and links
for the site (README links into docs/ become sibling links, repository files
become GitHub URLs), drops sections the site's navigation already covers,
and moves the command and configuration reference to docs/commands.md.

    python3 scripts/sync-docs.py          # rewrite docs/index.md and docs/commands.md
    python3 scripts/sync-docs.py --check  # exit 1 if they are out of date (CI)
"""
import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
GH = "https://github.com/weekwright/shopgen"
HEADER = "<!-- Generated from README.md by scripts/sync-docs.py. Edit the README, then run `make docs-sync`. -->\n\n"

# Sections that move to commands.md, and sections the site leaves out (the
# table of contents duplicates the site's navigation).
TO_COMMANDS = ["Commands", "Configuration"]
DROP = ["Contents"]


def split_sections(text):
    """Returns (preamble, [(title, body), ...]) split on '## ' headings."""
    parts = re.split(r"^## (.+)$", text, flags=re.M)
    preamble, rest = parts[0], parts[1:]
    return preamble, [(rest[i].strip(), rest[i + 1]) for i in range(0, len(rest), 2)]


def rewrite(md):
    md = md.replace('src="docs/assets/', 'src="assets/').replace('srcset="docs/assets/', 'srcset="assets/')
    md = re.sub(r"\]\(docs/([^)]+)\)", r"](\1)", md)
    # README anchors for sections that moved to commands.md.
    md = md.replace("](#commands)", "](commands.md)").replace("](#configuration)", "](commands.md#configuration)")

    def to_url(target):
        """Maps a README-relative target to a site link or a GitHub URL."""
        if re.match(r"^(https?:|mailto:|#)", target):
            return target
        path, _, anchor = target.partition("#")
        if (ROOT / "docs" / path).is_file():
            return target  # a page of this site
        if (ROOT / path).exists():
            kind = "tree" if (ROOT / path).is_dir() else "blob"
            return f"{GH}/{kind}/main/{path}" + (f"#{anchor}" if anchor else "")
        return target

    def repo_link(m):
        return f"]({to_url(m.group(1))})"

    md = re.sub(r'href="([^"]+)"', lambda m: f'href="{to_url(m.group(1))}"', md)
    md = re.sub(r"\]\(([^)]+)\)", repo_link, md)
    # Repository-relative image sources (the README's terminal screenshot) are fine as assets/.
    return md


def build():
    readme = (ROOT / "README.md").read_text()
    preamble, sections = split_sections(readme)
    # A visually hidden H1 stops Material adding a "Home" title above the
    # header image, and still names the page for search and screen readers.
    index = ["# shopgen { .sg-visually-hidden }\n\n" + rewrite(preamble).strip() + "\n"]
    commands = ["# Commands and configuration\n"]
    for title, body in sections:
        if title in DROP:
            continue
        chunk = f"\n## {title}\n{rewrite(body).rstrip()}\n"
        if title in TO_COMMANDS:
            commands.append(chunk.replace("](commands.md#", "](#"))
        else:
            index.append(chunk)
    return HEADER + "".join(index), HEADER + "".join(commands)


def main():
    index, commands = build()
    targets = {ROOT / "docs" / "index.md": index, ROOT / "docs" / "commands.md": commands}
    if "--check" in sys.argv:
        stale = [p.name for p, text in targets.items() if not p.exists() or p.read_text() != text]
        if stale:
            print(f"docs out of date with README.md: {', '.join(stale)}; run `make docs-sync`", file=sys.stderr)
            return 1
        return 0
    for path, text in targets.items():
        path.write_text(text)
        print(f"wrote {path.relative_to(ROOT)}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
