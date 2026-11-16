#!/usr/bin/env python3
"""Drive real audio commands in owned PTYs. Requires a prior PTY reservation."""

import argparse
from functools import partial
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import re
import shutil


def load(name, path):
    spec = importlib.util.spec_from_file_location(name, path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--binary", type=Path, required=True)
    parser.add_argument("--source", required=True)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--repo-root", type=Path, default=os.environ.get("DEMO_REPO_ROOT"))
    parser.add_argument("--case", choices=("workflow", "compact", "navigation", "navigation-compact", "failure", "failure-compact", "wide",
                                          "ctrl-c", "sigterm", "busy-ctrl-c", "busy-sigterm",
                                          "stopped-reader", "startup", "print-command", "all"), default="all")
    parser.add_argument("--theme", choices=("dark", "light", "no-color"), default="dark")
    parser.add_argument("--asciinema", default=shutil.which("asciinema"))
    parser.add_argument("--navigation-observer", type=Path,
                        help="Optional hash-verified observer override; the repository adapter is the default")
    parser.add_argument("--observer-source-directory", type=Path,
                        help="Pinned development sources installed by setup_observer.py")
    parser.add_argument("--emit", action="store_true", help="Forward actual PTY bytes for the shared recorder")
    parser.add_argument("--no-resize", action="store_true", help="Keep the emitted comparison workflow at 90x34")
    args = parser.parse_args()
    root = args.repo_root or Path(__file__).resolve().parents[3]
    navigation_cases = ("navigation", "navigation-compact")
    failure_cases = ("failure", "failure-compact")
    observed_cases = (*navigation_cases, *failure_cases)
    needs_navigation_observer = args.case in (*observed_cases, "all")
    if not args.asciinema and args.case not in (*observed_cases, "print-command"):
        raise SystemExit("asciinema is required for real terminal-screen reconstruction")
    if not re.fullmatch(r"[0-9a-f]{40}", args.source):
        raise SystemExit("source must be a complete commit identity")
    if args.emit and args.case != "workflow":
        raise SystemExit("--emit records only the workflow case")
    if args.no_resize and not (args.emit and args.case == "workflow"):
        raise SystemExit("--no-resize applies only to the emitted comparison workflow")
    observer_factory = None
    if needs_navigation_observer:
        adapter_path = args.navigation_observer or Path(__file__).with_name("observer.py")
        if not adapter_path.is_file():
            raise SystemExit("Navigation observer adapter is missing from the selected source")
        adapter_sha256 = hashlib.sha256(adapter_path.read_bytes()).hexdigest()
        repository_adapter = "dff1c3441e77ca8016f1dbbb87b82e2478f0387d48b6bf17d80904f3815cfc86"
        legacy_adapter = "6b97dfa956642d7fe3c26bde091b9e932775e72c20973b128923389de4dae79e"
        if adapter_sha256 not in (repository_adapter, legacy_adapter):
            raise SystemExit("Navigation observer adapter does not match the reviewed source")
        navigation_adapter = load("audio_navigation_observer", adapter_path)
        if not callable(getattr(navigation_adapter, "create_observer", None)):
            raise SystemExit("Navigation observer adapter has no supported factory")
        if adapter_sha256 == repository_adapter:
            try:
                navigation_adapter.verify_sources(args.observer_source_directory)
            except ValueError as error:
                raise SystemExit(str(error)) from error
            observer_factory = partial(navigation_adapter.create_observer,
                                       package_directory=args.observer_source_directory)
        else:
            if args.observer_source_directory is not None:
                raise SystemExit("The legacy observer override owns its existing source directory")
            observer_factory = navigation_adapter.create_observer
    elif args.navigation_observer is not None or args.observer_source_directory is not None:
        raise SystemExit("Observer options apply only to navigation/failure cases or --case all")

    args.output.mkdir(parents=True, exist_ok=False)
    picker = load("audio_terminal", root / "scripts/image_picker_harness.py")
    fixture = load("audio_fixture", Path(__file__).with_name("fixture.py"))
    context = load("audio_case_context", Path(__file__).with_name("case_context.py"))
    scenarios = load("audio_scenarios", Path(__file__).with_name("scenarios.py"))
    run = context.Harness(args, picker, fixture, observer_factory)
    cases = scenarios.SCENARIOS if args.case == "all" else (args.case,)
    with run.lifecycle():
        for name in cases:
            context.CaseContext(run, name).execute(scenarios.SCENARIOS[name])
    if not args.emit:
        print(json.dumps({"passed": len(run.report["cases"]), "source": args.source,
                          "binary_sha256": run.report["binary_sha256"]}))
    return 130 if args.emit else 0


if __name__ == "__main__":
    raise SystemExit(main())
