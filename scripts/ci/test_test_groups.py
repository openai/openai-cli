"""Exercise the real test launcher with synthetic package discovery and Go calls."""
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest


class TestGroups(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        (self.root / 'scripts').mkdir()
        (self.root / 'bin').mkdir()
        self.script = self.root / 'scripts/test'
        self.script.write_text((Path(__file__).resolve().parents[1] / 'test').read_text())
        self.log = self.root / 'calls.jsonl'
        fake_go = self.root / 'bin/go'
        fake_go.write_text(f'#!{sys.executable}\n' + '''import json, os, sys
if sys.argv[1] == 'list':
    print(os.environ['PACKAGES'])
    sys.exit(int(os.environ.get('LIST_EXIT', '0')))
with open(os.environ['CALL_LOG'], 'a') as out:
    out.write(json.dumps([os.environ.get('GOOS', ''), sys.argv[1:]]) + '\\n')
sys.exit(int(os.environ.get('TEST_EXIT', '0')))
''')
        fake_go.chmod(0o755)
        self.packages = [
            'github.com/openai/openai-cli/cmd/openai',
            'github.com/openai/openai-cli/pkg/cmd',
            'github.com/openai/openai-cli/internal/payloadtest',
            'github.com/openai/openai-cli/future/package',
        ]

    def run_script(self, group='', **extra):
        self.log.write_text('')
        env = dict(os.environ, PATH=str(self.root / 'bin') + os.pathsep + os.environ['PATH'],
                   TEST_API_BASE_URL='http://127.0.0.1:4010', CALL_LOG=str(self.log),
                   TEST_PACKAGE_GROUP=group, PACKAGES='\n'.join(self.packages),
                   SKIP_WINDOWS_TEST_COMPILE='1', GOOS='', GOARCH='')
        env.update(extra)
        result = subprocess.run(['bash', str(self.script), '-p', '1', '-count=1'],
                                env=env, capture_output=True, text=True)
        calls = [json.loads(line) for line in self.log.read_text().splitlines()]
        return result, calls

    def test_groups_partition_every_package_including_future_packages(self):
        cli, cli_calls = self.run_script('cli')
        other, other_calls = self.run_script('other')
        self.assertEqual(cli.returncode, 0, cli.stderr)
        self.assertEqual(other.returncode, 0, other.stderr)
        self.assertEqual(cli_calls, [['', ['test', './cmd/openai', '-p', '1', '-count=1']]])
        self.assertEqual(other_calls, [['', ['test', *self.packages[1:], '-p', '1', '-count=1']]])

    def test_default_keeps_all_packages_and_windows_compilation(self):
        result, calls = self.run_script(SKIP_WINDOWS_TEST_COMPILE='0')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(calls, [['', ['test', './...', '-p', '1', '-count=1']],
                                 ['windows', ['test', '-c', './...', '-p', '1', '-count=1']]])

    def test_invalid_group_and_failed_discovery_do_not_run_tests(self):
        for group, extra in [('typo', {}), ('other', {'LIST_EXIT': '7'}),
                             ('other', {'PACKAGES': self.packages[0]})]:
            with self.subTest(group=group, extra=extra):
                result, calls = self.run_script(group, **extra)
                self.assertNotEqual(result.returncode, 0)
                self.assertEqual(calls, [])

    def test_failed_tests_propagate(self):
        result, _ = self.run_script('cli', TEST_EXIT='9')
        self.assertEqual(result.returncode, 9)
