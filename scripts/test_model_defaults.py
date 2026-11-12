"""Offline tests for the release check. Fixtures contain no account data."""

from contextlib import redirect_stdout
from importlib.util import module_from_spec, spec_from_file_location
from io import StringIO
from pathlib import Path
import unittest


spec = spec_from_file_location("model_check", Path(__file__).with_name("check-model-defaults.py"))
check = module_from_spec(spec)
spec.loader.exec_module(check)


class ModelDefaultsTest(unittest.TestCase):
    def setUp(self):
        self.models = {"Speech": "voice", "SavedImage": "image", "Translation": "translate",
                       "TranslationText": "text", "TranslationTranscript": "captions"}
        self.pages = {
            check.SPEECH: "Migrate to `voice` using the\n[Realtime API](guide). Older examples use `old`.",
            check.IMAGE: ("### GPT-Image-4.0\n"
                          "[A](https://developers.openai.com/api/docs/models/image) for detailed visual work\n"
                          "### GPT-Image-3.0\n"
                          "[A](https://developers.openai.com/api/docs/models/old) for detailed visual work\n"
                          "For new integrations, use [the current models](#gpt-image-40)"),
            check.TRANSLATION: "Use [`translate`](model) when your application should translate",
            check.CAPTIONS: "For translation, configure input transcription with `captions`.",
            check.CAPTION_EVENTS: '```json\n{"type":"session.update","session":{"audio":{"input":'
                                  '{"transcription":{"model":"captions"}}}}}\n```',
            check.DEPRECATIONS: "| Shutdown date | Model / system | Recommended replacement |\n"
                                "|---|---|---|\n| Yesterday | `old` \\| `older` | `voice` |\n"
                                "| Tomorrow | `gpt-5-mini` \\| `gpt-5-mini-2025-08-07` | `text` |",
        }
        for model, routes in [("voice", ["v1/realtime"]), ("image", ["v1/images/generations", "v1/images/edits"]),
                              ("translate", ["v1/realtime/translations"]), ("text", ["v1/responses"]),
                              ("captions", [])]:
            self.pages[check.DOCS + "models/" + model + ".md"] = "\n".join(
                [f"Model ID: `{model}`"] + [f"| API | `{route}` | Supported |" for route in routes]
            )

    def verify(self):
        with redirect_stdout(StringIO()):
            check.verify(self.models, self.pages.__getitem__)

    def test_unchanged_recommendations(self):
        self.verify()

    def test_new_or_missing_role(self):
        self.models["FutureCommand"] = "new"
        with self.assertRaisesRegex(ValueError, "FutureCommand"):
            self.verify()
        del self.models["FutureCommand"], self.models["Speech"]
        with self.assertRaisesRegex(ValueError, "Speech"):
            self.verify()

    def test_stale_default_despite_old_examples(self):
        self.pages[check.SPEECH] = "Migrate to `new-voice` using the [Realtime API](guide). Example: `voice`."
        with self.assertRaisesRegex(ValueError, "current guidance recommends new-voice"):
            self.verify()

    def test_missing_text_translation_role(self):
        del self.models["TranslationText"]
        with self.assertRaisesRegex(ValueError, "model roles: TranslationText"):
            self.verify()

    def test_text_translation_follows_replacement_not_original_model(self):
        self.models["TranslationText"] = "gpt-5-mini"
        with self.assertRaisesRegex(ValueError, "TranslationText selects gpt-5-mini; current guidance recommends text"):
            self.verify()

    def test_text_translation_requires_one_recommendation(self):
        previous = self.pages[check.DEPRECATIONS]
        for changed in [previous.replace("`gpt-5-mini`", "`gpt-5-mini-preview`"),
                        previous + "\n| Tomorrow | `gpt-5-mini` | `other` |",
                        previous.replace("| `text` |", "| `text` or `other` |")]:
            with self.subTest(page=changed):
                self.pages[check.DEPRECATIONS] = changed
                with self.assertRaisesRegex(ValueError, "unambiguous"):
                    self.verify()

    def test_text_translation_requires_responses_support(self):
        url = check.DOCS + "models/text.md"
        for support in ["Not supported", ""]:
            with self.subTest(support=support):
                self.pages[url] = "Model ID: `text`\n| API | `v1/chat/completions` | Supported |"
                if support:
                    self.pages[url] += f"\n| API | `v1/responses` | {support} |"
                with self.assertRaises(ValueError):
                    self.verify()

    def test_text_translation_replacement_must_not_be_retired(self):
        self.pages[check.DEPRECATIONS] += "\n| Tomorrow | `text` | `new-text` |"
        with self.assertRaisesRegex(ValueError, "TranslationText selects deprecated text"):
            self.verify()

    def test_text_translation_ignores_unrelated_retirement_advice(self):
        self.pages[check.DEPRECATIONS] += "\n| Tomorrow | `unrelated` | `other-text` |"
        self.verify()

    def test_new_image_family_despite_old_section(self):
        self.pages[check.IMAGE] = self.pages[check.IMAGE].replace("(#gpt-image-40)", "(#gpt-image-30)")
        with self.assertRaisesRegex(ValueError, "current guidance recommends old"):
            self.verify()

    def test_ambiguous_recommendation(self):
        self.pages[check.CAPTIONS] += " Alternatively, configure input transcription with `other`."
        with self.assertRaisesRegex(ValueError, "unambiguous"):
            self.verify()

    def test_new_advice_alongside_previous_recommendation(self):
        previous = "Migrate to `gpt-voice` using the [Realtime API](guide)."
        reviewed = {check.SPEECH: check.guidance_digest(previous)}
        changed = previous + "\n\nFor new speech synthesis, prefer `gpt-realtime-2.2-mini`."
        with self.assertRaisesRegex(ValueError, "Model guidance changed"):
            check.verify_reviewed_guidance(lambda url: changed, reviewed)
        new_example = previous + '\n\n```python\n# use gpt-example in this example\n```'
        check.verify_reviewed_guidance(lambda url: new_example, reviewed)

    def test_deprecation_even_if_guide_lags(self):
        self.pages[check.DEPRECATIONS] += "\n| Tomorrow | `older` \\| `image` | `new` |"
        with self.assertRaisesRegex(ValueError, "deprecated image"):
            self.verify()

    def test_missing_role_specific_endpoint(self):
        url = check.DOCS + "models/image.md"
        self.pages[url] = self.pages[url].replace("`v1/images/edits` | Supported", "`v1/images/edits` | Not supported")
        with self.assertRaisesRegex(ValueError, "v1/images/edits"):
            self.verify()

    def test_model_card_independently_reports_deprecation(self):
        self.pages[check.DOCS + "models/voice.md"] += "\nStatus: Deprecated"
        with self.assertRaisesRegex(ValueError, "inactive or unrecognized status"):
            self.verify()

    def test_retired_route_despite_supported_model_card(self):
        self.pages[check.DEPRECATIONS] += "\n| Tomorrow | `/v1/realtime/translations` | `/v1/new` |"
        with self.assertRaisesRegex(ValueError, "deprecated endpoint v1/realtime/translations"):
            self.verify()

    def test_wrong_nested_caption_model(self):
        self.pages[check.CAPTION_EVENTS] = self.pages[check.CAPTION_EVENTS].replace("captions", "wrong")
        with self.assertRaisesRegex(ValueError, "input transcription"):
            self.verify()

    def test_invalid_inventory_and_changed_guide(self):
        with self.assertRaisesRegex(ValueError, "Invalid model declaration"):
            check.runtime_models('package modeldefaults\nconst (\nSpeech = other.Source\n)')
        self.pages[check.SPEECH] = "# Page unavailable"
        with self.assertRaisesRegex(ValueError, "unambiguous"):
            self.verify()
