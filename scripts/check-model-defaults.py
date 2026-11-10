#!/usr/bin/env python3
"""Verify the automatic CLI model choices against fresh public OpenAI guidance."""

from functools import lru_cache
import json
from pathlib import Path
import re
import sys
from urllib.request import Request, urlopen


SITE = "https://developers.openai.com/"
DOCS = SITE + "api/docs/"
SPEECH = DOCS + "guides/text-to-speech.md"
IMAGE = DOCS + "guides/image-generation.md"
TRANSLATION = DOCS + "guides/realtime-translation.md"
CAPTIONS = SITE + "cookbook/examples/voice_solutions/realtime_translation_guide.md"
CAPTION_EVENTS = SITE + "api/reference/resources/realtime/translation-client-events.md"
DEPRECATIONS = DOCS + "deprecations.md"
INVENTORY = Path("internal/modeldefaults/defaults.go")
MODEL_ID = r"[a-z0-9][a-z0-9.-]*"


@lru_cache(maxsize=None)
def fetch(url):
    request = Request(url, headers={"Accept": "text/markdown", "Cache-Control": "no-cache"})
    with urlopen(request, timeout=20) as response:
        if response.url != url or response.headers.get_content_type() != "text/markdown":
            raise ValueError(f"Expected the official Markdown page at {url}")
        return response.read().decode("utf-8")


def unique(pattern, text, source):
    matches = set(re.findall(pattern, text, re.MULTILINE))
    if len(matches) != 1:
        raise ValueError(f"Expected one unambiguous match in {source}")
    return matches.pop()


def runtime_models(source):
    # Read the entire runtime inventory so new roles require matching guidance.
    code = re.sub(r"//[^\n]*", "", source).strip()
    block = re.fullmatch(r"package modeldefaults\s+const\s*\(([^()]*)\)", code)
    if not block:
        raise ValueError(f"Unrecognized model declarations in {INVENTORY}")
    models = {}
    for line in block[1].strip().splitlines():
        match = re.fullmatch(r'\s*(\w+)\s*=\s*"(' + MODEL_ID + r')"\s*', line)
        if not match or match[1] in models:
            raise ValueError(f"Invalid model declaration in {INVENTORY}")
        models[match[1]] = match[2]
    return models


def image_recommendation(guide):
    # Older examples remain on the same page. Follow the link for new integrations.
    section = unique(r"For new integrations, use \[[^\]]+\]\(#([\w-]+)\)", guide, IMAGE)
    headings = list(re.finditer(r"^(#{1,6}) (.+)$", guide, re.MULTILINE))
    for i, heading in enumerate(headings):
        slug = re.sub(r"[^\w -]", "", heading[2].lower()).replace(" ", "-")
        if slug == section:
            end = next((h.start() for h in headings[i + 1:] if len(h[1]) <= len(heading[1])), len(guide))
            return unique(r"\[[^\]]+\]\(https://developers\.openai\.com/api/docs/models/(" +
                          MODEL_ID + r")\) for detailed visual work", guide[heading.end():end], IMAGE)
    raise ValueError(f"Missing recommended image model section in {IMAGE}")


def retired_models(page):
    # Only the affected model column denotes a retirement. Replacements can be current.
    deprecated = set()
    active_table = False
    for line in page.splitlines():
        if not line.startswith("|"):
            active_table = False
            continue
        cells = [cell.strip() for cell in re.split(r"(?<!\\)\|", line.strip("|"))]
        if cells[0].lower() == "shutdown date":
            active_table = True
        elif active_table and len(cells) > 1 and not cells[0].startswith("-"):
            deprecated.update(re.findall(r"(?<![\w.-])(" + MODEL_ID + r")(?![\w.-])", cells[1]))
    if not deprecated:
        raise ValueError(f"Missing model retirement data in {DEPRECATIONS}")
    return deprecated


def verify(models, read):
    recommendations = {
        "Speech": (unique(r"Migrate to `(" + MODEL_ID + r")` using the\s+\[Realtime API\]",
                          read(SPEECH), SPEECH), SPEECH, ["v1/realtime"]),
        "SavedImage": (image_recommendation(read(IMAGE)), IMAGE, ["v1/images/generations", "v1/images/edits"]),
        "Translation": (unique(r"Use \[`(" + MODEL_ID + r")`\]\([^)]+\) when your application should translate",
                               read(TRANSLATION), TRANSLATION), TRANSLATION, ["v1/realtime/translations"]),
        "TranslationTranscript": (unique(r"configure input transcription with `(" + MODEL_ID + r")`",
                                         read(CAPTIONS), CAPTIONS), CAPTIONS, []),
    }
    if models.keys() != recommendations.keys():
        roles = ", ".join(sorted(models.keys() ^ recommendations.keys()))
        raise ValueError(f"Runtime and release guidance disagree about model roles: {roles}")
    retired = retired_models(read(DEPRECATIONS))
    for role, (recommended, guide, endpoints) in recommendations.items():
        model = models[role]
        if model != recommended:
            raise ValueError(f"{role} selects {model}; current guidance recommends {recommended}. See {guide}")
        if model in retired:
            raise ValueError(f"{role} selects deprecated {model}. See {DEPRECATIONS}")
        model_url = DOCS + "models/" + model + ".md"
        detail = read(model_url)
        if unique(r"^Model ID: `(" + MODEL_ID + r")`$", detail, model_url) != model:
            raise ValueError(f"Could not confirm model identity at {model_url}")
        for endpoint in endpoints:
            pattern = r"^\|[^|]+\|\s*`" + re.escape(endpoint) + r"`\s*\|\s*Supported\s*\|$"
            if not re.search(pattern, detail, re.MULTILINE):
                raise ValueError(f"{model} lacks documented support for {endpoint}. See {model_url}")
        if role == "TranslationTranscript":
            # A nested field in the translation session has no standalone endpoint.
            blocks = re.findall(r"```json\s*\n(.*?)\n```", read(CAPTION_EVENTS), re.DOTALL)
            events = (json.loads(block) for block in blocks)
            documented = {e.get("session", {}).get("audio", {}).get("input", {}).get("transcription", {}).get("model")
                          for e in events if e.get("type") == "session.update"}
            if documented - {None} != {model}:
                raise ValueError(f"Could not confirm session.update input transcription at {CAPTION_EVENTS}")
        print(f"{role}: {model} ({', '.join(endpoints) or 'v1/realtime/translations, input transcription'}) {guide}")


if __name__ == "__main__":
    try:
        verify(runtime_models(INVENTORY.read_text()), fetch)
    except (OSError, ValueError) as error:
        sys.exit(f"Model default verification failed: {error}")
