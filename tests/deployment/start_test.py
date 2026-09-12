"""Exercise start.sh with an isolated fake Docker CLI; never touch real containers."""
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]


class StartTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        shutil.copy2(ROOT / "start.sh", self.root / "start.sh")
        self.bin = self.root / "bin"
        self.bin.mkdir()
        docker = self.bin / "docker"
        docker.write_text('''#!/bin/sh
printf '%s\\n' "$*" >> "$DOCKER_CALLS"
test "$PWD" = "$EXPECTED_CWD" || exit 90
if [ "$*" = "compose build" ]; then exit "${BUILD_STATUS:-0}"; fi
test "$*" = "compose up -d --no-build" || exit 91
''')
        docker.chmod(0o700)
        self.env = dict(os.environ, PATH=f"{self.bin}:{os.environ['PATH']}",
                        DOCKER_CALLS=str(self.root / "calls"), EXPECTED_CWD=str(self.root))
        for name in ("PROXYFLEET_DATA_DIR", "PROXYFLEET_UID", "PROXYFLEET_GID"):
            self.env.pop(name, None)

    def run_start(self, status=0):
        self.env["BUILD_STATUS"] = str(status)
        result = subprocess.run([str(self.root / "start.sh")], cwd="/tmp", env=self.env,
                                capture_output=True, text=True)
        path = self.root / "calls"
        calls = path.read_text().splitlines() if path.exists() else []
        return result, calls

    def test_fresh_start_builds_then_starts_without_example_config(self):
        result, calls = self.run_start()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(calls, ["compose build", "compose up -d --no-build"])
        self.assertEqual((self.root / "data").stat().st_mode & 0o777, 0o700)
        self.assertFalse((self.root / "data/config.yaml").exists())

    def test_failed_build_leaves_current_data_and_container_untouched(self):
        data = self.root / "data"
        data.mkdir(mode=0o700)
        config = data / "config.yaml"
        config.write_text("synthetic-existing-config")
        config.chmod(0o600)
        result, calls = self.run_start(42)
        self.assertEqual(result.returncode, 42)
        self.assertEqual(calls, ["compose build"])
        self.assertEqual(config.read_text(), "synthetic-existing-config")
        self.assertEqual(config.stat().st_mode & 0o777, 0o600)

    def test_legacy_directory_is_preserved(self):
        legacy = self.root / "config.yaml"
        legacy.mkdir()
        (legacy / "keep").write_text("preserve")
        result, calls = self.run_start()
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(calls, [])
        self.assertEqual((legacy / "keep").read_text(), "preserve")

    def test_bad_new_mount_is_preserved(self):
        bad = self.root / "data/nodes.txt"
        bad.mkdir(parents=True)
        result, calls = self.run_start()
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(calls, [])
        self.assertTrue(bad.is_dir())

    def test_legacy_files_are_allowed_after_migration(self):
        (self.root / "config.yaml").write_text("legacy")
        (self.root / "data").mkdir()
        (self.root / "data/config.yaml").write_text("migrated")
        result, _ = self.run_start()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual((self.root / "data/config.yaml").read_text(), "migrated")


if __name__ == "__main__":
    unittest.main()
