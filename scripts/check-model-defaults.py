#!/usr/bin/env python3
"""Verify the automatic CLI model choices against fresh public OpenAI guidance."""

from functools import lru_cache
import hashlib
import json
from pathlib import Path
import re
import subprocess
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

# Review changes to model advice even if the source retains an older recommendation.
REVIEWED_GUIDANCE = {
    SPEECH: "5d7de5ea03eabc2ee2a34272e6128d37b90442c80592df4f13725e76638647e7",
    IMAGE: "00c36c0421267ee7667b35c2246bb6886f87f493447a768825c05025b42bfff5",
    TRANSLATION: "28d362b54b8f964cd96b1ab19b34b520f5e8163ed5f95ff04880210f5513da81",
    CAPTIONS: "ac7e0c6762d96fcb434779e9cfd4c7a702d39657bed7e7677a445984eee7c7dc",
}


def guidance_digest(page):
    prose = re.sub(r"^```[^\n]*\n.*?^```[ \t]*$", "", page, flags=re.MULTILINE | re.DOTALL)
    # New recommendations may use unfamiliar wording or an entirely new model family.
    return hashlib.sha256(" ".join(prose.split()).encode()).hexdigest()


def verify_reviewed_guidance(read, reviewed):
    for url, recorded in reviewed.items():
        fresh = guidance_digest(read(url))
        if fresh != recorded:
            raise ValueError(f"Model guidance changed. Review {url} before recording its new digest {fresh}")


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


def retirements(page):
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
            deprecated.update(re.findall(r"\b(v1/[\w/-]+)", cells[1]))
    if not deprecated:
        raise ValueError(f"Missing model retirement data in {DEPRECATIONS}")
    return deprecated


def check_caption_field(page, model):
    # Translation accepts a nested transcriber, not a standalone transcription request.
    documented = set()
    for block in re.findall(r"```json\s*\n(.*?)\n```", page, re.DOTALL):
        event = json.loads(block)
        if event.get("type") == "session.update":
            audio_input = event["session"].get("audio", {}).get("input", {})
            if audio_input.get("transcription"):
                documented.add(audio_input["transcription"]["model"])
    if documented != {model}:
        raise ValueError(f"Could not confirm session.update input transcription at {CAPTION_EVENTS}")


def verify(models, read):
    speech = unique(r"Migrate to `(" + MODEL_ID + r")` using the\s+\[Realtime API\]", read(SPEECH), SPEECH)
    translation = unique(r"Use \[`(" + MODEL_ID + r")`\]\([^)]+\) when your application should translate",
                         read(TRANSLATION), TRANSLATION)
    captions = unique(r"configure input transcription with `(" + MODEL_ID + r")`", read(CAPTIONS), CAPTIONS)
    recommendations = {
        "Speech": (speech, SPEECH, ["v1/realtime"]),
        "SavedImage": (image_recommendation(read(IMAGE)), IMAGE, ["v1/images/generations", "v1/images/edits"]),
        "Translation": (translation, TRANSLATION, ["v1/realtime/translations"]),
        "TranslationTranscript": (captions, CAPTIONS, ["v1/realtime/translations"]),
    }
    if models.keys() != recommendations.keys():
        roles = ", ".join(sorted(models.keys() ^ recommendations.keys()))
        raise ValueError(f"Runtime and release guidance disagree about model roles: {roles}")
    retired = retirements(read(DEPRECATIONS))
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
        statuses = re.findall(r"^Status:\s*(.+)$", detail, re.MULTILINE)
        if any(status.strip().lower() not in {"active", "generally available"} for status in statuses):
            raise ValueError(f"{role} selects a model with an inactive or unrecognized status. See {model_url}")
        for route in endpoints:
            if route in retired:
                raise ValueError(f"{role} uses deprecated endpoint {route}. See {DEPRECATIONS}")
            if role == "TranslationTranscript":
                check_caption_field(read(CAPTION_EVENTS), model)
            else:
                route_pattern = r"^\|[^|]+\|\s*`" + re.escape(route) + r"`\s*\|\s*([^|]+?)\s*\|$"
                if unique(route_pattern, detail, model_url) != "Supported":
                    raise ValueError(f"{model} lacks documented support for {route}. See {model_url}")
        print(f"{role}: {model} ({', '.join(endpoints)}) {guide}", flush=True)


if __name__ == "__main__":
    try:
        verify(runtime_models(INVENTORY.read_text()), fetch)
        verify_reviewed_guidance(fetch, REVIEWED_GUIDANCE)
        subprocess.run([sys.executable, "-I", "scripts/ci/verify_model_requests.py"], check=True)
    except (OSError, ValueError, subprocess.CalledProcessError) as error:
        sys.exit(f"Model default verification failed: {error}")
