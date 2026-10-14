"""MkDocs build hooks for the shopgen docs site.

The Markdown in docs/ is written to render well on github.com too, so it
uses GitHub conventions. These hooks translate them for the site:

* README-style ``<picture>`` elements whose ``<source>`` targets
  ``prefers-color-scheme: dark`` become two ``<img>`` tags, tagged
  ``sg-only-light`` / ``sg-only-dark``, so diagrams follow the site's own
  light/dark toggle rather than only the operating system.
* GitHub alerts (``> [!NOTE]``, ``[!TIP]``, ``[!IMPORTANT]``,
  ``[!WARNING]``, ``[!CAUTION]``) become Material admonitions.
"""

import re

PICTURE = re.compile(
    r"<picture>\s*"
    r'<source\s+media="\(prefers-color-scheme:\s*dark\)"\s+srcset="(?P<dark>[^"]+)"\s*/?>\s*'
    r"<img\s+(?P<attrs>[^>]*?)\s*/?>\s*"
    r"</picture>",
    re.S,
)

SRC = re.compile(r'\ssrc="([^"]+)"')

BLOCKQUOTE = re.compile(r"<blockquote>(?P<body>.*?)</blockquote>", re.S)

# A paragraph that opens with an alert marker. Python-Markdown merges
# consecutive "> ..." blocks into one blockquote (GitHub does not), so one
# blockquote may hold several alerts; each marker starts a new one.
MARKER = re.compile(r"<p>\[!(?P<kind>NOTE|TIP|IMPORTANT|WARNING|CAUTION)\]\s*(?:<br\s*/?>)?\s*")

TITLES = {"NOTE": "Note", "TIP": "Tip", "IMPORTANT": "Important", "WARNING": "Warning", "CAUTION": "Caution"}


def _picture(m):
    attrs = " " + m.group("attrs")
    light = SRC.search(attrs)
    if not light:
        return m.group(0)
    rest = SRC.sub("", attrs).strip()
    return (
        f'<img {rest} src="{light.group(1)}" class="sg-only-light">'
        f'<img {rest} src="{m.group("dark")}" class="sg-only-dark">'
    )


def _alerts(html):
    def convert(m):
        body = m.group("body")
        markers = list(MARKER.finditer(body))
        if not markers:
            return m.group(0)
        out = []
        lead = body[: markers[0].start()].strip()
        if lead:
            out.append(f"<blockquote>{lead}</blockquote>")
        for i, mk in enumerate(markers):
            end = markers[i + 1].start() if i + 1 < len(markers) else len(body)
            kind = mk.group("kind")
            content = "<p>" + body[mk.end():end].strip()
            out.append(
                f'<div class="admonition {kind.lower()}">'
                f'<p class="admonition-title">{TITLES[kind]}</p>{content}</div>'
            )
        return "".join(out)

    return BLOCKQUOTE.sub(convert, html)


def on_page_content(html, page, config, files):
    html = PICTURE.sub(_picture, html)
    return _alerts(html)
