"""Check the CLI's model choices with local request fixtures."""

import json
import os
from pathlib import Path
import re
import subprocess
import sys


ROOT = Path(__file__).resolve().parents[2]
MODULE = "github.com/openai/openai-cli"
CASES = {
    "cmd/openai": {
        "TestMainSpeakRealtime": ("openai speak, GET /v1/realtime", ("WAV_file",)),
        "TestMainImageGenerationDefaultsAndRequestOverrides": (
            "openai images generate, POST /v1/images/generations", ("defaults", "explicit_model_retains_API_defaults")
        ),
        "TestMainImageUploadSavingDefaultsAndSources": (
            "openai images edit and create-variation, POST /v1/images/{edits,variations}", ("edit", "create-variation")
        ),
        "TestMainRetiredImageVariationStopsBeforeUpload": (
            "openai images create-variation, retired OpenAI endpoint", ("default", "regional_environment", "flag_overrides_provider")
        ),
    },
    "internal/commands/audio": {
        "TestFreshSpeakComposerRequest": ("openai speak, fresh composer, GET /v1/realtime", ()),
        "TestAudioReviewHistoryRestoresSettingsWithoutGeneration": ("openai speak, saved settings", ()),
        "TestAudioGenerationSendsSelectedRequestSettings": ("openai speak, selected model, POST /v1/audio/speech", ()),
        "TestLiveTranslationSDKRoundTripPreservesConfigurationAudioAndDrain": (
            "openai audio translate --live, GET /v1/realtime/translations and session.update input transcription", ()
        ),
    },
    "internal/commands/images": {
        "TestImagePickerPromptSubmitsExactlyOnce": ("openai images generate, fresh picker", ()),
        "TestImagePickerDraftRestoresEditablePromptAndSettings": (
            "openai images generate, saved model", ("multiline_Unicode", "cleared_prompt")
        ),
        "TestImagePickerStateIgnoresLegacyPrompt": (
            "openai images generate, legacy saved model", ("legacy_prompt=false", "legacy_prompt=true")
        ),
    },
    "internal/releaseworkflow": {
        "TestGeneratedModelDefaults": ("generated API commands with omitted model flags", ()),
    },
}


def main():
    expected = {}
    names = []
    for package, tests in CASES.items():
        for test, (route, children) in tests.items():
            names.append(re.escape(test))
            for name in [test, *(f"{test}/{child}" for child in children)]:
                expected[(f"{MODULE}/{package}", name)] = route

    selector = "^(" + "|".join(names) + ")$"
    env = {key: value for key, value in os.environ.items() if not key.startswith("OPENAI_")}
    result = subprocess.run(
        ["go", "test", "-count=1", "-json", "-run", selector, *("./" + package for package in CASES)],
        cwd=ROOT, env=env, capture_output=True, text=True, check=False,
    )
    passed = set()
    for line in result.stdout.splitlines():
        event = json.loads(line)
        if event.get("Action") == "pass" and event.get("Test"):
            passed.add((event["Package"], event["Test"]))
        if result.returncode and event.get("Output"):
            sys.stderr.write(event["Output"])

    missing = expected.keys() - passed
    for package, test in sorted(missing):
        print(f"Missing Go test pass: {package} {test} ({expected[package, test]})", file=sys.stderr)
    if result.returncode or missing:
        sys.stderr.write(result.stderr)
        return 1
    print(f"Verified {len(expected)} required Go test passes for CLI model requests.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
