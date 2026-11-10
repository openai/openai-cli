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
                       "TranslationTranscript": "captions"}
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
                                "|---|---|---|\n| Yesterday | `old` \\| `older` | `voice` |",
        }
        for model, routes in [("voice", ["v1/realtime"]), ("image", ["v1/images/generations", "v1/images/edits"]),
                              ("translate", ["v1/realtime/translations"]), ("captions", [])]:
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

    def test_new_image_family_despite_old_section(self):
        self.pages[check.IMAGE] = self.pages[check.IMAGE].replace("(#gpt-image-40)", "(#gpt-image-30)")
        with self.assertRaisesRegex(ValueError, "current guidance recommends old"):
            self.verify()

    def test_ambiguous_recommendation(self):
        self.pages[check.CAPTIONS] += " Alternatively, configure input transcription with `other`."
        with self.assertRaisesRegex(ValueError, "unambiguous"):
            self.verify()

    def test_deprecation_even_if_guide_lags(self):
        self.pages[check.DEPRECATIONS] += "\n| Tomorrow | `older` \\| `image` | `new` |"
        with self.assertRaisesRegex(ValueError, "deprecated image"):
            self.verify()

    def test_missing_role_specific_endpoint(self):
        url = check.DOCS + "models/image.md"
        self.pages[url] = self.pages[url].replace("`v1/images/edits` | Supported", "`v1/images/edits` | Not supported")
        with self.assertRaisesRegex(ValueError, "v1/images/edits"):
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
